# inventory

gRPC service implementing `inventory.v1.InventoryService`
(`proto/inventory/v1/inventory.proto`, generated package `inventoryv1`).
Port `:50054`. It owns its own Postgres database (`inventory`) and never
connects to another service's database.

## Behaviour

Every RPC needs a valid JWT (default deny). Inventory is shared catalogue
data, so reads are open to any authenticated user and writes are admin-only.

| RPC | Who | Notes |
|---|---|---|
| `CreateInventoryItem` | admin | Service generates the id. SKU is trimmed and upper-cased (ASCII only), must match `^[A-Z0-9][A-Z0-9._-]{0,63}$`. `quantity >= 0`. Duplicate SKU: `AlreadyExists`. |
| `GetInventoryItem` | any authenticated | Unknown id: `NotFound`. |
| `UpdateInventoryItem` | admin | Full replace of `sku` and `quantity`. SKU held by another item: `AlreadyExists`. Unknown id: `NotFound`. Bumps `updated_at`. |
| `DeleteInventoryItem` | admin | Unknown id: `NotFound`. |
| `ListInventoryItems` | any authenticated | Keyset pagination on `(created_at, id)`, newest first. Default page 50, max 200. |

Non-admin writes return `PermissionDenied`. Bad UUIDs, SKUs, quantities and
page tokens return `InvalidArgument`. Anything unexpected is logged
server-side and returned as `Internal: internal error`.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `GRPC_ADDR` | `:50054` | Listen address. |
| `DATABASE_URL` | none, required | Postgres URL for this service's own database. |
| `APP_ENV` | `development` | `production` disables gRPC reflection. |
| `JWT_PUBLIC_KEY` | none, required | Ed25519 public key, PKIX PEM. Issuer `microservices-app/users`, audience `microservices-app`. |

Migrations are embedded and applied on startup. Connect and migrate retry for
about 30 seconds, so the service can start alongside its database.

## Contract gaps

`InventoryItem` has only `id`, `sku` and `quantity`. `UpdateInventoryItem` is a
full replace with no field mask, and there is no atomic adjust or reserve RPC.

Because `UpdateInventoryItem` sets an **absolute** `quantity`, two concurrent
callers doing read-modify-write can lose an update. This service cannot fix
that on its own. When orders start reserving stock, add
`AdjustQuantity(id, delta)`: an atomic `quantity = quantity + $delta` that
fails if the result would be negative. It is deliberately not built now, and
`proto/` has not been touched.

## Platform notes

- Uses `internal/platform/{config,authn,pagination,pgxutil,grpcx}` as-is.
  No gaps found that needed a workaround.
- `grpcx.Config.PublicMethods` is left empty, so every method requires a token.
- No dependencies were added. Everything the service imports is already in
  `go.mod` (`google.golang.org/grpc/test/bufconn` ships with grpc).

## Run

```sh
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/inventory?sslmode=disable'
# Run `go run ./tools/keygen` and paste the printed PUBLIC KEY PEM block:
export JWT_PUBLIC_KEY="$(cat users-public.pem)"
go run ./services/inventory/cmd/inventory
```

`inventory healthcheck` dials `GRPC_ADDR` on localhost and calls
`grpc.health.v1.Health/Check`. It exits 0 if SERVING, otherwise 1.

## Test

```sh
go test -short ./services/inventory/...   # unit + bufconn, no Docker
go test ./services/inventory/...          # also runs store tests on a throwaway Postgres (needs Docker)
```

- `internal/service`: fake `Store`; auth matrix for every RPC, SKU
  normalisation table, quantity and UUID validation, duplicate-SKU mapping,
  pagination, error non-leakage, registration check, plus a `bufconn` test
  through the real auth interceptor (admin writes, customer reads and lists,
  customer write denied, no token `Unauthenticated`).
- `internal/store`: real Postgres via `pgxtest.StartPostgres`: CRUD, unique
  SKU, `quantity >= 0` check, keyset pagination across `created_at` ties.
  Skipped under `-short` or when Docker is unavailable.

## Docker

Build context is the repo root:

```sh
docker build -f services/inventory/Dockerfile -t inventory:dev .
```

Smoke test against a throwaway Postgres (no secrets are baked into the image):

```sh
docker network create inv-net
docker run -d --name inv-pg --network inv-net \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=inventory postgres:17
docker run -d --name inv --network inv-net \
  -e DATABASE_URL='postgres://postgres:postgres@inv-pg:5432/inventory?sslmode=disable' \
  -e JWT_PUBLIC_KEY="$JWT_PUBLIC_KEY" \
  inventory:dev
docker inspect --format '{{.State.Health.Status}}' inv   # becomes "healthy"
docker stop inv   # exits cleanly
docker rm -f inv inv-pg && docker network rm inv-net
```

The image runs as non-root on `gcr.io/distroless/static-debian12:nonroot`.
The future compose file should give this service a dedicated Postgres service
and volume.
