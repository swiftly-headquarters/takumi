package golang

import (
	"fmt"
	"go/format"
	"path"
	"strconv"
	"strings"

	"github.com/swiftly-headquarters/takumi/protospec"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// grpcReserved are the names every generated message or client already declares.
var grpcReserved = []string{"Client", "NewClient"}

// messageMethods can't be field names: the struct has methods of the same name.
var messageMethods = []string{"Reset", "String", "ProtoMessage", "XXX_MessageName", "XXX_OneofWrappers"}

type grpcGen struct {
	used    map[string]bool
	names   map[protoreflect.FullName]string // message and enum -> Go type name
	imports map[string]bool
	decls   strings.Builder
}

// GenerateGRPC writes a gRPC client SDK for the services of a .proto file.
//
// Messages are plain structs carrying legacy `protobuf` struct tags, which
// google.golang.org/protobuf and grpc-go encode without generated descriptors.
func GenerateGRPC(fd protoreflect.FileDescriptor) (map[string][]byte, error) {
	if fd.Services().Len() == 0 {
		return nil, fmt.Errorf("%s defines no services", fd.Path())
	}
	g := &grpcGen{used: map[string]bool{}, names: map[protoreflect.FullName]string{}, imports: map[string]bool{}}
	for _, name := range grpcReserved {
		g.used[name] = true
	}
	types := protospec.Reachable(fd)
	for _, m := range types.Messages {
		if wk, ok := wellKnown(m); ok {
			g.names[m.FullName()] = wk
			continue
		}
		g.names[m.FullName()] = g.reserve(exported(protospec.LocalName(m)))
	}
	for _, e := range types.Enums {
		g.names[e.FullName()] = g.reserve(exported(protospec.LocalName(e)))
	}
	for _, e := range types.Enums {
		g.enum(e)
	}
	for _, m := range types.Messages {
		if _, ok := wellKnown(m); ok {
			continue
		}
		if err := g.message(m); err != nil {
			return nil, err
		}
	}

	typeImports := []string{}
	if len(types.Messages) > 0 {
		typeImports = append(typeImports, "google.golang.org/protobuf/encoding/prototext", "google.golang.org/protobuf/protoadapt")
	}
	if len(types.Enums) > 0 {
		typeImports = append(typeImports, "strconv")
	}
	for imp := range g.imports {
		typeImports = append(typeImports, imp)
	}
	files := map[string]string{
		"types.go":  file(typeImports, g.decls.String()),
		"client.go": file([]string{"context", "google.golang.org/grpc"}, g.client(fd)),
	}
	out := map[string][]byte{}
	for name, src := range files {
		formatted, err := format.Source([]byte(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = formatted
	}
	return out, nil
}

// wellKnown maps google.protobuf.* messages to the types google.golang.org/protobuf already ships.
func wellKnown(m protoreflect.MessageDescriptor) (string, bool) {
	if m.ParentFile().Package() != "google.protobuf" {
		return "", false
	}
	pkg := strings.ReplaceAll(strings.TrimSuffix(path.Base(m.ParentFile().Path()), ".proto"), "_", "") + "pb"
	return pkg + "." + exported(protospec.LocalName(m)), true
}

func wellKnownImport(name string) string {
	pkg, _, _ := strings.Cut(name, ".")
	if pkg == "descriptorpb" {
		return "google.golang.org/protobuf/types/descriptorpb"
	}
	return "google.golang.org/protobuf/types/known/" + pkg
}

func (g *grpcGen) reserve(name string) string { return unique(name, g.used) }

func (g *grpcGen) enum(e protoreflect.EnumDescriptor) {
	name := g.names[e.FullName()]
	g.decls.WriteString(goDoc(protospec.Comment(e), false))
	fmt.Fprintf(&g.decls, "type %s int32\n\nconst (\n", name)
	var cases strings.Builder
	seen := map[protoreflect.EnumNumber]bool{}
	for i := 0; i < e.Values().Len(); i++ {
		v := e.Values().Get(i)
		fmt.Fprintf(&g.decls, "%s %s = %d\n", g.reserve(name+"_"+string(v.Name())), name, v.Number())
		if !seen[v.Number()] { // aliases share a number; the first name wins
			seen[v.Number()] = true
			fmt.Fprintf(&cases, "case %d:\nreturn %s\n", v.Number(), strconv.Quote(string(v.Name())))
		}
	}
	fmt.Fprintf(&g.decls, ")\n\nfunc (x %s) String() string {\nswitch x {\n%sdefault:\nreturn strconv.Itoa(int(x))\n}\n}\n\n", name, cases.String())
}

type oneofDecl struct{ iface, field string }

func (g *grpcGen) message(m protoreflect.MessageDescriptor) error {
	name := g.names[m.FullName()]
	used := map[string]bool{}
	for _, r := range messageMethods {
		used[r] = true
	}
	var body, wrappers strings.Builder
	var wrapperNames []string
	oneofs := map[string]oneofDecl{}

	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		if f.Kind() == protoreflect.GroupKind {
			return fmt.Errorf("%s.%s: groups are not supported", m.FullName(), f.Name())
		}
		fname := unique(exported(string(f.Name())), used)
		doc := goDoc(protospec.Comment(f), false)
		json := fmt.Sprintf(` json:"%s,omitempty"`, f.JSONName())

		if o := f.ContainingOneof(); wrapped(f) {
			d, ok := oneofs[string(o.Name())]
			if !ok {
				oname := fname // a proto3 optional field is its own one-member oneof
				if !o.IsSynthetic() {
					oname = unique(exported(string(o.Name())), used)
				}
				d = oneofDecl{iface: g.reserve("is" + name + "_" + oname), field: oname}
				oneofs[string(o.Name())] = d
				fmt.Fprintf(&body, "%s %s `protobuf_oneof:\"%s\"`\n", oname, d.iface, o.Name())
				fmt.Fprintf(&wrappers, "type %s interface{ %s() }\n\n", d.iface, d.iface)
			}
			wrapper := g.reserve(name + "_" + fname)
			wrapperNames = append(wrapperNames, wrapper)
			fmt.Fprintf(&wrappers, "%stype %s struct {\n%s %s `protobuf:\"%s\"%s`\n}\n\nfunc (*%s) %s() {}\n\n",
				doc, wrapper, fname, g.fieldType(f), g.tag(f), json, wrapper, d.iface)
			continue
		}

		tags := fmt.Sprintf("protobuf:%q", g.tag(f))
		if f.IsMap() {
			tags += fmt.Sprintf(" protobuf_key:%q protobuf_val:%q", g.tag(f.MapKey()), g.tag(f.MapValue()))
		}
		body.WriteString(doc)
		fmt.Fprintf(&body, "%s %s `%s%s`\n", fname, g.fieldType(f), tags, json)
	}

	g.decls.WriteString(goDoc(protospec.Comment(m), false))
	fmt.Fprintf(&g.decls, "type %s struct {\n%s}\n\n", name, body.String())
	g.decls.WriteString(wrappers.String())
	fmt.Fprintf(&g.decls, "func (m *%[1]s) Reset() { *m = %[1]s{} }\n", name)
	fmt.Fprintf(&g.decls, "func (m *%s) String() string { return prototext.Format(protoadapt.MessageV2Of(m)) }\n", name)
	fmt.Fprintf(&g.decls, "func (*%s) ProtoMessage() {}\n", name)
	fmt.Fprintf(&g.decls, "func (*%s) XXX_MessageName() string { return %q }\n", name, m.FullName())
	if len(wrapperNames) > 0 {
		fmt.Fprintf(&g.decls, "func (*%s) XXX_OneofWrappers() []any { return []any{", name)
		for _, w := range wrapperNames {
			fmt.Fprintf(&g.decls, "(*%s)(nil),", w)
		}
		g.decls.WriteString("} }\n")
	}
	g.decls.WriteString("\n")
	return nil
}

// tag builds the legacy `protobuf` struct tag of a field (also used for map keys and values).
func (g *grpcGen) tag(f protoreflect.FieldDescriptor) string {
	wire := map[protoreflect.Kind]string{
		protoreflect.BoolKind: "varint", protoreflect.EnumKind: "varint", protoreflect.Int32Kind: "varint",
		protoreflect.Uint32Kind: "varint", protoreflect.Int64Kind: "varint", protoreflect.Uint64Kind: "varint",
		protoreflect.Sint32Kind: "zigzag32", protoreflect.Sint64Kind: "zigzag64",
		protoreflect.Sfixed32Kind: "fixed32", protoreflect.Fixed32Kind: "fixed32", protoreflect.FloatKind: "fixed32",
		protoreflect.Sfixed64Kind: "fixed64", protoreflect.Fixed64Kind: "fixed64", protoreflect.DoubleKind: "fixed64",
		protoreflect.StringKind: "bytes", protoreflect.BytesKind: "bytes", protoreflect.MessageKind: "bytes",
	}[f.Kind()]
	card := "opt"
	switch {
	case f.Cardinality() == protoreflect.Required:
		card = "req"
	case f.IsList() || f.IsMap():
		card = "rep"
	}
	parts := []string{wire, strconv.Itoa(int(f.Number())), card}
	if f.IsPacked() {
		parts = append(parts, "packed")
	}
	parts = append(parts, "name="+string(f.Name()))
	if f.JSONName() != string(f.Name()) {
		parts = append(parts, "json="+f.JSONName())
	}
	if f.Syntax() == protoreflect.Proto3 {
		parts = append(parts, "proto3")
	}
	if f.Kind() == protoreflect.EnumKind {
		parts = append(parts, "enum="+string(f.Enum().FullName()))
	}
	if wrapped(f) {
		parts = append(parts, "oneof")
	}
	return strings.Join(parts, ",")
}

func (g *grpcGen) fieldType(f protoreflect.FieldDescriptor) string {
	switch {
	case f.IsMap():
		return "map[" + g.scalar(f.MapKey()) + "]" + g.scalar(f.MapValue())
	case f.IsList():
		return "[]" + g.scalar(f)
	}
	t := g.scalar(f)
	// Proto2 scalars with presence are pointers. Proto3 optional ones are wrapped, see wrapped.
	if f.ContainingOneof() == nil && f.HasPresence() && f.Kind() != protoreflect.MessageKind && f.Kind() != protoreflect.BytesKind {
		return "*" + t
	}
	return t
}

// wrapped reports whether f is a oneof member that needs a wrapper struct. That includes proto3
// `optional` scalars: the legacy struct tags can't express presence on a proto3 field any other way.
func wrapped(f protoreflect.FieldDescriptor) bool {
	o := f.ContainingOneof()
	return o != nil && (!o.IsSynthetic() || f.Kind() != protoreflect.MessageKind)
}

// scalar is the Go type of one element of f.
func (g *grpcGen) scalar(f protoreflect.FieldDescriptor) string {
	switch f.Kind() {
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return "int32"
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "uint32"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return "int64"
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "uint64"
	case protoreflect.FloatKind:
		return "float32"
	case protoreflect.DoubleKind:
		return "float64"
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return "[]byte"
	case protoreflect.EnumKind:
		return g.names[f.Enum().FullName()]
	}
	return "*" + g.messageName(f.Message())
}

func (g *grpcGen) messageName(m protoreflect.MessageDescriptor) string {
	name := g.names[m.FullName()]
	if _, ok := wellKnown(m); ok {
		g.imports[wellKnownImport(name)] = true
	}
	return name
}

func (g *grpcGen) client(fd protoreflect.FileDescriptor) string {
	var b strings.Builder
	used := map[string]bool{}
	type service struct{ field, class string }
	var services []service
	for i := 0; i < fd.Services().Len(); i++ {
		s := fd.Services().Get(i)
		services = append(services, service{unique(exported(string(s.Name())), used), g.reserve(exported(string(s.Name())) + "Client")})
	}

	b.WriteString("// Client is the entry point of the SDK. Create it from a connection with grpc.NewClient.\ntype Client struct {\n")
	for _, s := range services {
		fmt.Fprintf(&b, "%s *%s\n", s.field, s.class)
	}
	b.WriteString("}\n\n// NewClient returns a Client that sends every call over cc.\nfunc NewClient(cc grpc.ClientConnInterface) *Client {\nreturn &Client{\n")
	for _, s := range services {
		fmt.Fprintf(&b, "%s: &%s{cc: cc},\n", s.field, s.class)
	}
	b.WriteString("}\n}\n\n")

	for i, s := range services {
		sd := fd.Services().Get(i)
		doc := protospec.Comment(sd)
		if doc == "" {
			doc = fmt.Sprintf("%s calls the %s service.", s.class, sd.FullName())
		}
		b.WriteString(goDoc(doc, false))
		fmt.Fprintf(&b, "type %s struct{ cc grpc.ClientConnInterface }\n\n", s.class)
		methods := map[string]bool{}
		for j := 0; j < sd.Methods().Len(); j++ {
			g.method(&b, s.class, sd, sd.Methods().Get(j), unique(exported(string(sd.Methods().Get(j).Name())), methods))
		}
	}
	return b.String()
}

func (g *grpcGen) method(b *strings.Builder, class string, sd protoreflect.ServiceDescriptor, m protoreflect.MethodDescriptor, name string) {
	in, out := g.messageName(m.Input()), g.messageName(m.Output())
	full := strconv.Quote("/" + string(sd.FullName()) + "/" + string(m.Name()))
	doc := protospec.Comment(m)
	if doc == "" {
		doc = fmt.Sprintf("%s calls %s.", name, full)
	}
	b.WriteString(goDoc(doc, false))
	cs, ss := m.IsStreamingClient(), m.IsStreamingServer()
	if !cs && !ss {
		fmt.Fprintf(b, "func (c *%s) %s(ctx context.Context, in *%s, opts ...grpc.CallOption) (*%s, error) {\nout := new(%s)\nif err := c.cc.Invoke(ctx, %s, in, out, opts...); err != nil {\nreturn nil, err\n}\nreturn out, nil\n}\n\n",
			class, name, in, out, out, full)
		return
	}
	var ret, sig string
	switch {
	case ss && !cs:
		ret, sig = "grpc.ServerStreamingClient["+out+"]", "ctx context.Context, in *"+in+", opts ...grpc.CallOption"
	case cs && !ss:
		ret, sig = "grpc.ClientStreamingClient["+in+", "+out+"]", "ctx context.Context, opts ...grpc.CallOption"
	default:
		ret, sig = "grpc.BidiStreamingClient["+in+", "+out+"]", "ctx context.Context, opts ...grpc.CallOption"
	}
	fmt.Fprintf(b, "func (c *%s) %s(%s) (%s, error) {\nstream, err := c.cc.NewStream(ctx, &grpc.StreamDesc{StreamName: %q, ServerStreams: %t, ClientStreams: %t}, %s, opts...)\nif err != nil {\nreturn nil, err\n}\nx := &grpc.GenericClientStream[%s, %s]{ClientStream: stream}\n",
		class, name, sig, ret, string(m.Name()), ss, cs, full, in, out)
	if ss && !cs {
		b.WriteString("if err := x.ClientStream.SendMsg(in); err != nil {\nreturn nil, err\n}\nif err := x.ClientStream.CloseSend(); err != nil {\nreturn nil, err\n}\n")
	}
	b.WriteString("return x, nil\n}\n\n")
}
