package spec_test

import (
	"strings"
	"testing"

	"github.com/swiftly-headquarters/takumi/spec"
)

func TestPlan(t *testing.T) {
	doc, err := spec.Parse([]byte(`{
  "openapi": "3.0.0",
  "paths": {
    "/v1/pets": {"get": {"responses": {}}, "post": {"responses": {}}},
    "/v1/pets/{id}": {"get": {"responses": {}}, "put": {"responses": {}}, "post": {"operationId": "updatePetWithForm", "responses": {}}},
    "/v1/user/login": {"get": {"responses": {}}},
    "/v1/user/createWithList": {"post": {"responses": {}}},
    "/v1/user/pets": {"get": {"parameters": [{"name": "limit", "in": "query"}], "responses": {}}},
    "/v1/orgs/{org}/repos": {"get": {"responses": {}}, "post": {"responses": {}}},
    "/v1/repos": {"get": {"operationId": "repos/list-all", "responses": {}}}
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range doc.Plan() {
		got = append(got, strings.ToUpper(r.Op.Method)+" "+r.Op.Path+" = "+strings.Join(append(r.Resource, r.Method), "."))
	}
	for _, want := range []string{
		"GET /v1/pets = pets.list",
		"POST /v1/pets = pets.create",
		"GET /v1/pets/{id} = pets.get",
		"PUT /v1/pets/{id} = pets.update",
		"POST /v1/pets/{id} = pets.updateWithForm",
		"GET /v1/user/login = user.login",
		"POST /v1/user/createWithList = user.createWithList",
		"GET /v1/user/pets = user.pets.list",
		"GET /v1/orgs/{org}/repos = orgs.repos.get",
		"POST /v1/orgs/{org}/repos = orgs.repos.create",
		"GET /v1/repos = repos.listAll",
	} {
		if !strings.Contains(strings.Join(got, "\n"), want) {
			t.Errorf("plan missing %q\n%s", want, strings.Join(got, "\n"))
		}
	}
}
