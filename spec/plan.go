package spec

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Route is where one operation lives in an SDK: api.<Resource...>.<Method>().
type Route struct {
	Resource []string // camelCase namespace chain, empty for top-level methods
	Method   string
	Op       Op
}

var versionRe = regexp.MustCompile(`^(api|v\d+(\.\d+)*)$`)

// Plan deduces a resource.method name for every operation, from the strongest
// signal to the weakest:
//  1. a dotted or slashed operationId ("repos/list-for-org", "pets.list")
//  2. the URL path (resource chain) plus HTTP method and sibling paths (verb)
//  3. operationId with the resource noun stripped, then a numeric suffix
func (d *Document) Plan() []Route {
	var routes []Route
	for _, op := range d.Operations() {
		chain, method := d.route(op)
		routes = append(routes, Route{Resource: chain, Method: method, Op: op})
	}
	d.dedupe(routes)
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := strings.Join(routes[i].Resource, "."), strings.Join(routes[j].Resource, ".")
		if a != b {
			return a < b
		}
		return routes[i].Method < routes[j].Method
	})
	return routes
}

type seg struct {
	name  string
	param bool
}

func segments(path string) []seg {
	var out []seg
	for s := range strings.SplitSeq(strings.Trim(path, "/"), "/") {
		switch {
		case s == "":
		case strings.Contains(s, "{"):
			out = append(out, seg{param: true})
		case len(out) == 0 && versionRe.MatchString(s):
		default:
			out = append(out, seg{name: Camel(s)})
		}
	}
	return out
}

// route returns the resource chain and method for op. Method is "" when the
// path rules can't decide; dedupe fills it from the operationId.
func (d *Document) route(op Op) ([]string, string) {
	if parts := strings.FieldsFunc(op.OperationID, func(r rune) bool { return r == '/' || r == '.' }); len(parts) > 1 {
		var chain []string
		for _, s := range parts[:len(parts)-1] {
			chain = append(chain, Camel(s))
		}
		return chain, Camel(parts[len(parts)-1])
	}

	segs := segments(op.Path)
	var chain []string
	for _, s := range segs {
		if !s.param {
			chain = append(chain, s.name)
		}
	}
	last := len(segs) - 1
	byItem := last >= 0 && segs[last].param

	// An operation alone on a path with no /x/{id} sibling is an action, not a resource:
	// POST /user/createWithList -> user.createWithList(), GET /user/login -> user.login().
	// Lists stay resources so GET /user/pets is user.pets.list().
	lone := d.Paths[op.Path] != nil && len(d.Paths[op.Path].operations()) == 1
	if lone && !byItem && len(chain) >= 2 && !d.hasItems(op.Path) && (op.Method == "post" || op.Method == "get" && !d.returnsList(op)) {
		return chain[:len(chain)-1], segs[last].name
	}

	switch op.Method {
	case "get":
		if !byItem && (d.hasItems(op.Path) || d.returnsList(op)) {
			return chain, "list"
		}
		return chain, "get"
	case "post":
		if !byItem {
			return chain, "create"
		}
		if d.has(op.Path, "put", "patch") {
			return chain, ""
		}
		return chain, "update"
	case "put":
		if d.has(op.Path, "patch") {
			return chain, "replace"
		}
		return chain, "update"
	case "patch":
		return chain, "update"
	}
	return chain, op.Method
}

func (d *Document) has(path string, methods ...string) bool {
	item := d.Paths[path]
	if item == nil {
		return false
	}
	ops := item.operations()
	return slices.ContainsFunc(methods, func(m string) bool { return ops[m] != nil })
}

// hasItems reports whether /x/{id} exists next to /x, i.e. /x is a collection.
func (d *Document) hasItems(path string) bool {
	prefix := strings.TrimSuffix(path, "/") + "/{"
	for p := range d.Paths {
		if rest, ok := strings.CutPrefix(p, prefix); ok && !strings.Contains(rest, "/") {
			return true
		}
	}
	return false
}

var paginationParams = []string{"limit", "page", "per_page", "page_size", "pagesize", "cursor", "offset", "after", "before", "starting_after"}

// returnsList guesses a GET returns many items: an array response, a wrapper
// object holding one (data/items/results), or pagination query parameters.
func (d *Document) returnsList(op Op) bool {
	for _, p := range op.Params {
		if p.In == "query" && slices.Contains(paginationParams, strings.ToLower(p.Name)) {
			return true
		}
	}
	for _, code := range SortedKeys(op.Responses) {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		for _, mt := range op.Responses[code].Content {
			if s := d.deref(mt.Schema); s != nil {
				if slices.Contains(s.Type, "array") {
					return true
				}
				for _, key := range []string{"data", "items", "results"} {
					if p := d.deref(s.Properties[key]); p != nil && slices.Contains(p.Type, "array") {
						return true
					}
				}
			}
		}
		return false
	}
	return false
}

func (d *Document) deref(s *Schema) *Schema {
	for i := 0; s != nil && s.Ref != "" && i < 16; i++ {
		s = d.Components.Schemas[SchemaName(s.Ref)]
	}
	return s
}

// dedupe resolves empty names and collisions within one resource, and with its child resources.
func (d *Document) dedupe(routes []Route) {
	children := map[string]map[string]bool{}
	nodes := map[string][]int{}
	for i, r := range routes {
		for j, name := range r.Resource {
			key := strings.Join(r.Resource[:j], ".")
			if children[key] == nil {
				children[key] = map[string]bool{}
			}
			children[key][name] = true
		}
		key := strings.Join(r.Resource, ".")
		nodes[key] = append(nodes[key], i)
	}
	for key, idx := range nodes {
		count := map[string]int{}
		for _, i := range idx {
			count[routes[i].Method]++
		}
		used := map[string]bool{}
		for name := range children[key] {
			used[name] = true
		}
		for _, i := range idx {
			r := &routes[i]
			if r.Method == "" || count[r.Method] > 1 {
				r.Method = fallback(r.Op, r.Resource)
			}
			name := r.Method
			for n := 2; used[r.Method]; n++ {
				r.Method = name + strconv.Itoa(n)
			}
			used[r.Method] = true
		}
	}
}

// fallback names an operation after its operationId minus the resource noun:
// updatePetWithForm under "pet" becomes updateWithForm.
func fallback(op Op, resource []string) string {
	src := op.OperationID
	if src == "" {
		src = op.Method + " " + op.Path
	}
	drop := map[string]bool{}
	if len(resource) > 0 {
		for _, w := range words(resource[len(resource)-1]) {
			w = strings.ToLower(w)
			drop[w], drop[strings.TrimSuffix(w, "s")] = true, true
		}
	}
	all := words(src)
	var keep []string
	for _, w := range all {
		if !drop[strings.ToLower(w)] {
			keep = append(keep, w)
		}
	}
	if len(keep) == 0 {
		keep = all
	}
	return camelWords(keep)
}

// Camel converts any identifier style (kebab, snake, dotted, Pascal) to camelCase.
func Camel(s string) string { return camelWords(words(s)) }

func camelWords(ws []string) string {
	var b strings.Builder
	for i, w := range ws {
		w = strings.ToLower(w)
		if i > 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(w)
	}
	return b.String()
}

// words splits on non-alphanumerics and on lower-to-upper camel boundaries.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) && len(cur) > 0 &&
			(unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]) ||
				(unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return out
}
