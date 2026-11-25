package typescript

import (
	_ "embed"
	"fmt"
	"strings"
	"unicode"

	"github.com/swiftly-headquarters/takumi/protospec"
	"google.golang.org/protobuf/reflect/protoreflect"
)

//go:embed grpc_runtime.ts
var grpcRuntime string

type grpcGen struct {
	names   map[protoreflect.FullName]string
	schemas map[protoreflect.FullName]string
}

// GenerateGRPC writes a fetch-based gRPC-web client SDK for the services of a .proto file.
// gRPC-web carries unary and server-streaming calls only, so client-streaming and
// bidirectional methods are left out (with a comment in client.ts).
func GenerateGRPC(fd protoreflect.FileDescriptor) (map[string][]byte, error) {
	if fd.Services().Len() == 0 {
		return nil, fmt.Errorf("%s defines no services", fd.Path())
	}
	g := &grpcGen{names: map[protoreflect.FullName]string{}, schemas: map[protoreflect.FullName]string{}}
	used := map[string]bool{"Client": true, "BaseClient": true}
	types := protospec.Reachable(fd)
	for _, e := range types.Enums {
		g.names[e.FullName()] = unique(pascal(protospec.LocalName(e)), used)
	}
	for _, m := range types.Messages {
		g.names[m.FullName()] = unique(pascal(protospec.LocalName(m)), used)
	}
	for _, m := range types.Messages {
		g.schemas[m.FullName()] = unique(g.names[m.FullName()]+"Schema", used)
	}

	var t strings.Builder
	t.WriteString(header)
	t.WriteString("import type { MessageSchema } from \"./runtime.js\";\n\n")
	for _, e := range types.Enums {
		g.enum(&t, e)
	}
	for _, m := range types.Messages {
		g.message(&t, m)
	}

	return map[string][]byte{
		"types.ts":   []byte(strings.TrimRight(t.String(), "\n") + "\n"),
		"client.ts":  []byte(g.client(fd)),
		"runtime.ts": []byte(header + grpcRuntime),
		"index.ts":   []byte(header + "export * from \"./client.js\";\nexport * from \"./types.js\";\n"),
	}, nil
}

func (g *grpcGen) enum(b *strings.Builder, e protoreflect.EnumDescriptor) {
	name := g.names[e.FullName()]
	b.WriteString(doc(protospec.Comment(e), false, ""))
	fmt.Fprintf(b, "export const %s = {\n", name)
	for i := 0; i < e.Values().Len(); i++ {
		v := e.Values().Get(i)
		fmt.Fprintf(b, "  %s: %d,\n", key(string(v.Name())), v.Number())
	}
	fmt.Fprintf(b, "} as const;\nexport type %[1]s = (typeof %[1]s)[keyof typeof %[1]s];\n\n", name)
}

func (g *grpcGen) message(b *strings.Builder, m protoreflect.MessageDescriptor) {
	name := g.names[m.FullName()]
	b.WriteString(doc(protospec.Comment(m), false, ""))
	fmt.Fprintf(b, "export interface %s {\n", name)
	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		opt := ""
		if f.HasPresence() {
			opt = "?"
		}
		b.WriteString(doc(protospec.Comment(f), false, "  "))
		fmt.Fprintf(b, "  %s%s: %s;\n", key(f.JSONName()), opt, g.fieldType(f))
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(b, "export const %s: MessageSchema = {\n  fields: [\n", g.schemas[m.FullName()])
	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		parts := []string{"name: " + jsonString(f.JSONName()), fmt.Sprintf("no: %d", f.Number())}
		switch {
		case f.IsMap():
			v := f.MapValue()
			inner := fmt.Sprintf("key: %q, value: %q", kind(f.MapKey()), kind(v))
			if v.Message() != nil {
				inner += ", message: () => " + g.schemas[v.Message().FullName()]
			}
			parts = append(parts, `type: "message"`, "repeated: true", "map: { "+inner+" }")
		default:
			parts = append(parts, fmt.Sprintf("type: %q", kind(f)))
			if f.IsList() {
				parts = append(parts, "repeated: true")
			}
			if f.IsPacked() {
				parts = append(parts, "packed: true")
			}
			if f.Message() != nil {
				parts = append(parts, "message: () => "+g.schemas[f.Message().FullName()])
			}
		}
		if f.HasPresence() {
			parts = append(parts, "presence: true")
		}
		if o := f.ContainingOneof(); o != nil && !o.IsSynthetic() {
			parts = append(parts, "oneof: "+jsonString(string(o.Name())))
		}
		fmt.Fprintf(b, "    { %s },\n", strings.Join(parts, ", "))
	}
	b.WriteString("  ],\n};\n\n")
}

// kind is the runtime FieldType name of a field.
func kind(f protoreflect.FieldDescriptor) string {
	k := f.Kind().String() // "int32", "sfixed64", "message", ... (groups don't occur in proto3)
	if k == "group" {
		return "message"
	}
	return k
}

func (g *grpcGen) fieldType(f protoreflect.FieldDescriptor) string {
	switch {
	case f.IsMap():
		return "Record<string, " + g.scalar(f.MapValue()) + ">"
	case f.IsList():
		return g.scalar(f) + "[]"
	}
	return g.scalar(f)
}

func (g *grpcGen) scalar(f protoreflect.FieldDescriptor) string {
	switch f.Kind() {
	case protoreflect.BoolKind:
		return "boolean"
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return "Uint8Array"
	case protoreflect.Int64Kind, protoreflect.Uint64Kind, protoreflect.Sint64Kind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return "bigint"
	case protoreflect.EnumKind:
		return g.names[f.Enum().FullName()]
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return g.names[f.Message().FullName()]
	}
	return "number"
}

func (g *grpcGen) client(fd protoreflect.FileDescriptor) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("import * as T from \"./types.js\";\nimport { BaseClient, type DeepPartial } from \"./runtime.js\";\n\n")
	b.WriteString("export { BaseClient, GrpcError, type ClientOptions } from \"./runtime.js\";\n")

	used := map[string]bool{"Client": true, "BaseClient": true, "T": true}
	for name := range g.names {
		used[g.names[name]] = true
	}
	type service struct{ field, class string }
	var services []service
	for i := 0; i < fd.Services().Len(); i++ {
		s := fd.Services().Get(i)
		services = append(services, service{field: unique(lowerFirst(pascal(string(s.Name()))), map[string]bool{"unary": true, "stream": true, "options": true, "constructor": true}), class: unique(pascal(string(s.Name()))+"Client", used)})
	}

	for i, s := range services {
		sd := fd.Services().Get(i)
		text := protospec.Comment(sd)
		if text == "" {
			text = "Calls the " + string(sd.FullName()) + " service."
		}
		b.WriteString("\n" + doc(text, false, ""))
		fmt.Fprintf(&b, "export class %s {\n  constructor(private readonly base: BaseClient) {}\n", s.class)
		methods := map[string]bool{"base": true, "constructor": true}
		for j := 0; j < sd.Methods().Len(); j++ {
			m := sd.Methods().Get(j)
			if m.IsStreamingClient() {
				fmt.Fprintf(&b, "\n  // %s is not available: gRPC-web cannot send a stream of requests.\n", m.Name())
				continue
			}
			text := protospec.Comment(m)
			if text == "" {
				text = fmt.Sprintf("Calls %s/%s.", sd.FullName(), m.Name())
			}
			in, out := g.names[m.Input().FullName()], g.names[m.Output().FullName()]
			path := jsonString("/" + string(sd.FullName()) + "/" + string(m.Name()))
			ret, call := "Promise<T."+out+">", "unary"
			if m.IsStreamingServer() {
				ret, call = "AsyncGenerator<T."+out+">", "stream"
			}
			fmt.Fprintf(&b, "\n%s  %s(request: DeepPartial<T.%s>, init?: RequestInit): %s {\n    return this.base.%s(%s, T.%s, T.%s, request, init);\n  }\n",
				doc(text, false, "  "), unique(lowerFirst(pascal(string(m.Name()))), methods), in, ret, call, path, g.schemas[m.Input().FullName()], g.schemas[m.Output().FullName()])
		}
		b.WriteString("}\n")
	}

	b.WriteString("\nexport class Client extends BaseClient {\n")
	for _, s := range services {
		fmt.Fprintf(&b, "  readonly %s = new %s(this);\n", s.field, s.class)
	}
	b.WriteString("}\n")
	return b.String()
}

func lowerFirst(s string) string {
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
