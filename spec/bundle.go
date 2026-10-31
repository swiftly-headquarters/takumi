package spec

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// bundle rewrites every external $ref (other files or URLs) in root, which was
// read from loc, so that only local refs remain. Components (schemas,
// parameters, ...) are copied into root under their own name; anything else,
// like a path item in its own file, is inlined.
func bundle(root map[string]any, loc string) error {
	b := &bundler{root: root, main: loc, files: map[string]any{loc: root}, names: map[string]string{}}
	b.swagger = strings.HasPrefix(fmt.Sprint(root["swagger"]), "2")

	// components.schemas.Pet: {$ref: pet.yaml} keeps the name Pet instead of becoming Pet2.
	for _, kind := range []string{"schemas", "parameters", "requestBodies", "responses"} {
		c := b.container(kind, false)
		for _, name := range SortedKeys(c) {
			n, ok := c[name].(map[string]any)
			ref, isRef := n["$ref"].(string)
			if !ok || !isRef || len(n) != 1 || strings.HasPrefix(ref, "#") {
				continue
			}
			target, frag := b.split(ref, loc)
			b.names[target+"#"+frag] = name
			node, err := b.node(target, frag)
			if err != nil {
				return err
			}
			if c[name], err = b.walk(node, target, kind == "schemas"); err != nil {
				return err
			}
		}
	}
	if _, err := b.walk(root, loc, false); err != nil {
		return err
	}

	paths, _ := root["paths"].(map[string]any)
	for p, item := range paths {
		for hops := 0; ; hops++ {
			ref, _ := obj(item)["$ref"].(string)
			name, ok := strings.CutPrefix(ref, "#/components/pathItems/")
			if !ok {
				break
			}
			if item = b.container("pathItems", false)[Unescape(name)]; item == nil || hops > 32 {
				return fmt.Errorf("unresolved or cyclic $ref %q", ref)
			}
		}
		paths[p] = clone(item)
	}
	return nil
}

type bundler struct {
	root    map[string]any
	main    string
	swagger bool
	files   map[string]any    // location -> decoded document
	names   map[string]string // location#pointer -> component name it was copied to
	depth   int
}

var componentRe = regexp.MustCompile(`^/(?:components/)?(schemas|definitions|parameters|requestBodies|responses)/([^/]+)$`)

// Schema keys whose values are data, not schemas, and are never walked.
var dataKeys = map[string]bool{"example": true, "examples": true, "default": true, "enum": true, "const": true}

// Schema keys holding a map of name -> schema.
var schemaMaps = map[string]bool{"properties": true, "patternProperties": true, "$defs": true, "definitions": true, "schemas": true}

// walk rewrites the external $refs under v, which came from loc. schema
// reports whether v is a schema; refs in schemas always become named schemas.
func (b *bundler) walk(v any, loc string, schema bool) (any, error) {
	var err error
	switch n := v.(type) {
	case []any:
		for i, e := range n {
			if n[i], err = b.walk(e, loc, schema); err != nil {
				return nil, err
			}
		}
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			return b.ref(n, ref, loc, schema)
		}
		for k, e := range n {
			switch {
			case schema && dataKeys[k], k == "example", k == "examples", strings.HasPrefix(k, "x-"):
			case schemaMaps[k] && (schema || k == "schemas" || k == "definitions"):
				m, _ := e.(map[string]any)
				for name, s := range m {
					if m[name], err = b.walk(s, loc, true); err != nil {
						return nil, err
					}
				}
			default:
				if n[k], err = b.walk(e, loc, schema || k == "schema"); err != nil {
					return nil, err
				}
			}
		}
	}
	return v, nil
}

func (b *bundler) ref(n map[string]any, ref, loc string, schema bool) (any, error) {
	if strings.HasPrefix(ref, "#") && loc == b.main {
		return n, nil
	}
	target, frag := b.split(ref, loc)
	if target == b.main {
		n["$ref"] = "#" + frag
		return n, nil
	}

	kind, name := "", ""
	if m := componentRe.FindStringSubmatch(frag); m != nil {
		kind, name = m[1], Unescape(m[2])
	}
	if kind == "definitions" || schema {
		kind = "schemas"
	}
	if kind == "" {
		// Not a component: inline a copy, refs inside it resolve against its own file.
		if b.depth++; b.depth > 64 {
			return nil, fmt.Errorf("$ref %q: cyclic or too deeply nested", ref)
		}
		defer func() { b.depth-- }()
		node, err := b.node(target, frag)
		if err != nil {
			return nil, err
		}
		return b.walk(node, target, schema)
	}

	key := target + "#" + frag
	if existing, ok := b.names[key]; ok {
		n["$ref"] = b.prefix(kind) + escape(existing)
		return n, nil
	}
	if name == "" {
		name = strings.TrimSuffix(path.Base(filepath.ToSlash(target)), path.Ext(target))
		if i := strings.LastIndex(frag, "/"); i >= 0 && frag[i+1:] != "" {
			name = Unescape(frag[i+1:])
		}
	}
	c := b.container(kind, true)
	for base, i := name, 2; c[name] != nil; i++ {
		name = base + strconv.Itoa(i)
	}
	c[name] = map[string]any{} // reserve the name before walking, for cycles
	b.names[key] = name
	node, err := b.node(target, frag)
	if err != nil {
		return nil, err
	}
	if c[name], err = b.walk(node, target, kind == "schemas"); err != nil {
		return nil, err
	}
	n["$ref"] = b.prefix(kind) + escape(name)
	return n, nil
}

// split turns a $ref found in loc into the location of its document and a JSON pointer.
func (b *bundler) split(ref, loc string) (string, string) {
	file, frag, _ := strings.Cut(ref, "#")
	if file == "" {
		return loc, frag
	}
	if isURL(file) {
		return file, frag
	}
	if isURL(loc) {
		base, _ := url.Parse(loc)
		r, err := url.Parse(file)
		if err != nil {
			return file, frag
		}
		return base.ResolveReference(r).String(), frag
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(filepath.Dir(loc), filepath.FromSlash(file))
	}
	return filepath.Clean(file), frag
}

func (b *bundler) node(loc, frag string) (any, error) {
	doc, ok := b.files[loc]
	if !ok {
		data, err := read(loc)
		if err != nil {
			return nil, err
		}
		if doc, err = decode(data); err != nil {
			return nil, fmt.Errorf("%s: %w", loc, err)
		}
		b.files[loc] = doc
	}
	v := doc
	if frag = strings.Trim(frag, "/"); frag != "" {
		for seg := range strings.SplitSeq(frag, "/") {
			if s, err := url.PathUnescape(seg); err == nil {
				seg = s
			}
			seg = Unescape(seg)
			switch n := v.(type) {
			case map[string]any:
				v, ok = n[seg]
			case []any:
				i, err := strconv.Atoi(seg)
				ok = err == nil && i >= 0 && i < len(n)
				if ok {
					v = n[i]
				}
			default:
				ok = false
			}
			if !ok {
				return nil, fmt.Errorf("unresolved $ref %s#/%s", loc, frag)
			}
		}
	}
	return clone(v), nil
}

func (b *bundler) container(kind string, create bool) map[string]any {
	parent := b.root
	if b.swagger {
		if kind == "schemas" {
			kind = "definitions"
		}
	} else {
		c, _ := parent["components"].(map[string]any)
		if c == nil {
			if !create {
				return nil
			}
			c = map[string]any{}
			parent["components"] = c
		}
		parent = c
	}
	m, _ := parent[kind].(map[string]any)
	if m == nil && create {
		m = map[string]any{}
		parent[kind] = m
	}
	return m
}

func (b *bundler) prefix(kind string) string {
	if !b.swagger {
		return "#/components/" + kind + "/"
	}
	if kind == "schemas" {
		return "#/definitions/"
	}
	return "#/" + kind + "/"
}

func escape(s string) string { return strings.NewReplacer("~", "~0", "/", "~1").Replace(s) }

func clone(v any) any {
	switch n := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(n))
		for k, e := range n {
			m[k] = clone(e)
		}
		return m
	case []any:
		s := make([]any, len(n))
		for i, e := range n {
			s[i] = clone(e)
		}
		return s
	}
	return v
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func read(loc string) ([]byte, error) {
	if !isURL(loc) {
		return os.ReadFile(loc)
	}
	res, err := http.Get(loc)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", loc, res.Status)
	}
	return io.ReadAll(res.Body)
}
