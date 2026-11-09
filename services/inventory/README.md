# inventory

The stock catalogue: a gRPC service implementing `inventory.v1.InventoryService`
(`proto/inventory/v1/inventory.proto`, generated package `inventoryv1`). It
listens on `:50054` and owns its own Postgres database (`inventory`), which no
other service connects to.

An item is an `id`, a `sku` and a `quantity`. That's the entire domain, which
keeps the rest of this README mercifully short.

Background reading, if you want it: [architecture](../../docs/architecture.md),
[auth](../../docs/auth.md), [local development](../../docs/local-development.md)
and [protobuf and codegen](../../docs/protobuf-and-codegen.md).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `GRPC_ADDR` | `:50054` | Listen address. |
| `DATABASE_URL` | none, required | Postgres URL for this service's own database. |
| `APP_ENV` | `development` | Anything other than `production` enables gRPC reflection. The Dockerfile sets `production`. |
| `JWT_PUBLIC_KEY` | none, required | Ed25519 public key, PKIX PEM. Tokens must have issuer `microservices-app/users` and audience `microservices-app`. |

Missing `DATABASE_URL` or `JWT_PUBLIC_KEY` stops the service at startup, as does
a key that isn't a valid Ed25519 PKIX PEM. Migrations are embedded in the binary
and applied on every start. Both the migration and the connection retry for
about 30 seconds, so the service can be started alongside its database without
anyone having to arrange the order.

## Running

```sh
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/inventory?sslmode=disable'
# `go run ./tools/keygen` prints a private and a public key. Save the public
# key PEM block (only the public one) to users-public.pem.
export JWT_PUBLIC_KEY="$(cat users-public.pem)"
go run ./services/inventory/cmd/inventory
```

In `docker-compose.yml` the service is `inventory`, backed by `inventory-db`
(Postgres 17) and the `inventory-db-data` volume. It takes `JWT_PUBLIC_KEY` from
your environment or `.env`, and the gateway reaches it at
`INVENTORY_SERVICE_URL=http://inventory:50054`.

The binary also has a `healthcheck` subcommand. `inventory healthcheck` dials
`127.0.0.1` on the port from `GRPC_ADDR` and calls `grpc.health.v1.Health/Check`.
It exits 0 on `SERVING` and 1 otherwise. The Dockerfile's `HEALTHCHECK` uses it
(see [Known limitations](#known-limitations) before trusting the result).

### Docker

The build context is the repo root:

```sh
docker build -f services/inventory/Dockerfile -t inventory:dev .
```

The image runs as non-root on `gcr.io/distroless/static-debian12:nonroot`, with
`APP_ENV=production` and `GRPC_ADDR=:50054` baked in. No secrets are.

## API

Every RPC requires a valid bearer JWT; nothing is public. Reads are open to any
authenticated user, since this is shared catalogue data. Writes need the `admin`
role.

| RPC | Who | Behaviour |
|---|---|---|
| `CreateInventoryItem` | admin | The service generates the id; any id the caller sends is ignored. Duplicate SKU: `AlreadyExists`. |
| `GetInventoryItem` | any authenticated user | Unknown id: `NotFound`. |
| `UpdateInventoryItem` | admin | Full replace of `sku` and `quantity`, and bumps `updated_at`. SKU held by a different item: `AlreadyExists`. Unknown id: `NotFound`. |
| `DeleteInventoryItem` | admin | Hard delete. Unknown id: `NotFound`. |
| `ListInventoryItems` | any authenticated user | Keyset pagination on `(created_at, id)`, newest first. Default page size 50, maximum 200. |

Validation, applied to create and update:

- `sku` is trimmed and upper-cased (ASCII letters only, so non-ASCII characters can't fold into valid ones),
  then must match `^[A-Z0-9][A-Z0-9._-]{0,63}$`. So `" abc-1 "` is stored as `ABC-1`.
- `quantity` must be `>= 0`.
- `item` must be present, and `id` must be a valid UUID for get, update and delete.

Error mapping:

| Code | When |
|---|---|
| `Unauthenticated` | Missing, malformed or invalid token. |
| `PermissionDenied` | Write RPC without the `admin` role. The admin check runs before any validation. |
| `InvalidArgument` | Bad UUID, SKU, quantity or `page_token`. |
| `NotFound` / `AlreadyExists` | As in the table above. |
| `Internal` | Anything else. The real error is logged server-side; the caller only sees `internal error`. |

`ListInventoryItems` fetches one row more than the page size to decide whether
there's a next page. `next_page_token` is empty on the last page. Tokens are
opaque (base64 of timestamp and id); treat them that way.

## Data model

One table, created by `migrations/00001_create_inventory_items.sql` (goose):

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid` | Primary key. |
| `sku` | `text`, not null | Unique index `inventory_items_sku_key`. |
| `quantity` | `integer`, not null | `CHECK (quantity >= 0)`. |
| `created_at` | `timestamptz`, not null | Defaults to `now()`. |
| `updated_at` | `timestamptz`, not null | Defaults to `now()`; set on update. |

There's also an index on `(created_at DESC, id DESC)` to serve the list query.
`created_at` and `updated_at` exist in the database but aren't part of the
`InventoryItem` message, so clients never see them.

## Testing

```sh
go test -short ./services/inventory/...   # unit + bufconn, no Docker
go test ./services/inventory/...          # also runs the store tests on a throwaway Postgres (needs Docker)
```

- `internal/service` uses a fake `Store`. It covers the auth matrix for every
  RPC, SKU normalisation, quantity and UUID validation, duplicate-SKU mapping,
  pagination, error non-leakage and a check that every RPC in the contract is
  registered. A `bufconn` test also goes through the real auth interceptor:
  admin writes, customer reads and lists, a customer write denied, and no token
  giving `Unauthenticated`.
- `internal/store` runs against real Postgres via `pgxtest.StartPostgres`: CRUD,
  unique SKU, the `quantity >= 0` check and keyset pagination across identical
  `created_at` values. These are skipped under `-short` or when Docker isn't
  available.

## Known limitations

- **Lost updates.** `UpdateInventoryItem` sets an absolute `quantity`, so two
  callers doing read-modify-write can overwrite each other, and this service
  can't prevent that on its own. There's no field mask and no atomic adjust or
  reserve RPC. When orders start reserving stock, the sensible addition is
  `AdjustQuantity(id, delta)`: an atomic `quantity = quantity + $delta` that
  fails if the result would be negative. It hasn't been built, and `proto/` is
  unchanged.
- **Health probe and auth.** `grpcx.Config.PublicMethods` is left empty in
  `cmd/inventory/main.go`, and the health client sends no token. Reading the
  code, that means `Health/Check` is rejected as `Unauthenticated`, so
  `inventory healthcheck` (and therefore the Docker `HEALTHCHECK`) would report
  failure against a running server. I haven't run it to confirm. `payments`
  and `users` allow `/grpc.health.v1.Health/Check`; `orders` doesn't either.
