package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Document struct {
	OpenAPI    string
	Info       Info
	Servers    []Server
	Paths      map[string]*PathItem
	Components Components
}

type Info struct {
	Title       string
	Version     string
	Description string
}

type Server struct {
	URL string
}

type Components struct {
	Schemas       map[string]*Schema
	Parameters    map[string]*Parameter
	RequestBodies map[string]*RequestBody
	Responses     map[string]*Response
}

type PathItem struct {
	Parameters                                          []*Parameter
	Get, Put, Post, Delete, Options, Head, Patch, Trace *Operation
}

type Operation struct {
	OperationID string
	Summary     string
	Description string
	Deprecated  bool
	Tags        []string
	Parameters  []*Parameter
	RequestBody *RequestBody
	Responses   map[string]*Response
}

type Parameter struct {
	Ref         string `json:"$ref"`
	Name        string
	In          string // path, query, header, cookie
	Style       string
	Explode     *bool
	Required    bool
	Description string
	Schema      *Schema
}

type RequestBody struct {
	Ref         string `json:"$ref"`
	Required    bool
	Description string
	Content     map[string]*MediaType
}

type Response struct {
	Ref         string `json:"$ref"`
	Description string
	Content     map[string]*MediaType
}

type MediaType struct {
	Schema *Schema
}

type Schema struct {
	Ref                  string `json:"$ref"`
	Type                 Types
	Format               string
	Description          string
	Nullable             bool
	Deprecated           bool
	Enum                 []any
	Const                any
	Items                *Schema
	Properties           map[string]*Schema
	Required             []string
	AdditionalProperties *Schema
	AllOf, OneOf, AnyOf  []*Schema

	// False is set for the boolean schema `false` (e.g. additionalProperties: false).
	False bool `json:"-"`
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true":
		*s = Schema{}
		return nil
	case "false":
		*s = Schema{False: true}
		return nil
	}
	type plain Schema
	return json.Unmarshal(data, (*plain)(s))
}

type Types []string

func (t *Types) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*t = Types{one}
		return nil
	}
	return json.Unmarshal(data, (*[]string)(t))
}

// Op is one HTTP operation with path-level parameters merged in and all
// non-schema $refs resolved.
type Op struct {
	*Operation
	Method string // lowercase: get, post, ...
	Path   string
	Params []*Parameter
}

func Load(path string) (*Document, error) {
	if !isURL(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	return parse(data, path)
}

func Parse(data []byte) (*Document, error) { return parse(data, "") }

func parse(data []byte, loc string) (*Document, error) {
	tree, err := decode(data)
	if err != nil {
		return nil, err
	}
	root, ok := tree.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("document is not an object")
	}
	if err := bundle(root, loc); err != nil {
		return nil, err
	}
	if v := fmt.Sprint(root["swagger"]); strings.HasPrefix(v, "2") {
		root = fromSwagger(root)
	}
	if data, err = json.Marshal(root); err != nil {
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		return nil, fmt.Errorf("unsupported openapi version %q (need 3.x or swagger 2.0)", doc.OpenAPI)
	}
	return &doc, doc.resolve()
}

func decode(data []byte) (any, error) {
	var tree any
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(data, &tree); err != nil {
			return nil, fmt.Errorf("json: %w", err)
		}
		return tree, nil
	}
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	return stringKeys(tree), nil
}

func stringKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = stringKeys(e)
		}
	case map[any]any:
		m := make(map[string]any, len(v))
		for k, e := range v {
			m[fmt.Sprint(k)] = stringKeys(e)
		}
		return m
	case []any:
		for i, e := range v {
			v[i] = stringKeys(e)
		}
	}
	return v
}

func (d *Document) resolve() error {
	c := &d.Components
	var err error
	resolveParams := func(ps []*Parameter) {
		for i := range ps {
			if err == nil {
				ps[i], err = deref(ps[i], func(p *Parameter) string { return p.Ref }, c.Parameters, "parameters")
			}
		}
	}
	for _, item := range d.Paths {
		resolveParams(item.Parameters)
		for _, op := range item.operations() {
			resolveParams(op.Parameters)
			if err == nil && op.RequestBody != nil {
				op.RequestBody, err = deref(op.RequestBody, func(b *RequestBody) string { return b.Ref }, c.RequestBodies, "requestBodies")
			}
			for code, r := range op.Responses {
				if err == nil {
					op.Responses[code], err = deref(r, func(r *Response) string { return r.Ref }, c.Responses, "responses")
				}
			}
		}
	}
	return err
}

func deref[T any](v *T, ref func(*T) string, m map[string]*T, kind string) (*T, error) {
	for hops := 0; v != nil && ref(v) != ""; hops++ {
		r := ref(v)
		name, ok := strings.CutPrefix(r, "#/components/"+kind+"/")
		if !ok || hops > 32 {
			return nil, fmt.Errorf("unsupported or cyclic $ref %q", r)
		}
		if v = m[Unescape(name)]; v == nil {
			return nil, fmt.Errorf("unresolved $ref %q", r)
		}
	}
	return v, nil
}

func Unescape(s string) string {
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
}

func SchemaName(ref string) string {
	name, ok := strings.CutPrefix(ref, "#/components/schemas/")
	if !ok || strings.Contains(name, "/") {
		return ""
	}
	return Unescape(name)
}

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func (p *PathItem) operations() map[string]*Operation {
	ops := map[string]*Operation{}
	for i, op := range []*Operation{p.Get, p.Put, p.Post, p.Delete, p.Options, p.Head, p.Patch, p.Trace} {
		if op != nil {
			ops[methods[i]] = op
		}
	}
	return ops
}

func (d *Document) Operations() []Op {
	var out []Op
	for path, item := range d.Paths {
		for method, op := range item.operations() {
			params := slices.Clone(op.Parameters)
			for _, shared := range item.Parameters {
				if !slices.ContainsFunc(op.Parameters, func(p *Parameter) bool { return p.Name == shared.Name && p.In == shared.In }) {
					params = append(params, shared)
				}
			}
			out = append(out, Op{Operation: op, Method: method, Path: path, Params: params})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return slices.Index(methods, out[i].Method) < slices.Index(methods, out[j].Method)
	})
	return out
}

func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (p *Parameter) SerStyle() string {
	style := p.Style
	if style == "" {
		style = "form"
		if p.In == "path" || p.In == "header" {
			style = "simple"
		}
	}
	explode := style == "form"
	if p.Explode != nil {
		explode = *p.Explode
	}
	switch p.In {
	case "path":
		if style != "simple" && style != "label" && style != "matrix" || style == "simple" && !explode {
			return ""
		}
		if explode {
			return style + ":explode"
		}
		return style
	case "query", "cookie":
		switch {
		case style == "form" && !explode:
			return "form:implode"
		case style == "deepObject" || (style == "spaceDelimited" || style == "pipeDelimited") && !explode:
			return style
		}
	}
	return ""
}
