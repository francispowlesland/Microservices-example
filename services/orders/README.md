# orders

The orders service: a gRPC server implementing `orders.v1.OrdersService` (`proto/orders/v1/orders.proto`, generated Go package `ordersv1`) on `:50052`. It owns a Postgres database called `orders` and never touches any other service's database. It also never calls any other service, which is either admirable isolation or an unfinished job, depending on how you squint (see [Known limitations](#known-limitations)).

Every RPC requires a JWT. There are no public methods.

Background reading, if you want it: [architecture](../../docs/architecture.md), [auth](../../docs/auth.md), [local development](../../docs/local-development.md), [protobuf and codegen](../../docs/protobuf-and-codegen.md).

## Configuration

| Variable | Required | Default | Notes |
|---|---|---|---|
| `DATABASE_URL` | yes | | Postgres URL for the `orders` database |
| `JWT_PUBLIC_KEY` | yes | | Ed25519 public key, PKIX PEM. Only the verifying half; this service never signs anything |
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
| `CreateOrder` | any authenticated user | The `id` is always generated server-side; whatever the client sends is ignored. An empty `user_id` means the caller. Naming another user is `PermissionDenied` unless you're an admin. `total_cents` must be in `0..10^12`. |
| `GetOrder` | owner or admin | Someone else's order is `NotFound`, not `PermissionDenied`, so ids can't be probed for existence. |
| `UpdateOrder` | admin | Full replace of `user_id` and `total_cents` (both required and validated); bumps `updated_at`. |
| `DeleteOrder` | admin | Deleting an order that isn't there is `NotFound`. |
| `ListOrders` | any authenticated user | Newest first. Non-admins always see only their own orders; asking for another `user_id` is `PermissionDenied`. Admins see everything, or can filter by `user_id`. |

Other status codes you'll meet:

- `Unauthenticated`: missing or invalid token, or a `sub` that isn't a UUID.
- `InvalidArgument`: missing `order`, malformed UUIDs, out-of-range `total_cents`, bad `page_token`.
- `Internal`: any store failure. The client gets a bare `internal error`; the detail goes to the log.

### Pagination

`ListOrders` uses keyset pagination on `(created_at, id)` descending, with an opaque base64 `page_token`. `page_size` defaults to 50 and is capped at 200. The user filter is rebuilt from the request and the caller's claims on every page, never read back out of the token, so a non-admin can't widen their view by editing a token. The service fetches `size + 1` rows to decide whether a next page exists.

### Timestamps

`created_at` and `updated_at` exist only in the database. The `Order` message has no timestamp fields, so clients never see them.

## Data model

One table, created by `migrations/00001_create_orders.sql` (goose, embedded in the binary):

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid` | primary key |
| `user_id` | `uuid` | not null; no foreign key (the users table lives in a different database) |
| `total_cents` | `bigint` | not null, `CHECK (total_cents >= 0)` |
| `created_at` | `timestamptz` | default `now()` |
| `updated_at` | `timestamptz` | default `now()`; set to `now()` on update |

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

Worth knowing: when I last ran the store tests here, Docker was running but container creation failed with `unable to find network with name or ID bridge`, so they skipped. The SQL in `internal/store` compiles and is vetted, but I haven't seen it pass against a real Postgres from this machine. Run the full suite somewhere Docker behaves.

## Known limitations

- **No cross-service checks.** Orders doesn't verify that `user_id` exists, reserves no inventory and doesn't involve payments. Consistency between services is a later design problem.
- **The `healthcheck` subcommand probably doesn't work.** It calls `grpc.health.v1.Health/Check` without a token, but orders registers no public methods, so by my reading of the interceptor the call is rejected as `Unauthenticated`. The Dockerfile's `HEALTHCHECK` uses this subcommand, which would leave the container permanently `unhealthy`. Payments whitelists that one method; orders doesn't. I haven't run this end to end, so check before relying on it either way.
- **The contract is thin, and `proto/` isn't ours to change here.** Sensible additions would be:
  - line items, so `total_cents` can be derived instead of trusted;
  - a `status` field and state-transition RPCs instead of admin full-replace;
  - `created_at` / `updated_at` on `Order`;
  - a `FieldMask` on `UpdateOrderRequest` for partial updates;
  - an idempotency key on `CreateOrder` (a retry after a timeout currently creates a duplicate);
  - ordering and filter options on `ListOrdersRequest`.
- `pgxutil.Migrate` calls `goose.SetBaseFS` on a package global. Harmless with one migration filesystem per process, which is the case here.
