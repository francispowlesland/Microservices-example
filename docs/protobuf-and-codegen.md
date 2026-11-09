# Protobuf and code generation

The `.proto` files in `proto/` are the contract between the gateway and the services. Go and TypeScript code is generated from them and committed, so a fresh checkout builds without needing `buf`.

## Layout

```
proto/
  buf.yaml                  module + lint/breaking rules
  buf.gen.yaml              plugins and output directories
  user/v1/user.proto        package user.v1       UserService, AuthService
  orders/v1/orders.proto    package orders.v1     OrdersService
  payments/v1/payments.proto package payments.v1  PaymentsService
  inventory/v1/inventory.proto package inventory.v1 InventoryService

gen/go/<name>/v1/*.pb.go, *_grpc.pb.go        generated Go (package userv1, ordersv1, ...)
services/gateway/src/lib/gen/<name>/v1/*_pb.ts generated TypeScript
```

One file per API, `<name>/v1/` directories, and the proto package matches the directory. Note that `user` is singular in the proto but the service lives in `services/users`.

## Configuration

`proto/buf.yaml` (buf v2 format):

- One module rooted at `proto/`.
- Lint rule set: `STANDARD`.
- Breaking-change rule set: `FILE` (the strictest per-file category, which flags wire and source-level breaks).

`proto/buf.gen.yaml`:

- **Managed mode** is on, with `go_package_prefix` set to `github.com/francispowlesland/microservices-app-go/gen/go`. This is why the `.proto` files contain no `option go_package`: buf adds it at generation time.
- Three plugins, all remote (`buf.build/...`), so generating needs network access to the Buf Schema Registry:
  - `buf.build/protocolbuffers/go` to `../gen/go`, `paths=source_relative`
  - `buf.build/grpc/go` to `../gen/go`, `paths=source_relative`
  - `buf.build/bufbuild/es` to `../services/gateway/src/lib/gen`, `target=ts`

Output paths are relative to `proto/`, so run generation from that directory.

## Regenerating

With [buf](https://buf.build/docs/installation) installed:

```sh
cd proto
buf lint
buf generate
```

`buf generate` picks up `buf.gen.yaml` from the current directory and writes into `gen/go` and `services/gateway/src/lib/gen`. Commit the results with the `.proto` change.

I ran this against the current protos into a scratch directory and the output was byte-identical to what's committed, so the checked-in code is in sync with the schemas at the time of writing. A cheap CI check would be to run `buf generate` and fail on `git diff`; there isn't one in the repo yet.

Generated Go files declare `protoc-gen-go v1.36.12` and the generated TypeScript declares `protoc-gen-es v2.17.0`. Because the plugins are remote and unpinned in `buf.gen.yaml`, a later regeneration can pick up newer plugin versions and produce a noisy diff. That's normal, but worth knowing before you blame your own change. If you want reproducible output, pin versions in the `remote:` entries (e.g. `buf.build/protocolbuffers/go:v1.36.12`).

Don't edit anything under `gen/` or `services/gateway/src/lib/gen/` by hand.

### Who consumes what

- **Go services** import `github.com/francispowlesland/microservices-app-go/gen/go/<name>/v1` (for example `userv1`). Dockerfiles copy `gen/` into the build.
- **The gateway** imports the `*_pb.ts` files through the `#lib/gen/...` alias and builds clients with Connect (`createClient(OrdersService, transport)`). The TypeScript runtime is `@bufbuild/protobuf`.

## Conventions for evolving protos

These come from the existing files and the `buf` rule sets, not from a separate style guide.

**Do**

- Add new fields with **new field numbers** and new RPCs with new messages. This is compatible under the `FILE` breaking rules.
- Keep request and response messages per RPC (`GetOrderRequest`/`GetOrderResponse`), as every service does today. That's also what lets `STANDARD` lint pass.
- Follow the pagination shape: `page_size` and `page_token` in the request, `next_page_token` in the response. Services implement it with `internal/platform/pagination`.
- Mark secret-bearing fields `[debug_redact = true]`, as `user.proto` does for passwords and tokens, so they don't leak through debug printing of messages.
- Keep credentials out of response messages. The `User` message deliberately has no password or hash field.
- Use enum values prefixed by the enum name with a zero `..._UNSPECIFIED` value (`USER_STATUS_UNSPECIFIED = 0`). The database schemas store these enums as small integers with `CHECK` constraints, so **never renumber an enum value**. The users and payments migrations spell out the number-to-meaning mapping in comments.

**Don't**

- Reuse or renumber a field number or enum value, or change a field's type. `buf breaking` will flag it, and a stored enum number changing meaning would not announce itself at all.
- Rename fields, messages or RPCs casually; `FILE` treats renames as breaking.
- Add a service or message to an existing package without checking the gateway: a regenerated `*_pb.ts` changes its imports for everything that uses it.

**Removing something:** `reserved` the field number and name so they can't be reused. No `.proto` in the repo does this yet, since nothing has been removed.

**Breaking changes need a new version.** Packages are `*.v1`. A genuinely incompatible change means a `v2` package alongside, not an edit in place.

Check a change against the last commit with:

```sh
buf breaking proto --against '.git#subdir=proto'
```

(from the repo root). `buf lint proto` runs the lint rules. Both currently pass on this repo.

### After a change

1. Edit the `.proto`.
2. `cd proto && buf lint && buf generate`.
3. Update the server implementation. Handlers embed `Unimplemented<Name>ServiceServer`, so a new RPC compiles but returns `Unimplemented` until you write the handler. The compiler will not remind you.
4. Update the gateway where it uses the changed messages, and run `npm run check` there.
5. Commit proto and generated code together.

If the change touches an authorisation-relevant field (like `user_id` on orders), remember that services re-derive filters from the request and token claims, never from the page token. See [auth.md](auth.md).
