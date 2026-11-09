# Build prompt 02: orders service (OrdersService)

**Prerequisite:** prompt `00-platform-and-conventions.md` has been completed. Read it first. Its conventions apply here without being repeated.

## Goal

Build `services/orders/`, a Go gRPC service implementing `orders.v1.OrdersService` from `proto/orders/v1/orders.proto` (generated package `ordersv1` at `github.com/francispowlesland/microservices-app-go/gen/go/orders/v1`). Port `:50052`, its own Postgres database.

## Contract limits you must work within

The current `Order` message has only `id`, `user_id` and `total_cents`. There are **no line items, no status, no timestamps, and `UpdateOrderRequest` has no field mask.** Build exactly that. Do not edit the proto. If you think the contract is too thin for sensible behaviour, list suggested additions in the README under "Contract gaps" and carry on.

There are also **no calls to other services** in this version. Orders does not check that `user_id` exists, does not reserve inventory, and does not talk to payments. Cross-service consistency is a later design problem; say so in the README.

## Config

`GRPC_ADDR` (`:50052`), `DATABASE_URL` (required), `APP_ENV`, `JWT_PUBLIC_KEY` (PEM, required).

## Data model

`orders`: `id uuid pk`, `user_id uuid not null`, `total_cents bigint not null check (total_cents >= 0)`, `created_at timestamptz not null default now()`, `updated_at timestamptz not null default now()`. Index on `(user_id, created_at desc, id desc)` for the per-user list, and on `(created_at desc, id desc)` for the admin list. **No foreign key to users** (separate database).

## Behaviour

Authentication: every method requires a valid token (no public methods). The caller identity is the JWT `sub`.

| RPC | Who | Rules |
|---|---|---|
| `CreateOrder` | any authenticated user | The service generates the `id`; ignore any client-supplied `id`. A non-admin may only create for themselves: if `user_id` is empty, use the caller's `sub`; if it is set and differs, `PermissionDenied`. Admins may set any valid `user_id` (must be required if they are creating on someone else's behalf, empty still defaults to their own). `total_cents` must be `>= 0` and at most 10^12 (reject overflow-ish values with `InvalidArgument`). |
| `GetOrder` | owner or admin | Another user's order returns `NotFound`. |
| `UpdateOrder` | admin only | Full replace of `user_id` and `total_cents` for the given `id`. Non-admin: `PermissionDenied`. Unknown id: `NotFound`. Bump `updated_at`. |
| `DeleteOrder` | admin only | Non-admin: `PermissionDenied`. Unknown id: `NotFound`. |
| `ListOrders` | any authenticated user | Non-admin: results are always restricted to the caller. If `user_id` is set and differs from the caller, `PermissionDenied`. Admin: optional `user_id` filter, otherwise all orders. Keyset pagination per prompt 00; the filter comes from the request on every page, never from the page token. |

The proto's `Order` message has no timestamps, so `created_at`/`updated_at` exist only in the database for ordering and are not returned. Say this in the README.

## Required tests

- Service tests with a fake Store: the full matrix of owner / other user / admin for every RPC; `CreateOrder` defaulting and spoofing attempts; negative and oversized totals; invalid UUIDs; `ListOrders` filter forcing for non-admins.
- Store tests against real Postgres: CRUD, `check` constraint, keyset pagination across pages with identical `created_at` values (tie-break on `id`), filter plus pagination together.
- One in-process `bufconn` test through the real auth interceptor using tokens signed by `authn.Signer` with a test key: customer creates and reads their own order; a second customer cannot read it; admin can; no token gets `Unauthenticated`.

## Also deliver

`README.md` (env vars, run and test instructions, "Contract gaps", the no-cross-service-checks note), and the `cmd/orders` wiring including the `healthcheck` subcommand.

### Dockerfile: `services/orders/Dockerfile`

Follow "Dockerfile (all services)" in prompt 00 exactly, with these specifics:
- Binary name `orders`, `EXPOSE 50052`, `ENTRYPOINT ["/orders"]`, `HEALTHCHECK CMD ["/orders", "healthcheck"]`.
- Build: `docker build -f services/orders/Dockerfile -t orders:dev .` from the repo root.
- Runtime env, none baked in: `DATABASE_URL`, `JWT_PUBLIC_KEY`. The image sets `APP_ENV=production`.
- **Its own database:** this service owns the `orders` database and never connects to any other service's database. The README shows a `docker run` pairing with a throwaway `postgres:17` container for a smoke test, and notes that the future compose file gives it a dedicated Postgres service and volume.

## Done when

`gofmt -l .` is empty; `go build ./... && go vet ./... && go test -short ./...` pass; `go test ./services/orders/...` passes with Docker; `docker build` succeeds and the image runs as non-root; against a throwaway Postgres container migrations apply and the health check reports healthy; the container stops cleanly on `docker stop`; nothing under `proto/` or `gen/` changed.
