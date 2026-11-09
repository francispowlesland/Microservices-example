# orders

gRPC service implementing `orders.v1.OrdersService` (`proto/orders/v1/orders.proto`, generated package `ordersv1`). Port `:50052`. Owns its own Postgres database (`orders`) and never connects to any other service's database.

## Environment

| Variable | Required | Default | Notes |
|---|---|---|---|
| `GRPC_ADDR` | no | `:50052` | listen address |
| `DATABASE_URL` | yes | | Postgres URL for the `orders` database |
| `APP_ENV` | no | `development` | `production` disables gRPC reflection |
| `JWT_PUBLIC_KEY` | yes | | Ed25519 public key, PKIX PEM. Generate a pair with `go run ./tools/keygen` |

No method is public: every RPC needs `authorization: Bearer <jwt>`. The caller is the JWT `sub` (must be a UUID) and `admin` comes from the `roles` claim.

## Behaviour

| RPC | Who | Notes |
|---|---|---|
| `CreateOrder` | any user | id is always generated; empty `user_id` means the caller; non-admin naming someone else gets `PermissionDenied`; `0 <= total_cents <= 10^12` |
| `GetOrder` | owner or admin | someone else's order is `NotFound` |
| `UpdateOrder` | admin | full replace of `user_id` and `total_cents`; bumps `updated_at` |
| `DeleteOrder` | admin | |
| `ListOrders` | any user | non-admin forced to own rows (other `user_id` is `PermissionDenied`); admin may filter by `user_id`. Keyset on `(created_at, id)` descending. Filter is taken from the request and claims on every page, never from the token |

`created_at` / `updated_at` exist only in the database for ordering. The `Order` message has no timestamps, so they are not returned.

## Layout

```
cmd/orders/            wiring, plus the `healthcheck` subcommand
internal/config/       env config
internal/service/      handlers, authorisation, Store interface (fake-store + bufconn tests)
internal/store/        pgx implementation (real-Postgres tests)
migrations/            goose SQL, embedded
```

## Run

```sh
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/orders?sslmode=disable'
export JWT_PUBLIC_KEY="$(cat public.pem)"
go run ./services/orders/cmd/orders
```

Migrations run on startup.

## Test

```sh
go test -short ./services/orders/...   # service + bufconn tests, no Docker
go test ./services/orders/...          # also store tests via testcontainers (needs Docker)
```

Store tests skip under `-short` or when Docker is unavailable.

## Docker

Build context is the repo root:

```sh
docker build -f services/orders/Dockerfile -t orders:dev .
```

Smoke test against a throwaway Postgres (the future compose file gives orders its own dedicated Postgres service and volume):

```sh
docker network create orders-net
docker run -d --name orders-db --network orders-net \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=orders postgres:17
docker run -d --name orders --network orders-net \
  -e DATABASE_URL='postgres://postgres:postgres@orders-db:5432/orders?sslmode=disable' \
  -e JWT_PUBLIC_KEY="$(cat public.pem)" \
  orders:dev
docker exec orders /orders healthcheck   # exit 0 when SERVING
docker stop orders orders-db && docker rm orders orders-db && docker network rm orders-net
```

No secrets are baked into the image; `DATABASE_URL` and `JWT_PUBLIC_KEY` are runtime only. The image sets `APP_ENV=production` and `GRPC_ADDR=:50052`.

## No cross-service checks

This version makes no calls to other services. Orders does not verify that `user_id` exists, reserves no inventory and does not talk to payments. Cross-service consistency is a later design problem.

## Contract gaps

The contract is deliberately thin. Suggested additions (not made; `proto/` is read-only):

- Line items (`product_id`, `quantity`, `unit_price_cents`) so `total_cents` can be derived rather than trusted.
- `status` (pending, paid, cancelled, ...) and state-machine RPCs instead of admin full-replace.
- `created_at` / `updated_at` on `Order`.
- `google.protobuf.FieldMask` on `UpdateOrderRequest` for partial updates.
- A request id / idempotency key on `CreateOrder` (a retried call after a timeout creates a duplicate).
- `ListOrdersRequest` ordering and filter options.

## Notes

- `internal/platform` needed no workarounds. `pgxutil.Migrate` calls `goose.SetBaseFS` on a package global; harmless here as the service has one migration FS.
- No new dependencies were required; `go.mod` / `go.sum` are untouched. `google.golang.org/grpc/test/bufconn` is part of the already-required grpc module.
- Store tests could not be executed in the authoring environment (Docker present but container creation failed: `unable to find network with name or ID bridge`), so the SQL in `internal/store` is compiled and vetted but **not yet run against Postgres**. Run `go test ./services/orders/...` where Docker works. Likewise the `docker build` / smoke test steps are unverified.
