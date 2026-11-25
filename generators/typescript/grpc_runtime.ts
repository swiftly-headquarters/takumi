export type FieldType =
  | "double" | "float"
  | "int32" | "uint32" | "sint32" | "fixed32" | "sfixed32"
  | "int64" | "uint64" | "sint64" | "fixed64" | "sfixed64"
  | "bool" | "string" | "bytes" | "enum" | "message";

/** Describes one field of a message; the generated `*Schema` constants are made of these. */
export interface FieldSchema {
  name: string;
  no: number;
  type: FieldType;
  repeated?: boolean;
  packed?: boolean;
  /** Encoded even when it holds the default value (optional, oneof and message fields). */
  presence?: boolean;
  oneof?: string;
  message?: () => MessageSchema;
  map?: { key: FieldType; value: FieldType; message?: () => MessageSchema };
}

export interface MessageSchema {
  fields: FieldSchema[];
}

export type DeepPartial<T> = T extends Uint8Array | bigint | string | number | boolean
  ? T
  : T extends (infer U)[]
    ? DeepPartial<U>[]
    : { [K in keyof T]?: DeepPartial<T[K]> };

export interface ClientOptions {
  /** Base URL of a gRPC-web endpoint, e.g. an Envoy proxy or a server with gRPC-web enabled. */
  baseUrl?: string;
  /** Sent with every call. Use a function for tokens that change. */
  headers?: Record<string, string> | (() => Record<string, string> | Promise<Record<string, string>>);
  /** Custom fetch, e.g. for tests or older runtimes. */
  fetch?: typeof fetch;
}

/** A call that finished with a non-OK gRPC status. `code` is the numeric status (5 = NOT_FOUND). */
export class GrpcError extends Error {
  constructor(
    readonly code: number,
    message: string,
  ) {
    super(message || `gRPC status ${code}`);
    this.name = "GrpcError";
  }
}

// ---- protobuf wire format ----

const WIRE: Record<FieldType, number> = {
  int32: 0, uint32: 0, sint32: 0, int64: 0, uint64: 0, sint64: 0, bool: 0, enum: 0,
  fixed64: 1, sfixed64: 1, double: 1,
  string: 2, bytes: 2, message: 2,
  fixed32: 5, sfixed32: 5, float: 5,
};
const BIG = new Set<FieldType>(["int64", "uint64", "sint64", "fixed64", "sfixed64"]);
const utf8 = new TextEncoder();

function defaultOf(type: FieldType): unknown {
  if (type === "string") return "";
  if (type === "bytes") return new Uint8Array();
  if (type === "bool") return false;
  if (type === "message") return undefined;
  return BIG.has(type) ? 0n : 0;
}

function isDefault(type: FieldType, x: unknown): boolean {
  if (type === "bytes") return (x as Uint8Array).length === 0;
  return x === defaultOf(type);
}

function varint(out: number[], v: bigint) {
  v = BigInt.asUintN(64, v);
  while (v > 0x7fn) {
    out.push(Number(v & 0x7fn) | 0x80);
    v >>= 7n;
  }
  out.push(Number(v));
}

function fixed(out: number[], size: number, set: (view: DataView) => void) {
  const view = new DataView(new ArrayBuffer(size));
  set(view);
  out.push(...new Uint8Array(view.buffer));
}

function writeValue(out: number[], type: FieldType, x: unknown, schema?: () => MessageSchema) {
  switch (type) {
    case "int32": case "int64": case "uint32": case "uint64": case "enum":
      return varint(out, BigInt(x as number | bigint));
    case "sint32": {
      const n = BigInt(x as number | bigint);
      return varint(out, BigInt.asUintN(32, (n << 1n) ^ (n >> 31n)));
    }
    case "sint64": {
      const n = BigInt(x as number | bigint);
      return varint(out, (n << 1n) ^ (n >> 63n));
    }
    case "bool": return out.push(x ? 1 : 0);
    case "fixed32": return fixed(out, 4, (v) => v.setUint32(0, Number(x), true));
    case "sfixed32": return fixed(out, 4, (v) => v.setInt32(0, Number(x), true));
    case "float": return fixed(out, 4, (v) => v.setFloat32(0, Number(x), true));
    case "fixed64": return fixed(out, 8, (v) => v.setBigUint64(0, BigInt.asUintN(64, BigInt(x as number | bigint)), true));
    case "sfixed64": return fixed(out, 8, (v) => v.setBigInt64(0, BigInt.asIntN(64, BigInt(x as number | bigint)), true));
    case "double": return fixed(out, 8, (v) => v.setFloat64(0, Number(x), true));
    case "string": case "bytes": case "message": {
      const bytes = type === "string" ? utf8.encode(x as string) : type === "bytes" ? (x as Uint8Array) : encode(schema!(), x as object);
      varint(out, BigInt(bytes.length));
      for (const b of bytes) out.push(b);
    }
  }
}

function writeField(out: number[], no: number, type: FieldType, x: unknown, schema?: () => MessageSchema) {
  varint(out, BigInt((no << 3) | WIRE[type]));
  writeValue(out, type, x, schema);
}

function fromKey(type: FieldType, k: string): unknown {
  if (type === "string") return k;
  if (type === "bool") return k === "true";
  return BIG.has(type) ? BigInt(k) : Number(k);
}

/** Encodes a message value to protobuf bytes. Missing fields are left out. */
export function encode(schema: MessageSchema, value: object): Uint8Array {
  const out: number[] = [];
  for (const f of schema.fields) {
    const x = (value as Record<string, unknown>)[f.name];
    if (x === undefined || x === null) continue;
    if (f.map) {
      for (const [k, v] of Object.entries(x as object)) {
        const entry: number[] = [];
        writeField(entry, 1, f.map.key, fromKey(f.map.key, k));
        writeField(entry, 2, f.map.value, v, f.map.message);
        varint(out, BigInt((f.no << 3) | 2));
        varint(out, BigInt(entry.length));
        entry.forEach((b) => out.push(b));
      }
    } else if (f.repeated) {
      const items = x as unknown[];
      if (f.packed) {
        if (!items.length) continue;
        const body: number[] = [];
        for (const i of items) writeValue(body, f.type, i);
        varint(out, BigInt((f.no << 3) | 2));
        varint(out, BigInt(body.length));
        body.forEach((b) => out.push(b));
      } else {
        for (const i of items) writeField(out, f.no, f.type, i, f.message);
      }
    } else if (f.presence || !isDefault(f.type, x)) {
      writeField(out, f.no, f.type, x, f.message);
    }
  }
  return Uint8Array.from(out);
}

class Reader {
  pos = 0;
  constructor(readonly buf: Uint8Array) {}
  get done() { return this.pos >= this.buf.length; }
  varint(): bigint {
    let v = 0n;
    for (let shift = 0n; shift < 70n; shift += 7n) {
      if (this.pos >= this.buf.length) throw new GrpcError(13, "truncated protobuf message");
      const b = this.buf[this.pos++];
      v |= BigInt(b & 0x7f) << shift;
      if (!(b & 0x80)) return v;
    }
    throw new GrpcError(13, "malformed varint");
  }
  bytes(n: number): Uint8Array {
    if (this.pos + n > this.buf.length) throw new GrpcError(13, "truncated protobuf message");
    return this.buf.slice(this.pos, (this.pos += n));
  }
  view(n: number) {
    const b = this.bytes(n);
    return new DataView(b.buffer, b.byteOffset, n);
  }
  skip(wire: number) {
    if (wire === 0) this.varint();
    else if (wire === 1) this.bytes(8);
    else if (wire === 2) this.bytes(Number(this.varint()));
    else if (wire === 5) this.bytes(4);
    else throw new GrpcError(13, `unsupported protobuf wire type ${wire}`);
  }
}

function readValue(r: Reader, type: FieldType, schema?: () => MessageSchema): unknown {
  switch (type) {
    case "int32": case "enum": return Number(BigInt.asIntN(32, r.varint()));
    case "uint32": return Number(BigInt.asUintN(32, r.varint()));
    case "int64": return BigInt.asIntN(64, r.varint());
    case "uint64": return BigInt.asUintN(64, r.varint());
    case "sint32": { const v = BigInt.asUintN(32, r.varint()); return Number((v >> 1n) ^ -(v & 1n)); }
    case "sint64": { const v = BigInt.asUintN(64, r.varint()); return (v >> 1n) ^ -(v & 1n); }
    case "bool": return r.varint() !== 0n;
    case "fixed32": return r.view(4).getUint32(0, true);
    case "sfixed32": return r.view(4).getInt32(0, true);
    case "float": return r.view(4).getFloat32(0, true);
    case "fixed64": return r.view(8).getBigUint64(0, true);
    case "sfixed64": return r.view(8).getBigInt64(0, true);
    case "double": return r.view(8).getFloat64(0, true);
    case "string": return new TextDecoder().decode(r.bytes(Number(r.varint())));
    case "bytes": return r.bytes(Number(r.varint()));
    case "message": return decode(schema!(), r.bytes(Number(r.varint())));
  }
}

const indexes = new WeakMap<MessageSchema, Map<number, FieldSchema>>();

/** Decodes protobuf bytes. Fields without presence get their default value, unknown fields are skipped. */
export function decode<T>(schema: MessageSchema, buf: Uint8Array): T {
  let byNumber = indexes.get(schema);
  if (!byNumber) indexes.set(schema, (byNumber = new Map(schema.fields.map((f) => [f.no, f]))));
  const msg: Record<string, unknown> = {};
  for (const f of schema.fields) {
    if (f.map) msg[f.name] = {};
    else if (f.repeated) msg[f.name] = [];
    else if (!f.presence) msg[f.name] = defaultOf(f.type);
  }
  const r = new Reader(buf);
  while (!r.done) {
    const key = Number(r.varint());
    const f = byNumber.get(key >>> 3);
    const wire = key & 7;
    if (!f) {
      r.skip(wire);
    } else if (f.map) {
      const entry = new Reader(r.bytes(Number(r.varint())));
      let k = defaultOf(f.map.key);
      let v = defaultOf(f.map.value);
      while (!entry.done) {
        const ek = Number(entry.varint());
        if (ek >>> 3 === 1) k = readValue(entry, f.map.key);
        else if (ek >>> 3 === 2) v = readValue(entry, f.map.value, f.map.message);
        else entry.skip(ek & 7);
      }
      if (v === undefined) v = decode(f.map.message!(), new Uint8Array());
      (msg[f.name] as Record<string, unknown>)[String(k)] = v;
    } else if (f.repeated) {
      const list = msg[f.name] as unknown[];
      if (wire === 2 && WIRE[f.type] !== 2) {
        const packed = new Reader(r.bytes(Number(r.varint())));
        while (!packed.done) list.push(readValue(packed, f.type));
      } else {
        list.push(readValue(r, f.type, f.message));
      }
    } else {
      msg[f.name] = readValue(r, f.type, f.message);
      if (f.oneof) {
        for (const other of schema.fields) if (other.oneof === f.oneof && other !== f) delete msg[other.name];
      }
    }
  }
  return msg as T;
}

// ---- gRPC-web transport ----

const HTTP_TO_GRPC: Record<number, number> = { 400: 13, 401: 16, 403: 7, 404: 12, 429: 14, 502: 14, 503: 14, 504: 14 };

async function* frames(res: Response): AsyncGenerator<[boolean, Uint8Array]> {
  const reader = res.body?.getReader();
  if (!reader) return;
  let buf = new Uint8Array(0);
  for (;;) {
    const { done, value } = await reader.read();
    if (value) {
      const next = new Uint8Array(buf.length + value.length);
      next.set(buf);
      next.set(value, buf.length);
      buf = next;
    }
    while (buf.length >= 5) {
      if (buf[0] & 1) throw new GrpcError(12, "compressed gRPC-web messages are not supported");
      const len = new DataView(buf.buffer, buf.byteOffset).getUint32(1);
      if (buf.length < 5 + len) break;
      yield [(buf[0] & 0x80) !== 0, buf.slice(5, 5 + len)];
      buf = buf.subarray(5 + len);
    }
    if (done) break;
  }
  if (buf.length) throw new GrpcError(13, "truncated gRPC-web frame");
}

export class BaseClient {
  constructor(protected readonly options: ClientOptions = {}) {}

  /** @internal Sends one request message and resolves with the single response message. */
  async unary<Res>(path: string, req: MessageSchema, res: MessageSchema, request: object, init?: RequestInit): Promise<Res> {
    let out: Res | undefined;
    for await (const m of this.stream<Res>(path, req, res, request, init)) out = m;
    if (out === undefined) throw new GrpcError(13, "the server sent no response message");
    return out;
  }

  /** @internal Sends one request message and yields every response message. Throws GrpcError on a non-OK status. */
  async *stream<Res>(path: string, req: MessageSchema, res: MessageSchema, request: object, init?: RequestInit): AsyncGenerator<Res> {
    const headers = new Headers();
    const defaults = typeof this.options.headers === "function" ? await this.options.headers() : this.options.headers;
    for (const source of [defaults, init?.headers]) {
      new Headers((source ?? {}) as HeadersInit).forEach((value, name) => headers.set(name, value));
    }
    headers.set("content-type", "application/grpc-web+proto");
    headers.set("accept", "application/grpc-web+proto");
    headers.set("x-grpc-web", "1");

    const message = encode(req, request);
    const body = new Uint8Array(5 + message.length);
    new DataView(body.buffer).setUint32(1, message.length);
    body.set(message, 5);

    const base = (this.options.baseUrl ?? "").replace(/\/+$/, ""); // empty: same origin as the page
    const response = await (this.options.fetch ?? fetch)(base + path, { ...init, method: "POST", headers, body });
    let status = response.headers.get("grpc-status");
    let detail = response.headers.get("grpc-message");
    if (!response.ok) {
      throw new GrpcError(status ? Number(status) : (HTTP_TO_GRPC[response.status] ?? 2), response.statusText || `HTTP ${response.status}`);
    }
    for await (const [trailer, data] of frames(response)) {
      if (!trailer) {
        yield decode<Res>(res, data);
        continue;
      }
      const t = new Headers();
      for (const line of new TextDecoder().decode(data).split("\r\n")) {
        const i = line.indexOf(":");
        if (i > 0) t.append(line.slice(0, i).trim(), line.slice(i + 1).trim());
      }
      status = t.get("grpc-status") ?? status;
      detail = t.get("grpc-message") ?? detail;
    }
    if (status !== "0") throw new GrpcError(status === null ? 2 : Number(status), decodeURIComponent(detail ?? ""));
  }
}
