package spec

import "strings"

func PickMedia(content map[string]*MediaType) (string, *MediaType) {
	keys := SortedKeys(content)
	if len(keys) == 0 {
		return "", nil
	}
	for _, want := range []string{"json", "multipart", "form"} {
		for _, k := range keys {
			if MediaKind(k) == want {
				return k, content[k]
			}
		}
	}
	return keys[0], content[keys[0]]
}

func MediaKind(contentType string) string {
	ct, _, _ := strings.Cut(contentType, ";")
	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		return "json"
	case ct == "multipart/form-data":
		return "multipart"
	case ct == "application/x-www-form-urlencoded":
		return "form"
	}
	return "raw"
}
