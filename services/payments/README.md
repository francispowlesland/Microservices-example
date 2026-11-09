# payments

`payments.v1.PaymentsService` (`proto/payments/v1/payments.proto`) is a payment **ledger** with a small state machine. There is no bank and no payment processor. The service decides every outcome itself, deterministically, based on one number.

It listens on `:50053`, owns the `payments` Postgres database and never connects to another service's database. It also never calls another service: `order_id` is checked for being a UUID, not for belonging to an order that exists (see [Known limitations](#known-limitations)). The SvelteKit gateway is the only client in the repo; it calls `ProcessPayment` from the order page, and `ListPayments`, `GetPayment` and `RefundPayment` from the payments pages. For the wider picture, see [architecture](../../docs/architecture.md) and [auth](../../docs/auth.md).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `GRPC_ADDR` | `:50053` | Listen address. |
| `DATABASE_URL` | – (required) | Postgres URL for the `payments` database. |
| `APP_ENV` | `development` | `production` disables gRPC reflection. |
| `JWT_PUBLIC_KEY` | – (required) | Ed25519 public key, PKIX PEM. The service only verifies tokens; it never signs them. `go run ./tools/keygen` makes a pair. |
| `PAYMENT_MAX_CENTS` | `1000000` | Largest amount the simulated processor accepts. Must be positive; startup fails otherwise. |

An empty value counts as unset, so `GRPC_ADDR=` gives you the default rather than a listener on nowhere in particular.

## Running

Locally, against any Postgres you have to hand:

```sh
export DATABASE_URL='postgres://postgres:pw@localhost:5432/payments?sslmode=disable'
export JWT_PUBLIC_KEY="$(go run ./tools/keygen | awk '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/')"
go run ./services/payments/cmd/payments
```

Migrations are applied on startup, and both the migration and the connection retry for up to about 30 seconds, so starting the service a moment before its database is fine. SIGINT or SIGTERM triggers a graceful stop (forced after 5 seconds).

Under the root `docker-compose.yml` the service is `payments`, backed by its own `payments-db` (Postgres 17) with the named volume `payments-db-data`. It publishes no ports; only the gateway does. Compose sets just `DATABASE_URL` and `JWT_PUBLIC_KEY` and takes the rest from the image defaults. More on getting everything up in [local development](../../docs/local-development.md).

### Docker

The build context is the repo root:

```sh
docker build -f services/payments/Dockerfile -t payments:dev .
```

The image is a static binary on distroless, non-root, at about 20 MB. It sets `APP_ENV=production`, `GRPC_ADDR=:50053` and `PAYMENT_MAX_CENTS=1000000`; `DATABASE_URL` and `JWT_PUBLIC_KEY` are supplied at runtime only. The build stage pins `golang:1.27`, matching the minor version in `go.mod`.

A smoke test with a throwaway Postgres:

```sh
docker network create paynet
docker run -d --rm --name paypg --network paynet \
  -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=payments postgres:17
docker run -d --name payments --network paynet \
  -e DATABASE_URL='postgres://postgres:pw@paypg:5432/payments?sslmode=disable' \
  -e JWT_PUBLIC_KEY="$(go run ./tools/keygen | awk '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/')" \
  payments:dev
docker exec payments /payments healthcheck   # exit 0 when SERVING
docker stop payments                         # exits cleanly
```

`/payments healthcheck` dials `GRPC_ADDR` on localhost and calls the standard `grpc.health.v1.Health/Check`. The Dockerfile's `HEALTHCHECK` runs it every 10 seconds, with a 30-second start period.

## API

All four RPCs sit behind the shared default-deny auth interceptor, so every call needs a valid bearer token (EdDSA, issuer and audience as defined in `internal/platform/authn`). The token's `sub` must be a UUID, otherwise the call fails with `Unauthenticated: invalid token subject`. The only exemption is `grpc.health.v1.Health/Check`, listed in `PublicMethods` in `cmd/payments/main.go`.

| RPC | Who | Notes |
|---|---|---|
| `ProcessPayment` | any authenticated user | Idempotent; see below. |
| `GetPayment` | owner or admin | Someone else's payment returns `NotFound`, not `PermissionDenied`, so ids can't be probed. |
| `ListPayments` | owner or admin | A non-admin is always restricted to their own rows; an admin sees everything. Optional `order_id` filter. Keyset pagination, newest first. |
| `RefundPayment` | admin only (`PermissionDenied` otherwise) | Full refunds only. |

Ownership is `created_by`, the caller's JWT `sub`. It's stored but never returned: the proto `Payment` has no owner field. The same goes for `refund_reason` and the timestamps. The wire message carries `id`, `order_id`, `amount_cents`, `currency`, `status` and `failure_reason`, and that's all.

Internal errors are logged server-side and returned to the caller as a bare `Internal: internal error`.

### Validation

| Field | Rule | Error |
|---|---|---|
| `order_id`, `id` | Parseable UUID | `InvalidArgument` |
| `amount_cents` | Greater than zero | `InvalidArgument` |
| `currency` | Exactly `USD`, `EUR` or `GBP` (upper-case) | `InvalidArgument` |
| `idempotency_key` | 1 to 128 printable ASCII characters (`0x20`–`0x7e`) | `InvalidArgument` |
| `reason` (refund) | Trimmed; at most 500 characters | `InvalidArgument` |
| `page_token` | Must decode | `InvalidArgument` |

`page_size` is clamped: zero or negative gives 50, and anything above 200 gives 200.

### The simulated processor

`amount_cents <= PAYMENT_MAX_CENTS` gives `SUCCEEDED`. Anything larger gives `FAILED` with `failure_reason = "amount exceeds limit"`. A decline is a **normal OK response containing a `FAILED` payment**, not a gRPC error, so callers have to look at `status`, not just at whether the call returned an error.

`PAYMENT_STATUS_PENDING` exists in the enum, but this service never leaves a row in it: every payment is settled in the same transaction that creates it. (The database `CHECK` still permits `1`, so the state is reserved rather than impossible.)

### Idempotency

The key is scoped to the caller, with a unique constraint on `(created_by, idempotency_key)`. Two users can therefore use the same key without noticing each other.

- Key unseen: a new payment is inserted and returned.
- Key seen, with the same `order_id`, `amount_cents` and `currency`: the **original payment** is returned and no new row is written. That includes a `FAILED` original; a retry doesn't get a second opinion.
- Key seen, with any of those three different: `InvalidArgument` with "idempotency key reused with different parameters".

The insert is `INSERT ... ON CONFLICT DO NOTHING RETURNING`, followed by a read-back in the same transaction. A concurrent call with the same key waits for the first to commit, so exactly one row results (there's a goroutine test for it).

### Refunds and the state machine

```
SUCCEEDED --(RefundPayment, admin)--> REFUNDED
FAILED, REFUNDED: terminal
```

`RefundPayment` does `SELECT ... FOR UPDATE`, requires `SUCCEEDED`, then sets `REFUNDED`, stores the trimmed `reason` and bumps `updated_at`. Anything else returns `FailedPrecondition`, including a repeat refund. An unknown id returns `NotFound`. Of several concurrent refunds of one payment, exactly one succeeds.

## Data model

One table, `payments`, created by `migrations/00001_create_payments.sql` (goose, embedded in the binary).

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid` | Primary key, generated by the service. |
| `order_id` | `uuid` | Not a foreign key; there's nothing in this database to point at. |
| `amount_cents` | `bigint` | `CHECK (amount_cents > 0)`. |
| `currency` | `char(3)` | |
| `status` | `smallint` | `CHECK` in 1–4: 1 `PENDING`, 2 `SUCCEEDED`, 3 `FAILED`, 4 `REFUNDED`. |
| `failure_reason`, `refund_reason` | `text` | Default `''`. |
| `created_by` | `uuid` | The caller's `sub`. |
| `idempotency_key` | `text` | Unique with `created_by`. |
| `created_at`, `updated_at` | `timestamptz` | Default `now()`. |

Indexes on `(order_id, created_at DESC, id DESC)` and `(created_by, created_at DESC, id DESC)` serve the two list filters. Page tokens are opaque base64 encodings of the last row's `(created_at, id)`.

### Ledger rules

The store interface exposes insert, get, list and a status transition, and nothing else: there is deliberately no way to edit an amount. The database backs that up. The `payments_guard` triggers reject every `DELETE`, and any `UPDATE` that changes `amount_cents`, `currency`, `order_id` or `created_by` (both with `restrict_violation`). Because they live in the migration, they're applied automatically on startup and travel with the service wherever it goes.

The `Down` migration drops the table. Don't run it against a database you care about; this is the ledger.

## Layout

```
cmd/payments/       wiring and the `healthcheck` subcommand
internal/config/    typed env config
internal/service/   handlers, validation, authorisation; depends on a small Store interface
internal/store/     pgx implementation
migrations/         goose SQL, embedded
```

The handlers know nothing about SQL and the store knows nothing about gRPC codes. It's a boring split, and that's the point. The proto and its generated code are covered in [protobuf and codegen](../../docs/protobuf-and-codegen.md).

## Testing

```sh
go test -short ./services/payments/...   # unit tests only, no Docker
go test ./services/payments/...          # adds real-Postgres store tests (testcontainers)
```

- `internal/service` tests run against an in-memory fake store, including an end-to-end pass over `bufconn` and an auth matrix (owner, other customer, admin).
- `internal/store` tests run against a real Postgres: replay, concurrent same-key inserts, concurrent refunds, ledger immutability, constraints and keyset pagination. They skip under `-short` or when Docker isn't available.
- **Podman:** on a Podman-backed Docker socket the testcontainers reaper can fail (`network bridge not found`), so the store tests skip. Run with `TESTCONTAINERS_RYUK_DISABLED=true` to execute them.
- **Podman and `HEALTHCHECK`:** Podman builds OCI images by default and ignores `HEALTHCHECK` (with a build warning). Use `--format docker` or a real Docker daemon to bake it in. `/payments healthcheck` works either way.

## Known limitations

- **No order validation.** `order_id` is any UUID. Nothing checks that the order exists, belongs to the caller or has the amount you've just paid. The service trusts its caller on all three.
- **Plaintext gRPC.** The server has no TLS; it relies on being reachable only on the internal network, as in compose.
- **Partial immutability.** The triggers protect the money-defining columns. `status`, `failure_reason`, `refund_reason` and `idempotency_key` can still be changed by anyone with direct SQL access (status only to a value the `CHECK` allows). The service itself only ever performs the `SUCCEEDED` to `REFUNDED` transition.
- **Full refunds only,** and refund history exists only as the final row state plus `refund_reason`; there are no separate refund records.
- **Simulated outcomes.** The only possible failure is "amount exceeds limit".
- **Ownership isn't visible to clients.** An admin listing all payments can't see whose they are, since `created_by` isn't in the response.
