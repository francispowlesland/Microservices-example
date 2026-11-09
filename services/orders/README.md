# orders

The orders service: a gRPC server implementing `orders.v1.OrdersService` (`proto/orders/v1/orders.proto`, generated Go package `ordersv1`) on `:50052`. It owns a Postgres database called `orders` and never touches any other service's database. Its one outbound call is to the inventory service: placing an order reserves stock there, and deleting an order puts it back.

Every RPC requires a JWT. There are no public methods.

Background reading, if you want it: [architecture](../../docs/architecture.md), [auth](../../docs/auth.md), [local development](../../docs/local-development.md), [protobuf and codegen](../../docs/protobuf-and-codegen.md).

## Configuration

| Variable | Required | Default | Notes |
|---|---|---|---|
| `DATABASE_URL` | yes | | Postgres URL for the `orders` database |
| `JWT_PUBLIC_KEY` | yes | | Ed25519 public key, PKIX PEM. Only the verifying half; this service never signs anything |
| `INVENTORY_ADDR` | no | `localhost:50054` | Inventory gRPC address (`inventory:50054` in compose) |
| `SERVICE_PRIVATE_KEY` | yes | | Ed25519 private key, PKCS#8 PEM, from `go run ./tools/keygen service`. Signs the service tokens inventory requires for reserve/release. Orders only |
| `GRPC_ADDR` | no | `:50052` | Listen address |
| `APP_ENV` | no | `development` | Anything other than `production` registers gRPC reflection |

`go run ./tools/keygen` prints a private and a public PEM to stdout. It doesn't write files, so save the public block yourself (the examples below assume `public.pem`).

The service refuses to start if either required variable is missing or if the key doesn't parse as an Ed25519 PKIX public key. The Docker image sets `APP_ENV=production` and `GRPC_ADDR=:50052`; nothing secret is baked in.

At startup it applies migrations, then connects (both retry for up to 30 seconds, to give a slow Postgres container time to wake up), then serves. `SIGINT`/`SIGTERM` trigger a graceful stop with a 5-second cap. Logs are JSON on stdout.

## Running

Locally, with a Postgres you already have:

```sh
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/orders?sslmode=disable'
export JWT_PUBLIC_KEY="$(cat public.pem)"
go run ./services/orders/cmd/orders
```

With the whole stack, from the repo root (needs `JWT_PRIVATE_KEY` and `JWT_PUBLIC_KEY` in the environment or a `.env`):

```sh
docker compose up --build orders
```

Compose gives orders its own Postgres (`orders-db`, volume `orders-db-data`) and doesn't publish the port; only the gateway is reachable from outside, and it reaches orders at `http://orders:50052`.

To build the image on its own (the build context is the repo root):

```sh
docker build -f services/orders/Dockerfile -t orders:dev .
```

## API

The caller's identity is the JWT `sub`, which must be a UUID. Admin status is the presence of `admin` in the `roles` claim. Tokens must be EdDSA-signed with issuer `microservices-app/users` and audience `microservices-app`, passed as `authorization: Bearer <jwt>`. How tokens are minted is the users service's business; see [auth](../../docs/auth.md).

| RPC | Who | Behaviour |
|---|---|---|
| `CreateOrder` | any authenticated user | The `id` is always generated server-side; whatever the client sends is ignored. An empty `user_id` means the caller. Naming another user is `PermissionDenied` unless you're an admin. Needs 1 to 100 `lines` (`sku`, `quantity >= 1`). Stock is reserved all-or-nothing, and `total_cents` is computed from inventory's prices at that moment; client-sent `total_cents` and `unit_price_cents` are ignored. Total above 10^12 is `InvalidArgument` (stock is given back). If saving fails after reserving, the stock is released. Unknown SKU is `InvalidArgument`; not enough stock is `FailedPrecondition`; inventory down is `Unavailable`. |
| `GetOrder` | owner or admin | Someone else's order is `NotFound`, not `PermissionDenied`, so ids can't be probed for existence. |
| `UpdateOrder` | admin | Reassigns `user_id` only (required); bumps `updated_at`. Lines and total are fixed once placed, and anything sent for them is ignored. |
| `DeleteOrder` | admin | Deleting an order that isn't there is `NotFound`. Its lines' stock is released back to inventory after the delete; a failed release is logged and does not fail the delete. |
| `ListOrders` | any authenticated user | Newest first. Non-admins always see only their own orders; asking for another `user_id` is `PermissionDenied`. Admins see everything, or can filter by `user_id`. |

Other status codes you'll meet:

- `Unauthenticated`: missing or invalid token, or a `sub` that isn't a UUID.
- `InvalidArgument`: missing `order`, malformed UUIDs, missing/invalid lines, order total too large, bad `page_token`.
- `Internal`: any store failure. The client gets a bare `internal error`; the detail goes to the log.

### Pagination

`ListOrders` uses keyset pagination on `(created_at, id)` descending, with an opaque base64 `page_token`. `page_size` defaults to 50 and is capped at 200. The user filter is rebuilt from the request and the caller's claims on every page, never read back out of the token, so a non-admin can't widen their view by editing a token. The service fetches `size + 1` rows to decide whether a next page exists.

### Timestamps

`created_at` and `updated_at` exist only in the database. The `Order` message has no timestamp fields, so clients never see them.

## Data model

`orders`, created by `migrations/00001_create_orders.sql` (goose, embedded in the binary):

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid` | primary key |
| `user_id` | `uuid` | not null; no foreign key (the users table lives in a different database) |
| `total_cents` | `bigint` | not null, `CHECK (total_cents >= 0)` |
| `created_at` | `timestamptz` | default `now()` |
| `updated_at` | `timestamptz` | default `now()`; set to `now()` on update |

`order_lines` (`migrations/00002_create_order_lines.sql`) holds `order_id` (cascade delete), `position`, `sku`, `quantity > 0` and `unit_price_cents >= 0`, keyed on `(order_id, position)`. Orders placed before this migration have no lines.

Two indexes back the list queries: `(user_id, created_at DESC, id DESC)` for per-user listing and `(created_at DESC, id DESC)` for admins. The database enforces the lower bound on `total_cents`; the 10^12 upper bound lives only in the service.

## Code layout

```
cmd/orders/        wiring, plus the `healthcheck` subcommand
internal/config/   env config
internal/service/  handlers, authorisation, the Store interface
internal/store/    pgx implementation (all the SQL)
migrations/        goose SQL, embedded
```

Handlers talk to a `Store` interface, which is what lets the service tests run against an in-memory fake.

## Testing

```sh
go test -short ./services/orders/...   # service and bufconn tests; no Docker
go test ./services/orders/...          # also the store tests, via testcontainers (needs Docker)
```

The service tests cover authorisation, filter forcing, pagination (including identical `created_at` values) and the "store errors aren't leaked" rule. The bufconn test sends real requests through the JWT interceptor. The store tests run the actual SQL against `postgres:16-alpine` and skip themselves under `-short` or when Docker isn't usable.


## Known limitations

- **Reserve and save aren't one transaction.** If saving fails the stock is released, and if *that* fails (or the process dies between the two steps) the stock stays missing until an admin corrects it; the lines are in the log. A reserve that times out may also have committed. Fixing both needs a retry queue/outbox and an idempotency key on `ReserveStock`. Orders also doesn't verify that `user_id` exists.
- **The contract is still thin.** Sensible additions would be:
  - a `status` field and state-transition RPCs instead of admin full-replace;
  - `created_at` / `updated_at` on `Order`;
  - a `FieldMask` on `UpdateOrderRequest` for partial updates;
  - an idempotency key on `CreateOrder` (a retry after a timeout currently creates a duplicate);
  - ordering and filter options on `ListOrdersRequest`.
- `pgxutil.Migrate` calls `goose.SetBaseFS` on a package global. Harmless with one migration filesystem per process, which is the case here.
