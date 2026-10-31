package spec

import (
	"fmt"
	"slices"
	"strings"
)

func fromSwagger(root map[string]any) map[string]any {
	consumes, produces := strs(root["consumes"]), strs(root["produces"])
	out := map[string]any{"openapi": "3.0.3", "info": root["info"]}

	host, _ := root["host"].(string)
	base, _ := root["basePath"].(string)
	if host != "" || base != "" {
		server := strings.TrimSuffix(base, "/")
		if host != "" {
			scheme := "https"
			if s := strs(root["schemes"]); len(s) > 0 && !slices.Contains(s, "https") {
				scheme = s[0]
			}
			server = scheme + "://" + host + server
		}
		out["servers"] = []any{map[string]any{"url": server}}
	}

	shared := obj(root["parameters"])
	params, responses := map[string]any{}, map[string]any{}
	for name, p := range shared {
		if pm := obj(p); pm["in"] != "body" && pm["in"] != "formData" {
			params[name] = param3(pm)
		}
	}
	for name, r := range obj(root["responses"]) {
		responses[name] = response3(obj(r), produces)
	}
	out["components"] = map[string]any{"schemas": root["definitions"], "parameters": params, "responses": responses}

	paths := map[string]any{}
	for p, item := range obj(root["paths"]) {
		it := obj(item)
		converted := map[string]any{}
		for _, m := range methods {
			if op := obj(it[m]); op != nil {
				converted[m] = op3(op, arr(it["parameters"]), shared, or(strs(op["consumes"]), consumes), or(strs(op["produces"]), produces))
			}
		}
		paths[p] = converted
	}
	out["paths"] = paths
	return refs3(out).(map[string]any)
}

func op3(op map[string]any, pathParams []any, shared map[string]any, consumes, produces []string) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"operationId", "summary", "description", "deprecated", "tags"} {
		if v, ok := op[k]; ok {
			out[k] = v
		}
	}

	var params []any
	var body map[string]any
	form, formRequired, file := map[string]any{}, []any{}, false
	seen := map[string]bool{}

	for _, p := range append(arr(op["parameters"]), pathParams...) {
		pm := obj(p)
		if ref, ok := pm["$ref"].(string); ok {
			pm = obj(shared[Unescape(strings.TrimPrefix(ref, "#/parameters/"))])
		}
		id := fmt.Sprint(pm["in"], " ", pm["name"])
		if pm == nil || seen[id] {
			continue
		}
		seen[id] = true
		switch pm["in"] {
		case "body":
			body = pm
		case "formData":
			form[fmt.Sprint(pm["name"])] = schema3(pm)
			if pm["required"] == true {
				formRequired = append(formRequired, pm["name"])
			}
			file = file || pm["type"] == "file"
		default:
			params = append(params, param3(pm))
		}
	}
	if len(params) > 0 {
		out["parameters"] = params
	}
	switch {
	case body != nil:
		out["requestBody"] = map[string]any{
			"description": body["description"],
			"required":    body["required"] == true,
			"content":     map[string]any{pick(consumes, "application/json"): map[string]any{"schema": body["schema"]}},
		}
	case len(form) > 0:
		ct := "application/x-www-form-urlencoded"
		if file || slices.Contains(consumes, "multipart/form-data") {
			ct = "multipart/form-data"
		}
		schema := map[string]any{"type": "object", "properties": form}
		if len(formRequired) > 0 {
			schema["required"] = formRequired
		}
		out["requestBody"] = map[string]any{"required": len(formRequired) > 0, "content": map[string]any{ct: map[string]any{"schema": schema}}}
	}

	responses := map[string]any{}
	for code, r := range obj(op["responses"]) {
		if rm := obj(r); rm["$ref"] != nil {
			responses[code] = rm
		} else {
			responses[code] = response3(rm, produces)
		}
	}
	out["responses"] = responses
	return out
}

func param3(p map[string]any) map[string]any {
	out := map[string]any{"name": p["name"], "in": p["in"], "required": p["required"] == true, "schema": schema3(p)}
	if d, ok := p["description"]; ok {
		out["description"] = d
	}
	if p["type"] == "array" && (p["in"] == "query" || p["in"] == "formData") {
		switch p["collectionFormat"] {
		case "multi":
			out["explode"] = true
		case "ssv":
			out["style"], out["explode"] = "spaceDelimited", false
		case "pipes":
			out["style"], out["explode"] = "pipeDelimited", false
		default: // csv (the 2.0 default), tsv
			out["explode"] = false
		}
	}
	return out
}

var schemaFields = []string{"type", "format", "items", "enum", "default", "maximum", "minimum", "exclusiveMaximum",
	"exclusiveMinimum", "maxLength", "minLength", "pattern", "maxItems", "minItems", "uniqueItems", "multipleOf"}

func schema3(p map[string]any) map[string]any {
	if p["type"] == "file" {
		return map[string]any{"type": "string", "format": "binary"}
	}
	s := map[string]any{}
	for _, k := range schemaFields {
		if v, ok := p[k]; ok {
			s[k] = v
		}
	}
	if items := obj(s["items"]); items != nil {
		s["items"] = schema3(items)
	}
	return s
}

func response3(r map[string]any, produces []string) map[string]any {
	out := map[string]any{"description": r["description"]}
	if out["description"] == nil {
		out["description"] = ""
	}
	if schema := obj(r["schema"]); schema != nil {
		if schema["type"] == "file" {
			schema = map[string]any{"type": "string", "format": "binary"}
		}
		out["content"] = map[string]any{pick(produces, "application/json"): map[string]any{"schema": schema}}
	}
	return out
}

func refs3(v any) any {
	switch n := v.(type) {
	case map[string]any:
		for k, e := range n {
			switch {
			case k == "$ref":
				if s, ok := e.(string); ok {
					s = strings.Replace(s, "#/definitions/", "#/components/schemas/", 1)
					s = strings.Replace(s, "#/parameters/", "#/components/parameters/", 1)
					n[k] = strings.Replace(s, "#/responses/", "#/components/responses/", 1)
				}
			case k == "x-nullable":
				n["nullable"] = e
			case k != "example" && k != "examples":
				n[k] = refs3(e)
			}
		}
	case []any:
		for i, e := range n {
			n[i] = refs3(e)
		}
	}
	return v
}

func pick(offered []string, want string) string {
	if len(offered) == 0 || slices.Contains(offered, want) {
		return want
	}
	for _, t := range offered {
		if strings.Contains(t, "json") {
			return t
		}
	}
	return offered[0]
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }

func strs(v any) []string {
	var out []string
	for _, e := range arr(v) {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

func or(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}
