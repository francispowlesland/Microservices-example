# Architecture

Four Go gRPC services, each with its own Postgres database, and a SvelteKit gateway in front. The gateway is the only thing with a published port. Everything else lives on the compose network and talks to nobody except the gateway.

```
                 browser
                    │  HTTP :3000   (the only published port)
                    ▼
        ┌───────────────────────┐
        │ gateway (SvelteKit)   │  cookies hold the tokens; no database
        └───────────┬───────────┘
        gRPC (plaintext HTTP/2), Bearer access token on every call
   ┌────────┬───────┴─┬───────────┐
   ▼        ▼         ▼           ▼
 users    orders   payments   inventory
 :50051   :50052   :50053     :50054
   │        │         │           │
users-db orders-db payments-db inventory-db   (Postgres 17, one each)
```

## The services

| Service | gRPC services it registers | Port | Database | README |
|---|---|---|---|---|
| users | `user.v1.UserService`, `user.v1.AuthService` | 50051 | `users` | [services/users](../services/users/README.md) |
| orders | `orders.v1.OrdersService` | 50052 | `orders` | [services/orders](../services/orders/README.md) |
| payments | `payments.v1.PaymentsService` | 50053 | `payments` | [services/payments](../services/payments/README.md) |
| inventory | `inventory.v1.InventoryService` | 50054 | `inventory` | [services/inventory](../services/inventory/README.md) |
| gateway | none (it's a client) | 3000 (HTTP) | none | [services/gateway](../services/gateway/README.md) |

The users service is the only one that issues tokens, and the only one with side-effects beyond its own database (it queues "emails", which are currently log lines). See [auth.md](auth.md).

## How they communicate

**Gateway to services, over gRPC, plus exactly one service-to-service call: orders to inventory.** "Microservices" usually implies a web of calls between them; here there's one edge. When an order is placed, orders calls `inventory.ReserveStock` (all lines or none, and it returns the unit prices the total is built from). Deleting an order calls `ReleaseStock`. Orders authenticates with its own service token, signed with a separate key pair from user tokens (see [auth](auth.md)). Nothing else dials anything, and there's no message broker.

The consequences are easy to see in the schemas:

- Because orders and inventory have separate databases, reserve-then-save is not one transaction. If saving the order fails, orders releases the stock; if that release fails it's logged for manual correction (no retry queue or outbox). A reserve that times out might still have committed.
- `orders.user_id` and `payments.order_id` are plain `uuid` columns. No foreign keys (they'd have to cross databases), and no check in the code that the referenced user or order exists.
- Payments will happily process a payment for an order ID that orders has never heard of. It records the ID, applies its own rules and moves on.
- Anything that needs cross-service consistency has to be done by the caller, which today means the gateway or a human.

That's a perfectly defensible trade for a small system: no distributed transactions, no cascading failures, no versioning problem between services beyond the protos. The price is that the "relationships" are conventions rather than constraints.

The gateway talks to backends using Connect's Node gRPC transport (`createGrpcTransport`), with one shared HTTP/2 session per backend, a 5-second default timeout and the caller's access token attached as `authorization: Bearer …`. Backend addresses come from `USERS_SERVICE_URL`, `ORDERS_SERVICE_URL`, `PAYMENTS_SERVICE_URL` and `INVENTORY_SERVICE_URL` (defaulting to `http://localhost:5005x`). Both ends are plaintext; there's no TLS or mutual TLS between gateway and services. Keep them on a private network.

## Database ownership

One Postgres database per service, and only that service holds the connection string. `docker-compose.yml` gives each its own `postgres:17-alpine` container and named volume (`users-db-data` and so on). None of the database containers publish a port.

The databases all use the same `postgres`/`postgres` credentials in compose. That is fine for a laptop and not something to copy anywhere else; isolation here comes from the network and from each service only being told its own URL.

### Migrations

Each service embeds its SQL with `go:embed` (`services/<name>/migrations/embed.go`) and applies it with [goose](https://github.com/pressly/goose) **on every startup**, before opening its connection pool. There's no separate migration step or job. Each service currently has a single migration, `00001_*.sql`.

Both `pgxutil.Migrate` and `pgxutil.Connect` retry with backoff for up to 30 seconds, so a service that starts a moment before its database doesn't need to be babysat. Compose still adds a `service_healthy` dependency on the database for good measure.

Two things worth knowing:

- Migrations run as whoever `DATABASE_URL` says, which in compose is the Postgres superuser. A least-privilege runtime role would need a separate migration identity; nothing in the repo does that yet.
- The payments migration installs a trigger that makes `payments` an append-only ledger: `DELETE` is rejected, and `amount_cents`, `currency`, `order_id` and `created_by` can't be updated. So the immutability is enforced by the database rather than by good intentions in Go.

## Shared code: `internal/platform`

One Go module (`github.com/francispowlesland/microservices-app-go`) holds all the services, the generated code and the shared packages. Each service's Dockerfile copies `gen/`, `internal/` and its own `services/<name>/` directory, with the repo root as build context.

| Package | What it does |
|---|---|
| `authn` | Ed25519 JWT `Signer` and `Verifier`, PEM key parsing, a `Claims` type (`Subject`, `Roles`, `IsAdmin()`), and a unary gRPC interceptor that is default-deny. Details in [auth.md](auth.md). |
| `config` | Four environment helpers: `String`, `Required`, `Duration`, `Int`. They return errors rather than exiting; each service's `main` decides what to do. |
| `grpcx` | `Run` builds the server and handles graceful shutdown (5 s default before a hard stop). Interceptors, in order: panic recovery (returns `Internal`, logs the stack), request logging (method, code, duration, peer; no bodies), then `authn` if a verifier was supplied. Registers the standard health service, and reflection unless `APP_ENV=production`. `HealthCheck` is the client side, used by each binary's `healthcheck` subcommand. |
| `pagination` | Keyset pagination on `(created_at, id)`. `PageSize` defaults to 50 and clamps to 200. `Encode`/`Decode` produce an opaque base64 token. The token is not signed, so services re-apply their filters from the request and claims on every page. |
| `pgxutil` | `Connect` (pgx pool: 25 max, 2 min connections, with retry), `Migrate` (goose), `IsUniqueViolation`, `IsNotFound`. |
| `pgxutil/pgxtest` | Test helper that starts a throwaway Postgres 16 container. See [local-development.md](local-development.md). |

Only unary interceptors are installed. That means streaming endpoints, in practice the health `Watch` and server reflection, don't pass through `authn`. Nothing else in the services is streaming.

### Per-service layout

Each service follows the same shape: `cmd/<name>/main.go` (wiring only), `internal/config`, `internal/service` (handlers, business rules, authorisation, against a small `Store` interface), `internal/store` (pgx SQL), `migrations/`, `Dockerfile`. The design briefs these were built from are in [`prompts/`](../prompts/); `00-platform-and-conventions.md` is the one that explains why the layout looks like this.

Every image is a multi-stage build ending in `gcr.io/distroless/static-debian12:nonroot`, defaulting to `APP_ENV=production`, with a `healthcheck` subcommand and a Dockerfile `HEALTHCHECK` that runs it (there's no shell in the image to run anything else).

Every service lists `/grpc.health.v1.Health/Check` as a public method so the tokenless health probe passes `authn`; the compose stack was run to confirm all containers report healthy. Compose starts orders after inventory, and a one-shot `inventory-seed` container loads sample data once inventory is healthy.

## Why it looks like this, and what it costs

- **gRPC and protobuf contracts** give typed clients on both sides (Go and TypeScript are generated from the same files; see [protobuf-and-codegen.md](protobuf-and-codegen.md)). The cost is that the browser can't talk to services directly, hence the gateway.
- **Database per service** means a service can change its schema without negotiating with anyone. It also means no joins across services and no referential integrity between them (see above).
- **A single module with shared `internal/platform`** keeps auth, pagination and config consistent. The price is that a change to `authn` is a change to every service, and they all build from one `go.mod`.
- **Stateless gateway.** Sessions are cookies containing the tokens themselves, so there's no session store to run. Details, and the one in-process shortcut it takes, are in [auth.md](auth.md).
- **Apply migrations at startup** is simple and means a deployed binary always matches its schema. It also means a bad migration stops the service starting, which is arguably the right failure but is still a failure.
