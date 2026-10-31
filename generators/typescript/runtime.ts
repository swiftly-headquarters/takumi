export interface ClientOptions {
  /** Defaults to the first `servers` entry of the spec. */
  baseUrl?: string;
  /** Sent with every request. Use a function for tokens that change. */
  headers?: Record<string, string> | (() => Record<string, string> | Promise<Record<string, string>>);
  /** Custom fetch, e.g. for tests or older runtimes. */
  fetch?: typeof fetch;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly body: unknown,
    readonly response: Response,
  ) {
    super(`HTTP ${status} ${response.statusText}`.trim());
    this.name = "ApiError";
  }
}

type RequestParams = {
  path?: Record<string, unknown>;
  query?: Record<string, unknown>;
  headers?: Record<string, unknown>;
  cookies?: Record<string, unknown>;
  body?: unknown;
};

export type Send = <R>(
  method: string,
  path: string,
  params: RequestParams,
  bodyKind: string,
  contentType: string,
  parse: string,
  init?: RequestInit,
  styles?: Record<string, string>,
) => Promise<R>;

const scalar = (v: unknown) => (typeof v === "object" ? JSON.stringify(v) : String(v));
const present = (v: unknown) => v !== undefined && v !== null;
const SEPARATORS: Record<string, string> = { "form:implode": ",", spaceDelimited: " ", pipeDelimited: "|" };

function deepObjectPairs(name: string, value: unknown): [string, string][] {
  if (!present(value)) return [];
  if (Array.isArray(value)) return [[name, value.map(scalar).join(",")]];
  if (typeof value !== "object") return [[name, String(value)]];
  return Object.entries(value).flatMap(([k, v]) => deepObjectPairs(`${name}[${k}]`, v));
}

function arrayPairs(name: string, items: unknown[], separator?: string): [string, string][] {
  const values = items.filter(present).map(scalar);
  return separator ? [[name, values.join(separator)]] : values.map((v) => [name, v]);
}

function objectPairs(name: string, object: object, separator?: string): [string, string][] {
  const entries = Object.entries(object).filter(([, v]) => present(v));
  if (separator) return [[name, entries.flatMap(([k, v]) => [k, scalar(v)]).join(separator)]];
  return entries.map(([k, v]) => [k, scalar(v)]);
}

function pairs(kind: string, values: Record<string, unknown> | undefined, styles: Record<string, string>): [string, string][] {
  return Object.entries(values ?? {}).flatMap(([name, value]): [string, string][] => {
    if (!present(value)) return [];
    const style = styles[`${kind}.${name}`];
    const separator = SEPARATORS[style];
    if (style === "deepObject") return deepObjectPairs(name, value);
    if (Array.isArray(value)) return arrayPairs(name, value, separator);
    if (typeof value === "object") return objectPairs(name, value as object, separator);
    return [[name, String(value)]];
  });
}

function pathValue(name: string, value: unknown, style = "simple"): string {
  const [kind, explode] = style.split(":");
  const enc = (v: unknown) => encodeURIComponent(scalar(v));
  const isObject = typeof value === "object" && value !== null && !Array.isArray(value);
  const items: string[][] = Array.isArray(value)
    ? value.map((v) => [enc(v)])
    : isObject
      ? Object.entries(value as object).map(([k, v]) => [encodeURIComponent(k), enc(v)])
      : [[enc(value)]];
  if (kind === "matrix") {
    if (!explode) return `;${name}=` + items.flat().join(",");
    return items.map((i) => (isObject ? `;${i[0]}=${i[1]}` : `;${name}=${i[0]}`)).join("");
  }
  const prefix = kind === "label" ? "." : "";
  return prefix + items.map((i) => i.join(explode ? "=" : ",")).join(explode && prefix ? prefix : ",");
}

function encodeBody(kind: string, value: unknown, contentType: string, headers: Headers): BodyInit {
  const fields = () => Object.entries((value ?? {}) as object).filter(([, v]) => present(v));
  switch (kind) {
    case "json":
      headers.set("content-type", contentType);
      return JSON.stringify(value);
    case "form":
      return new URLSearchParams(fields().map(([k, v]) => [k, String(v)]));
    case "multipart": {
      const form = new FormData();
      for (const [k, v] of fields()) form.append(k, v instanceof Blob ? v : scalar(v));
      return form; // fetch sets the multipart boundary itself
    }
    default:
      headers.set("content-type", contentType);
      return value as BodyInit;
  }
}

/** Browsers forbid the Cookie header, so there cookies are stored and fetch attaches them. */
function applyCookies(headers: Headers, cookies: [string, string][], credentials?: RequestCredentials) {
  if (!cookies.length) return credentials;
  const encoded = cookies.map(([k, v]) => `${k}=${encodeURIComponent(v)}`);
  const doc = (globalThis as { document?: { cookie: string } }).document;
  if (!doc) {
    headers.set("cookie", [headers.get("cookie"), ...encoded].filter(Boolean).join("; "));
    return credentials;
  }
  for (const c of encoded) doc.cookie = `${c}; path=/; SameSite=Lax`;
  return credentials ?? "include";
}

async function parseResponse<R>(res: Response, parse: string): Promise<R> {
  if (parse === "void" || res.status === 204) return undefined as R;
  if (parse === "text") return (await res.text()) as R;
  if (parse === "blob") return (await res.blob()) as R;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as R;
}

async function errorFrom(res: Response): Promise<ApiError> {
  const text = await res.text();
  let body: unknown = text;
  try {
    body = JSON.parse(text);
  } catch {}
  return new ApiError(res.status, body, res);
}

export class BaseClient {
  constructor(protected readonly options: ClientOptions = {}) {}

  protected readonly send: Send = (method, path, params, bodyKind, contentType, parse, init, styles) =>
    this.request(method, path, params, bodyKind, contentType, parse, init, styles);

  protected async request<R>(
    method: string,
    path: string,
    params: RequestParams,
    bodyKind: string,
    contentType: string,
    parse: string,
    init?: RequestInit,
    styles: Record<string, string> = {},
  ): Promise<R> {
    const base = (this.options.baseUrl ?? "__BASE_URL__").replace(/\/+$/, "");
    let url = base + path.replace(/\{([^}]+)\}/g, (_, name: string) => pathValue(name, params.path?.[name], styles[`path.${name}`]));

    const query = pairs("query", params.query, styles).map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`);
    if (query.length) url += "?" + query.join("&");

    const headers = new Headers();
    const defaults = typeof this.options.headers === "function" ? await this.options.headers() : this.options.headers;
    for (const source of [defaults, init?.headers]) {
      new Headers((source ?? {}) as HeadersInit).forEach((value, name) => headers.set(name, value));
    }
    for (const [name, value] of Object.entries(params.headers ?? {})) {
      if (present(value)) headers.set(name, String(value));
    }

    const credentials = applyCookies(headers, pairs("cookie", params.cookies, styles), init?.credentials);
    const body = params.body === undefined ? undefined : encodeBody(bodyKind, params.body, contentType, headers);

    const doFetch = this.options.fetch ?? fetch;
    const res = await doFetch(url, { ...init, method, headers, body, credentials });
    if (!res.ok) throw await errorFrom(res);
    return parseResponse<R>(res, parse);
  }
}
