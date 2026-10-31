package spec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swiftly-headquarters/takumi/spec"
)

func TestLoadExternalRefs(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"openapi.yaml": `
openapi: 3.0.0
paths:
  /pets:
    $ref: paths/pets.yaml
components:
  schemas:
    Pet:
      $ref: schemas/pet.yaml
`,
		"paths/pets.yaml": `
get:
  parameters:
    - $ref: ../common.yaml#/components/parameters/Limit
  responses:
    "200":
      description: ok
      content:
        application/json:
          schema:
            type: array
            items: { $ref: ../schemas/pet.yaml }
`,
		"schemas/pet.yaml": `
type: object
properties:
  owner: { $ref: "#/definitions/Owner" }
  friends: { type: array, items: { $ref: pet.yaml } }
definitions:
  Owner: { type: object, properties: { name: { type: string } } }
`,
		"common.yaml": `
components:
  parameters:
    Limit: { name: limit, in: query, schema: { type: integer } }
`,
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	doc, err := spec.Load(filepath.Join(dir, "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	pet := doc.Components.Schemas["Pet"]
	if pet == nil || pet.Properties["owner"].Ref != "#/components/schemas/Owner" || pet.Properties["friends"].Items.Ref != "#/components/schemas/Pet" {
		t.Fatalf("Pet not bundled with named refs: %+v", pet)
	}
	if doc.Components.Schemas["Owner"] == nil {
		t.Fatal("Owner not hoisted")
	}
	op := doc.Operations()[0]
	if len(op.Params) != 1 || op.Params[0].Name != "limit" {
		t.Fatalf("params = %+v", op.Params)
	}
	if ref := op.Responses["200"].Content["application/json"].Schema.Items.Ref; ref != "#/components/schemas/Pet" {
		t.Fatalf("response items ref = %q", ref)
	}
}

func TestParseSwagger2(t *testing.T) {
	doc, err := spec.Parse([]byte(`
swagger: "2.0"
info: { title: Pets, version: "1" }
host: api.example.com
basePath: /v1
schemes: [https]
paths:
  /pets:
    get:
      parameters:
        - { name: tags, in: query, type: array, items: { type: string } }
        - $ref: "#/parameters/Limit"
      responses:
        200:
          description: ok
          schema: { type: array, items: { $ref: "#/definitions/Pet" } }
        default: { $ref: "#/responses/Error" }
    post:
      consumes: [application/json]
      parameters:
        - { name: pet, in: body, required: true, schema: { $ref: "#/definitions/Pet" } }
      responses: { 201: { description: created } }
  /pets/{id}/photo:
    put:
      parameters:
        - { name: id, in: path, required: true, type: integer }
        - { name: file, in: formData, type: file, required: true }
      responses: { 204: { description: done } }
parameters:
  Limit: { name: limit, in: query, type: integer }
responses:
  Error: { description: error, schema: { $ref: "#/definitions/Error" } }
definitions:
  Pet: { type: object, properties: { name: { type: string, x-nullable: true } } }
  Error: { type: object }
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Servers[0].URL != "https://api.example.com/v1" {
		t.Errorf("server = %q", doc.Servers[0].URL)
	}
	if !doc.Components.Schemas["Pet"].Properties["name"].Nullable {
		t.Error("x-nullable not mapped")
	}
	ops := map[string]spec.Op{}
	for _, op := range doc.Operations() {
		ops[op.Method+" "+op.Path] = op
	}
	list := ops["get /pets"]
	if len(list.Params) != 2 || list.Params[0].Schema.Items == nil || list.Params[0].SerStyle() != "form:implode" || list.Params[1].Name != "limit" {
		t.Errorf("list params = %+v", list.Params)
	}
	if ref := list.Responses["200"].Content["application/json"].Schema.Items.Ref; ref != "#/components/schemas/Pet" {
		t.Errorf("list 200 items ref = %q", ref)
	}
	if list.Responses["default"].Content["application/json"].Schema.Ref != "#/components/schemas/Error" {
		t.Error("default response ref not resolved")
	}
	if b := ops["post /pets"].RequestBody; b == nil || !b.Required || b.Content["application/json"].Schema.Ref != "#/components/schemas/Pet" {
		t.Errorf("post body = %+v", b)
	}
	photo := ops["put /pets/{id}/photo"]
	if mt := photo.RequestBody.Content["multipart/form-data"]; mt == nil || mt.Schema.Properties["file"].Format != "binary" {
		t.Errorf("formData body = %+v", photo.RequestBody)
	}
	if len(photo.Params) != 1 || photo.Params[0].Schema.Type[0] != "integer" {
		t.Errorf("photo params = %+v", photo.Params)
	}
}

func TestPathStyle(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		p    spec.Parameter
		want string
	}{
		{spec.Parameter{In: "path"}, ""},
		{spec.Parameter{In: "path", Explode: &yes}, "simple:explode"},
		{spec.Parameter{In: "path", Style: "label"}, "label"},
		{spec.Parameter{In: "path", Style: "matrix", Explode: &yes}, "matrix:explode"},
		{spec.Parameter{In: "query", Explode: &no}, "form:implode"},
		{spec.Parameter{In: "query", Style: "deepObject"}, "deepObject"},
	} {
		if got := c.p.SerStyle(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.p, got, c.want)
		}
	}
}

func TestPathItemRefs(t *testing.T) {
	doc, err := spec.Parse([]byte(`
openapi: 3.1.0
paths:
  /pets:
    $ref: "#/components/pathItems/Pets"
  /animals:
    $ref: "#/components/pathItems/Alias"
components:
  pathItems:
    Alias: { $ref: "#/components/pathItems/Pets" }
    Pets:
      parameters: [{ $ref: "#/components/parameters/Limit" }]
      get: { operationId: listPets, responses: {} }
  parameters:
    Limit: { name: limit, in: query }
`))
	if err != nil {
		t.Fatal(err)
	}
	ops := doc.Operations()
	if len(ops) != 2 || ops[0].Path != "/animals" || ops[1].OperationID != "listPets" || ops[1].Params[0].Name != "limit" {
		t.Fatalf("ops = %+v", ops)
	}
	if _, err := spec.Parse([]byte(`{"openapi": "3.1.0", "paths": {"/x": {"$ref": "#/components/pathItems/Missing"}}}`)); err == nil {
		t.Fatal("want error for missing path item")
	}
}
