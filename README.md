# Takumi

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Build Status](https://img.shields.io/github/actions/workflow/status/swiftly-headquarters/takumi/build.yml?branch=main)](https://github.com/swiftly-headquarters/takumi/actions)
[![Release](https://img.shields.io/github/v/release/swiftly-headquarters/takumi?include_prereleases)](https://github.com/swiftly-headquarters/takumi/releases)

Generate typed API SDKs from OpenAPI 3.x and Swagger 2.0 specs. Point it at an `openapi.json` / `openapi.yaml` (a file or a URL) and get a ready-to-use client.

Built in Go, ships as a single static binary. Generated SDKs have zero runtime dependencies.

## Supported languages

| `-lang`      | Output                                                          |
| ------------ | --------------------------------------------------------------- |
| `typescript` | `fetch`-based client, works in browsers, Node 18+, Deno and Bun |

More languages are on the way.

## Install

Grab an archive from the [latest release](https://github.com/swiftly-headquarters/takumi/releases/latest):

| Archive                                                                                                                 | Use when            |
| ----------------------------------------------------------------------------------------------------------------------- | ------------------- |
| [`takumi-win-x64.zip`](https://github.com/swiftly-headquarters/takumi/releases/latest/download/takumi-win-x64.zip)       | Windows             |
| [`takumi-linux-x64.zip`](https://github.com/swiftly-headquarters/takumi/releases/latest/download/takumi-linux-x64.zip)   | Linux               |
| [`takumi-linux-arm64.zip`](https://github.com/swiftly-headquarters/takumi/releases/latest/download/takumi-linux-arm64.zip) | Linux on ARM        |
| [`takumi-osx-arm64.zip`](https://github.com/swiftly-headquarters/takumi/releases/latest/download/takumi-osx-arm64.zip)   | macOS Apple Silicon |

Those links always resolve to the newest stable release. On Linux and macOS, `chmod +x takumi` after unzipping.

Or with Go installed:

```bash
go install github.com/swiftly-headquarters/takumi@latest
```

## Layout

| Package                  | Purpose                                                                                   |
| ------------------------ | ----------------------------------------------------------------------------------------- |
| `spec`                   | Loads OpenAPI 3.x or Swagger 2.0 (JSON or YAML, file or URL), bundles multi-file specs, resolves `$ref`s, flattens paths into operations, deduces `resource.method` names. |
| `generators/typescript`  | TypeScript generator. Type mapping in `typescript.go`, the shipped HTTP runtime in `runtime.ts`. |
| `main.go`                | CLI entry point and the `-lang` → generator registry.                                     |

## Quick start

Generate a TypeScript SDK into `./sdk`:

```bash
./takumi -input openapi.yaml -output ./sdk
```

Pick the language explicitly:

```bash
./takumi -input openapi.json -output ./src/api -lang typescript
./takumi -input https://petstore.swagger.io/v2/swagger.json -output ./sdk
```

Specs split over several files work as is: `$ref: schemas/pet.yaml` or `$ref: https://example.com/common.yaml#/components/parameters/Limit` are resolved relative to the file that contains them. Swagger 2.0 documents are converted to OpenAPI 3.0 on load.

Show all available flags:

```bash
./takumi -input openapi.yaml -plan   # print the resource.method name of every operation
./takumi -help
```

Re-running overwrites the generated files in place. Files you add to the output directory are left alone.

## TypeScript output

```
sdk/
  types.ts    one interface / type alias per components.schemas entry
  client.ts   Client class, one method per operation, plus the fetch runtime
  index.ts    re-exports both
```

Use it:

```ts
import { Client, ApiError } from "./sdk/index.js";

const api = new Client({
  baseUrl: "https://api.example.com", // defaults to the spec's first server
  headers: async () => ({ authorization: `Bearer ${await getToken()}` }),
});

const pets = await api.pets.list({ query: { limit: 10 } });
const pet = await api.pets.create({ body: { name: "Rex" } });

try {
  await api.pets.get({ path: { petId: 42 } });
} catch (e) {
  if (e instanceof ApiError) console.log(e.status, e.body);
}
```

Every method takes one object with `path`, `query`, `headers`, `cookies` and `body` keys (whichever the operation uses), plus an optional `RequestInit` for things like `signal`.

Parameters honour `style` and `explode`. Path: `simple` (default), `label` (`.5`) and `matrix` (`;id=5`). Query and cookie: `form` (default, exploded or comma-joined), `spaceDelimited`, `pipeDelimited` and `deepObject` (`filter[a][b]=1`).

### Method names

The URL path sets the namespace: `GET /user/pets` is `api.user.pets.list()`. Run `takumi -input openapi.yaml -plan` to preview names.

Name sources, first match wins:

1. Dotted or slashed `operationId` (`repos/list-for-org`) gives resource and method.
2. HTTP method and sibling paths give the verb:

| Operation                                                  | Method    |
| ---------------------------------------------------------- | --------- |
| `GET /pets` (list shape or `/pets/{id}` exists)            | `list`    |
| other `GET`                                                | `get`     |
| `POST` on a collection                                     | `create`  |
| `POST /pets/{id}`, `PATCH`, `PUT`                          | `update`  |
| `PUT` beside a `PATCH`                                     | `replace` |
| `DELETE`, `HEAD`, `OPTIONS`                                | same name |
| lone `POST`/`GET` action (`/user/login`)                   | last segment, `user.login()` |

3. Taken or ambiguous: `operationId` minus the resource noun (`updatePetWithForm` under `pet` gives `updateWithForm`), then a numeric suffix.

How schemas map:

| OpenAPI                                  | TypeScript                               |
| ---------------------------------------- | ---------------------------------------- |
| `object` with `properties`               | `interface` (missing from `required` = optional) |
| `additionalProperties`                   | `Record<string, T>`                      |
| `enum` / `const`                         | literal union                            |
| `oneOf` / `anyOf`                        | union                                    |
| `allOf`                                  | intersection                             |
| `nullable: true` / `type: [T, "null"]`   | `T \| null`                              |
| `string` + `format: binary`              | `Blob`                                   |
| `integer` / `number`                     | `number`                                 |

Request bodies are sent as JSON, `multipart/form-data` or `application/x-www-form-urlencoded`, preferring JSON when a spec offers several. Non-2xx responses throw `ApiError` with the parsed body.

## Limitations

- Browsers forbid setting the `Cookie` header, so in a browser cookie parameters are written to `document.cookie` and the request is sent with `credentials: "include"`. The API only receives them when it is on the same site as the page. Node, Deno and Bun send the `Cookie` header directly.

## Building from source

Requires **Go 1.27+**.

```bash
git clone https://github.com/swiftly-headquarters/takumi.git
cd takumi
go build -o takumi .
```

Run the tests. Set `TAKUMI_TSC=1` to also type-check the generated TypeScript through `npx tsc` and run its runtime under Node, checking every parameter style:

```bash
go test ./...
TAKUMI_TSC=1 go test ./...
```

## Releases & branches

| Branch | Publishes      | Tag      |
| ------ | -------------- | -------- |
| `main` | Stable release | `vX.Y.Z` |

Every push to `main` runs the tests, builds all four archives and publishes a release. Versions come from [GitVersion](GitVersion.yml): add `+semver: feature` or `+semver: breaking` to a commit message to bump minor or major.

## Architecture

```
takumi/
├── spec/
│   ├── spec.go                  # OpenAPI model, JSON/YAML loading, $ref resolution, Operations()
│   ├── bundle.go                # Inlines external $refs (other files, URLs)
│   ├── swagger.go               # Swagger 2.0 -> OpenAPI 3.0 conversion
│   └── plan.go                  # Plan(): deduces resource.method names from paths and operationIds
├── generators/
│   └── typescript/
│       ├── typescript.go        # Schema -> TS types, operations -> Client methods
│       └── runtime.ts           # Embedded fetch runtime (BaseClient, ApiError)
├── tests/
│   ├── spec/                    # Plan(), loading, bundling and Swagger 2.0 tests
│   └── generators/
│       └── typescript/
│           ├── typescript_test.go
│           └── testdata/        # Fixture specs
├── main.go                      # CLI entry point + language registry
└── GitVersion.yml               # Versioning rules
```

## Community

- **Issues**: [Report bugs and request features](https://github.com/swiftly-headquarters/takumi/issues)
- **Security**: [Report privately](https://github.com/swiftly-headquarters/takumi/security/advisories/new) - never in a public issue

## License

GPL-3.0. See [LICENSE](LICENSE).
