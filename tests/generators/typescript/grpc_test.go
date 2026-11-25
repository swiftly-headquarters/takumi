package typescript_test

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swiftly-headquarters/takumi/generators/typescript"
	"github.com/swiftly-headquarters/takumi/protospec"
	"github.com/swiftly-headquarters/takumi/tests/protohelper"
)

func TestGenerateGRPC(t *testing.T) {
	fd, err := protospec.Load("testdata/shop.proto")
	if err != nil {
		t.Fatal(err)
	}
	files, err := typescript.GenerateGRPC(fd)
	if err != nil {
		t.Fatal(err)
	}
	types, client := string(files["types.ts"]), string(files["client.ts"])
	for _, want := range []string{
		"/** Order is a customer order. */\nexport interface Order {",
		"  totalCents: bigint;",
		"  items: Item[];",
		"  bySku: Record<string, Item>;",
		"  coupon?: string;",
		"  placedAt?: Timestamp;",
		"export const Status = {",
		"export type Status = (typeof Status)[keyof typeof Status];",
		`{ name: "bySku", no: 11, type: "message", repeated: true, map: { key: "string", value: "message", message: () => ItemSchema } }`,
		`{ name: "codes", no: 9, type: "int32", repeated: true, packed: true }`,
		`{ name: "card", no: 16, type: "string", presence: true, oneof: "payment" }`,
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.ts missing %q\n%s", want, types)
		}
	}
	for _, want := range []string{
		"  place(request: DeepPartial<T.Order>, init?: RequestInit): Promise<T.Receipt> {",
		`this.base.unary("/shop.v1.Shop/Place", T.OrderSchema, T.ReceiptSchema, request, init)`,
		"  watch(request: DeepPartial<T.WatchRequest>, init?: RequestInit): AsyncGenerator<T.Receipt> {",
		"// Upload is not available",
		"// Chat is not available",
		"readonly shop = new ShopClient(this);",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.ts missing %q\n%s", want, client)
		}
	}
}

// TestGRPCRuntime type-checks the generated SDK, decodes bytes made by the canonical
// protobuf encoder, and runs unary, streaming and error calls through a fake fetch.
func TestGRPCRuntime(t *testing.T) {
	if os.Getenv("TAKUMI_TSC") == "" {
		t.Skip("set TAKUMI_TSC=1 to run")
	}
	fd, err := protospec.Load("testdata/shop.proto")
	if err != nil {
		t.Fatal(err)
	}
	files, err := typescript.GenerateGRPC(fd)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range files {
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644)
	check := strings.Replace(grpcCheck, "__WIRE__", hex.EncodeToString(protohelper.OrderWire(t, fd)), 1)
	os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(check), 0o644)

	cmd := exec.Command("npx", "-y", "-p", "typescript", "tsc", "--strict", "--target", "es2022",
		"--module", "nodenext", "--lib", "es2022,dom", "--outDir", filepath.Join(dir, "out"), filepath.Join(dir, "index.ts"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(dir, "out", "package.json"), []byte(`{"type":"module"}`), 0o644)
	if out, err := exec.Command("node", filepath.Join(dir, "check.mjs")).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

const grpcCheck = `
import { Client, GrpcError } from "./out/index.js";
import { decode, encode } from "./out/runtime.js";
import { OrderSchema, ReceiptSchema, WatchRequestSchema } from "./out/types.js";

const fails = [];
const show = (v) =>
  JSON.stringify(v, (_, x) => {
    if (typeof x === "bigint") return x + "n";
    if (x && typeof x === "object" && !Array.isArray(x)) return Object.fromEntries(Object.entries(x).sort(([p], [q]) => (p < q ? -1 : 1)));
    return x;
  });
const eq = (name, got, want) => {
  if (show(got) !== show(want)) fails.push(name + ": got " + show(got) + ", want " + show(want));
};

// Bytes from google.golang.org/protobuf decode to the expected values and survive a round trip.
const wire = Uint8Array.from(Buffer.from("__WIRE__", "hex"));
const o = decode(OrderSchema, wire);
eq("id", o.id, "o1");
eq("totalCents", o.totalCents, -5n);
eq("delta", o.delta, -3);
eq("weight", o.weight, 1.5);
eq("ratio", o.ratio, 0.25);
eq("gift", o.gift, true);
eq("note", Array.from(o.note), [1, 2]);
eq("items", o.items, [{ sku: "a", quantity: 2 }]);
eq("codes", o.codes, [1, -1, 300]);
eq("tags", o.tags, ["x", "y"]);
eq("bySku", o.bySku, { k: { sku: "b", quantity: 0 } });
eq("labels", o.labels, { 7: "seven" });
eq("status", o.status, 1);
eq("priority", o.priority, 1);
eq("coupon", o.coupon, "c");
eq("voucher", o.voucher, { sku: "v", quantity: 0 });
eq("card", o.card, undefined);
eq("placedAt", o.placedAt, { seconds: 5n, nanos: 0 });
eq("big", o.big, 1n << 40n);
eq("small", o.small, -9);
eq("huge", o.huge, 1n << 63n);
const again = encode(OrderSchema, o); // oneof members may sit elsewhere than in the reference bytes
eq("re-encoded length", again.length, wire.length);
eq("round trip", decode(OrderSchema, again), o);

// A oneof member replaces the one decoded before it.
eq("oneof last wins", decode(OrderSchema, Uint8Array.from([0x82, 1, 1, 0x61, 0x8a, 1, 3, 0x0a, 1, 0x76])).card, undefined);

// gRPC-web through a fake fetch.
const frame = (flag, bytes) => {
  const b = new Uint8Array(5 + bytes.length);
  b[0] = flag;
  new DataView(b.buffer).setUint32(1, bytes.length);
  b.set(bytes, 5);
  return b;
};
const trailer = (text) => frame(0x80, new TextEncoder().encode(text));
const concat = (parts) => Uint8Array.from(parts.flatMap((p) => Array.from(p)));
const stream = (bytes) =>
  new ReadableStream({
    start(c) {
      for (let i = 0; i < bytes.length; i += 3) c.enqueue(bytes.slice(i, i + 3)); // frames split across reads
      c.close();
    },
  });

let seen;
const api = new Client({
  baseUrl: "http://x/",
  headers: { authorization: "Bearer t" },
  fetch: async (url, init) => {
    seen = { url, headers: init.headers, request: init.body };
    const req = init.body.slice(5);
    if (url.endsWith("/Watch")) {
      const id = decode(WatchRequestSchema, req).id;
      const body = concat([
        frame(0, encode(ReceiptSchema, { id, status: 1 })),
        frame(0, encode(ReceiptSchema, { id, status: 2 })),
        trailer("grpc-status: 0\r\n"),
      ]);
      return new Response(stream(body), { status: 200 });
    }
    if (decode(OrderSchema, req).id === "bad") {
      return new Response(stream(trailer("grpc-status: 5\r\ngrpc-message: no%20such%20order\r\n")), { status: 200 });
    }
    return new Response(stream(concat([frame(0, encode(ReceiptSchema, { id: "r1", status: 1 })), trailer("grpc-status: 0\r\n")])), { status: 200 });
  },
});

const r = await api.shop.place({ id: "o1", items: [{ sku: "a" }] });
eq("unary", r, { id: "r1", status: 1 });
eq("url", seen.url, "http://x/shop.v1.Shop/Place");
eq("content-type", seen.headers.get("content-type"), "application/grpc-web+proto");
eq("auth", seen.headers.get("authorization"), "Bearer t");
eq("request", decode(OrderSchema, seen.request.slice(5)).items, [{ sku: "a", quantity: 0 }]);

const got = [];
for await (const m of api.shop.watch({ id: "w" })) got.push(m);
eq("stream", got, [{ id: "w", status: 1 }, { id: "w", status: 2 }]);

try {
  await api.shop.place({ id: "bad" });
  fails.push("error: no GrpcError thrown");
} catch (e) {
  if (!(e instanceof GrpcError)) fails.push("error: not a GrpcError: " + e);
  else {
    eq("error code", e.code, 5);
    eq("error message", e.message, "no such order");
  }
}
eq("client streaming skipped", typeof api.shop.upload, "undefined");

if (fails.length) {
  console.error(fails.join("\n"));
  process.exit(1);
}
`
