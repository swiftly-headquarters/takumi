package golang_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swiftly-headquarters/takumi/generators/golang"
	"github.com/swiftly-headquarters/takumi/spec"
)

func TestGenerate(t *testing.T) {
	doc, err := spec.Load("../typescript/testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files, err := golang.Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	types, client := string(files["types.go"]), string(files["client.go"])

	for _, want := range []string{
		"type NewPet struct {",
		"`json:\"name\"`",
		"`json:\"tag,omitempty\"`",
		"type Pet struct {",
		"`json:\"id\"`",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.go missing %q\n%s", want, types)
		}
	}
	for _, want := range []string{
		`const defaultBaseURL = "https://petstore.example.com/v1"`,
		"type PetsService struct {",
		"func (s *PetsService) List(ctx context.Context, p PetsListParams, opts ...RequestOption) ([]Pet, error) {",
		`{in: "query", name: "tags", style: "pipeDelimited", value: p.Tags}`,
		`{in: "query", name: "filter", style: "deepObject", value: p.Filter}`,
		`{in: "cookie", name: "session", style: "", value: p.Session}`,
		`{in: "path", name: "petId", style: "matrix", value: p.PetId}`,
		`bodyKind:    "multipart"`,
		"func (s *PetsService) Delete(",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.go missing %q\n%s", want, client)
		}
	}
}

// TestSDK builds the generated package in a temp module, then runs its tests
// against an httptest server: path, query, cookie, multipart and error paths.
func TestSDK(t *testing.T) {
	doc, err := spec.Load("../typescript/testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files, err := golang.Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files["go.mod"] = []byte("module sdktest\n\ngo 1.22\n")
	files["runtime_test.go"] = []byte(runtimeTest)
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test on generated SDK failed: %v\n%s", err, out)
	}
}

const runtimeTest = `package sdk

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPathValue(t *testing.T) {
	cases := []struct {
		value any
		style string
		want  string
	}{
		{[]any{"a", "b"}, "", "a,b"},
		{[]any{"a", "b"}, "label", ".a,b"},
		{[]any{"a", "b"}, "label:explode", ".a.b"},
		{[]any{"a", "b"}, "matrix", ";id=a,b"},
		{[]any{"a", "b"}, "matrix:explode", ";id=a;id=b"},
		{map[string]any{"x": "1", "y": "2"}, "label:explode", ".x=1.y=2"},
		{map[string]any{"x": "1", "y": "2"}, "matrix:explode", ";x=1;y=2"},
		{map[string]any{"x": "1", "y": "2"}, "matrix", ";id=x,1,y,2"},
	}
	for _, c := range cases {
		if got := pathValue("id", norm(c.value), c.style); got != c.want {
			t.Errorf("pathValue(%v, %q) = %q, want %q", c.value, c.style, got, c.want)
		}
	}
}

func TestRequests(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		if r.URL.Path == "/pets/9" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("{\"message\":\"gone\"}"))
			return
		}
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL))
	ctx := context.Background()
	name := "a"

	if _, err := c.Pets.List(ctx, PetsListParams{Tags: []string{"a", "b"}, Filter: &PetsListFilter{Name: &name}, Session: &name}); err != nil {
		t.Fatal(err)
	}
	if got := seen.URL.Query().Get("tags"); got != "a|b" {
		t.Errorf("tags = %q, want a|b", got)
	}
	if got := seen.URL.Query().Get("filter[name]"); got != "a" {
		t.Errorf("filter[name] = %q, want a", got)
	}
	if got := seen.Header.Get("Cookie"); got != "session=a" {
		t.Errorf("Cookie = %q, want session=a", got)
	}

	if _, err := c.Pets.Photo.Update(ctx, PetsPhotoUpdateParams{PetId: 7, Body: &PetsPhotoUpdateBody{File: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if seen.RequestURI != "/pets/;petId=7/photo" {
		t.Errorf("RequestURI = %q, want /pets/;petId=7/photo", seen.RequestURI)
	}
	if !strings.HasPrefix(seen.Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data", seen.Header.Get("Content-Type"))
	}

	err := c.Pets.Delete(ctx, PetsDeleteParams{PetId: 9})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("Delete error = %v, want *APIError with status 404", err)
	}
}
`
