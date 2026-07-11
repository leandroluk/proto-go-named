# proto-go-named

`protoc-gen-go` always turns `user_id` into `UserId`. Go convention wants
`UserID`. This wraps `protoc-gen-go` and lets you override the generated Go
identifier per field, without touching wire format, JSON, or reflection.

```proto
import "golang/options.proto";

message User {
  string user_id    = 1 [(golang.field_name) = "UserID"];
  string tenant_url = 2 [(golang.field_name) = "TenantURL"];
}
```

```go
type User struct {
	UserID    string `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" ...`
	TenantURL string `protobuf:"bytes,2,opt,name=tenant_url,json=tenantUrl,proto3" ...`
}

func (x *User) GetUserID() string    { ... }
func (x *User) GetTenantURL() string { ... }
```

The `protobuf` tag, `json` name, and `.proto` field name are untouched —
only the Go identifier changes.

## How it works

`protoc-gen-go-named` is not a fork. On each run it:

1. Reads the `CodeGeneratorRequest` from stdin, same as any protoc plugin.
2. Uses `google.golang.org/protobuf/compiler/protogen` — the same library
   `protoc-gen-go` itself uses — to compute each field's *default* Go name
   and check whether it carries the `(golang.field_name)` extension.
3. Execs the real `protoc-gen-go` binary with the unmodified request and
   captures its `CodeGeneratorResponse`.
4. For every field that requested an override, rewrites the generated
   source with `go/ast`: renames the struct field, its `Get<Field>()`
   accessor, and internal references — scoped per struct, so two messages
   reusing the same field name never collide.
5. Emits the patched response.

No copy of `protoc-gen-go`'s internal generator code, no reimplementation
of the marshaling logic — it delegates all of that and only renames
identifiers afterward.

## Install

```sh
go install github.com/leandroluk/proto-go-named/cmd/protoc-gen-go-named@latest
```

Requires `protoc-gen-go` on `PATH` too (the real generator this wraps).
Override which binary gets exec'd with `PROTOC_GEN_GO_NAMED_REAL` if you
need to pin a specific one.

## Use in your proto module

1. Import the extension. Either vendor [`proto/golang/options.proto`](proto/golang/options.proto)
   into your own module, or add this repo as a `buf` dependency once it's
   published to the BSR.

2. Swap the Go plugin in your `buf.gen.yaml`:

   ```diff
    plugins:
   -  - remote: buf.build/protocolbuffers/go
   +  - local: protoc-gen-go-named
      out: .
      opt: paths=source_relative
   ```

3. `buf generate` as usual.

## Limitations

- Only renames what `protoc-gen-go`'s default (non-opaque) API generates:
  the struct field and its getter. Setters, `oneof` wrapper types, and the
  opaque API are not covered yet.
- Matching generated files to proto files assumes `paths=source_relative`
  (the common case, and what this repo's own `buf.gen.yaml` uses).
- This is a local (`local:`) plugin, not a BSR remote plugin — everyone
  running `buf generate` needs the binary installed.

## Development

```sh
cd proto && buf generate   # regenerate proto/golang and proto/testdata
go test ./...              # smoke test: wire + JSON stay standard-compliant
```

## CI

- `.github/workflows/go.yml` — build, vet, test the Go module.
- `.github/workflows/buf.yml` — lint/breaking-change check on PRs, `buf push`
  to the BSR on merge to `master`. Requires a `BUF_TOKEN` repo secret ([create
  one](https://buf.build/docs/bsr/authentication/#create-an-api-token)).
