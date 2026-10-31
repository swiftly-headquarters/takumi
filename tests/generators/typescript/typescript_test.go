package typescript_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swiftly-headquarters/takumi/generators/typescript"
	"github.com/swiftly-headquarters/takumi/spec"
)

func TestGenerate(t *testing.T) {
	doc, err := spec.Load("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files, err := typescript.Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	types, client := string(files["types.ts"]), string(files["client.ts"])

	for _, want := range []string{
		"export interface NewPet {",
		"  name: string;",
		"  tag?: string | null;",
		`  status?: "available" | "sold";`,
		`  "x-meta"?: Record<string, string>;`,
		"/** Pet name, e.g. *\\/Rex* */",
		"export type Pet = NewPet & { id: number; };",
		"export type PetKind = \"dog\" | \"cat\";",
		"message?: string | null;",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.ts missing %q\n%s", want, types)
		}
	}
	for _, want := range []string{
		`"https://petstore.example.com/v1"`,
		"cookies?: { session?: string; }",
		`init, {"query.filter":"deepObject","query.tags":"pipeDelimited"});`,
		"  create(params: { body: T.NewPet }, init?: RequestInit): Promise<T.Pet>",
		"  get(params: { path: { petId: number; } }, init?: RequestInit): Promise<T.Pet>",
		"  /** @deprecated */\n  delete(params: { path: { petId: number; }; headers?: { \"X-Reason\"?: string; } }",
		"readonly pets = new PetsApi(this.send);",
		"export class PetsApi {",
		"export class PetsPhotoApi {",
		"readonly photo: PetsPhotoApi;",
		"Promise<void>",
		`  update(params: { path: { petId: number; }; body?: { file?: Blob; } }, init?: RequestInit): Promise<string>`,
		`"PUT", "/pets/{petId}/photo", params, "multipart", "multipart/form-data", "text", init, {"path.petId":"matrix"}`,
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.ts missing %q\n%s", want, client)
		}
	}

	// Type-check the output when a TypeScript compiler is around (TAKUMI_TSC=1 to opt in; uses npx).
	if os.Getenv("TAKUMI_TSC") == "" {
		return
	}
	dir := t.TempDir()
	for name, data := range files {
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	cmd := exec.Command("npx", "-y", "-p", "typescript", "tsc", "--noEmit", "--strict", "--target", "es2022",
		"--module", "nodenext", "--lib", "es2022,dom", filepath.Join(dir, "index.ts"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
}

// TestRuntimeStyles checks the URL and Cookie header for every parameter style.
func TestRuntimeStyles(t *testing.T) {
	if os.Getenv("TAKUMI_TSC") == "" {
		t.Skip("set TAKUMI_TSC=1 to run")
	}
	doc, err := spec.Load("testdata/styles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files, err := typescript.Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range files {
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(styleCheck), 0o644)
	cmd := exec.Command("npx", "-y", "-p", "typescript", "tsc", "--strict", "--target", "es2022",
		"--module", "nodenext", "--lib", "es2022,dom", "--outDir", filepath.Join(dir, "out"), filepath.Join(dir, "index.ts"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
	if out, err := exec.Command("node", filepath.Join(dir, "check.mjs")).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

// Expected values follow the style tables of the OpenAPI 3.1 spec.
const styleCheck = `
import { Client } from "./out/index.js";
let got;
const api = new Client({ baseUrl: "http://x", fetch: async (url, init) => {
  got = url.slice("http://x".length) + (init.headers.get("cookie") ? " cookie:" + init.headers.get("cookie") : "");
  return new Response(null, { status: 204 });
} });
const prim = 5, arr = [3, 4, 5], obj = { role: "admin", firstName: "Alex" };
const cases = [
  ["simple", prim, "/simple/5"],
  ["simple", arr, "/simple/3,4,5"],
  ["simple", obj, "/simple/role,admin,firstName,Alex"],
  ["simpleExplode", obj, "/simpleExplode/role=admin,firstName=Alex"],
  ["simpleExplode", arr, "/simpleExplode/3,4,5"],
  ["label", prim, "/label/.5"],
  ["label", arr, "/label/.3,4,5"],
  ["label", obj, "/label/.role,admin,firstName,Alex"],
  ["labelExplode", prim, "/labelExplode/.5"],
  ["labelExplode", arr, "/labelExplode/.3.4.5"],
  ["labelExplode", obj, "/labelExplode/.role=admin.firstName=Alex"],
  ["matrix", prim, "/matrix/;v=5"],
  ["matrix", arr, "/matrix/;v=3,4,5"],
  ["matrix", obj, "/matrix/;v=role,admin,firstName,Alex"],
  ["matrixExplode", prim, "/matrixExplode/;v=5"],
  ["matrixExplode", arr, "/matrixExplode/;v=3;v=4;v=5"],
  ["matrixExplode", obj, "/matrixExplode/;role=admin;firstName=Alex"],
];
const fails = [];
for (const [method, v, want] of cases) {
  await api.path[method]({ path: { v } });
  if (got !== want) fails.push(method + " " + JSON.stringify(v) + ": got " + got + ", want " + want);
}
await api.query.all({
  query: { form: [3, 4], implode: [3, 4], space: [3, 4], pipe: [3, 4], deep: { a: { b: 1 } } },
  cookies: { session: "s 1" },
});
const want = "/query?form=3&form=4&implode=3%2C4&space=3%204&pipe=3%7C4&deep%5Ba%5D%5Bb%5D=1 cookie:session=s%201";
if (got !== want) fails.push("query: got " + got + ", want " + want);
await api.query.all({ query: { form: obj } });
if (got !== "/query?role=admin&firstName=Alex") fails.push("query form object: got " + got);
if (fails.length) {
  console.error(fails.join("\n"));
  process.exit(1);
}
`
