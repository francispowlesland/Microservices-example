# payments

`payments.v1.PaymentsService` (`proto/payments/v1/payments.proto`): a payment **ledger** with a small state machine. There is no bank and no processor. The service decides the outcome itself, deterministically.

Port `:50053`. Owns the `payments` database and never connects to another service's database.

## Environment

| Variable | Default | Notes |
|---|---|---|
| `GRPC_ADDR` | `:50053` | Listen address. |
| `DATABASE_URL` | – (required) | Postgres URL for the `payments` database. |
| `APP_ENV` | `development` | `production` disables gRPC reflection. |
| `JWT_PUBLIC_KEY` | – (required) | Ed25519 public key, PKIX PEM. `go run ./tools/keygen` makes a pair. |
| `PAYMENT_MAX_CENTS` | `1000000` | Largest amount the simulated processor accepts. Must be positive. |

## Behaviour

| RPC | Who | Notes |
|---|---|---|
| `ProcessPayment` | any authenticated user | Idempotent, see below. |
| `GetPayment` | owner or admin | Someone else's payment returns `NotFound`. |
| `ListPayments` | owner or admin | Non-admin is always restricted to their own rows. Optional `order_id` filter. Keyset pagination. |
| `RefundPayment` | admin only | Full refunds only. |

Ownership is `created_by`, the caller's JWT `sub`. It is stored but not returned (the proto `Payment` has no owner field).

### Simulated processor

`amount_cents <= PAYMENT_MAX_CENTS` gives `SUCCEEDED`. Anything larger gives `FAILED` with `failure_reason = "amount exceeds limit"`. A decline is a **normal OK response containing a `FAILED` payment**, not a gRPC error. Accepted currencies: `USD`, `EUR`, `GBP` (upper-case, exact).

### `PENDING` is unused

`PAYMENT_STATUS_PENDING` exists in the enum, but this service never leaves a row in it. Every payment is settled to `SUCCEEDED` or `FAILED` in the same transaction that creates it.

### Idempotency

The key is scoped to the caller: unique `(created_by, idempotency_key)`. It must be 1 to 128 printable ASCII characters.

- Key unseen: a new payment is inserted and returned.
- Key seen with the same `order_id`, `amount_cents` and `currency`: the **original payment** is returned, no new row.
- Key seen with any of those different: `InvalidArgument` "idempotency key reused with different parameters".

Insert is `INSERT ... ON CONFLICT DO NOTHING RETURNING`, then a read-back in the same transaction. Concurrent calls with the same key produce exactly one row (tested with goroutines).

### State machine

```
SUCCEEDED --(RefundPayment, admin)--> REFUNDED
FAILED, REFUNDED: terminal
```

`RefundPayment` does `SELECT ... FOR UPDATE`, requires `SUCCEEDED`, then sets `REFUNDED`, stores the trimmed `reason` (at most 500 chars) and bumps `updated_at`. Anything else returns `FailedPrecondition`, including a repeat refund. Unknown id: `NotFound`. Concurrent refunds: exactly one succeeds.

### Ledger rules

The store exposes only insert, get, list and a status transition. Migration `00001_create_payments.sql` also adds triggers (`payments_guard`) that reject any `UPDATE` changing `amount_cents`, `currency`, `order_id` or `created_by`, and every `DELETE`. Because they live in the migration, the container applies them itself on startup.

## Layout

```
cmd/payments/       wiring and the `healthcheck` subcommand
internal/config/    typed env config
internal/service/   handlers, validation, authorisation; depends on a small Store interface
internal/store/     pgx implementation
migrations/         goose SQL, embedded
```

## Run

```sh
export DATABASE_URL='postgres://postgres:pw@localhost:5432/payments?sslmode=disable'
export JWT_PUBLIC_KEY="$(go run ./tools/keygen | awk '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/')"
go run ./services/payments/cmd/payments
```

Migrations are applied on startup; the service retries the database for about 30 seconds.

## Test

```sh
go test -short ./services/payments/...   # unit tests only, no Docker
go test ./services/payments/...          # adds real-Postgres store tests (testcontainers)
```

Store tests skip under `-short` or when Docker is unavailable.

## Docker

Build context is the repo root:

```sh
docker build -f services/payments/Dockerfile -t payments:dev .
```

Smoke test with a throwaway Postgres:

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

The image sets `APP_ENV=production`, `GRPC_ADDR=:50053` and `PAYMENT_MAX_CENTS=1000000`; `DATABASE_URL` and `JWT_PUBLIC_KEY` are runtime-only. The image is distroless, non-root, about 20 MB.

The future compose file must give this service its **own dedicated Postgres service and a persistent volume**. That volume matters most here: this is the ledger.

## Notes on the shared platform

- **Health check vs auth (gap in `internal/platform/grpcx`).** `grpcx.Run` installs `authn` default-deny on all unary methods, and `grpc.health.v1.Health/Check` is unary, so `grpcx.HealthCheck` is rejected with `Unauthenticated`. Worked around in `cmd/payments/main.go` by passing `PublicMethods: {"/grpc.health.v1.Health/Check": true}`. The platform should probably do this itself. (Prompt 00 claims only streaming Watch and reflection are unauthenticated.)
- **Testcontainers under Podman.** On a Podman-backed Docker socket the testcontainers reaper fails (`network bridge not found`), so store tests skip. Run with `TESTCONTAINERS_RYUK_DISABLED=true` to execute them. `pgxtest` is untouched.
- **Podman and `HEALTHCHECK`.** Podman builds OCI images by default and ignores `HEALTHCHECK` (warning at build). Use `--format docker` or a real Docker daemon to bake it in; `/payments healthcheck` works either way.
- No missing dependencies: everything used is already in `go.mod`/`go.sum`.
- The Dockerfile pins `golang:1.27`, the minor version from `go.mod` (`go 1.27.2`).
