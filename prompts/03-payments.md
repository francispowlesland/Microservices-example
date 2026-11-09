# Build prompt 03: payments service (PaymentsService)

**Prerequisite:** prompt `00-platform-and-conventions.md` has been completed. Read it first. Its conventions apply here without being repeated.

## Goal

Build `services/payments/`, a Go gRPC service implementing `payments.v1.PaymentsService` from `proto/payments/v1/payments.proto` (generated package `paymentsv1` at `github.com/francispowlesland/microservices-app-go/gen/go/payments/v1`). Port `:50053`, its own Postgres database.

This is **not a CRUD service** and **there is no bank or processor**. It is a payment ledger with a small state machine. The service itself decides whether a payment succeeds, using a deterministic rule (below), so behaviour is testable and repeatable. Do not add a payment-provider abstraction; if a real processor is added later, that is a separate task.

## Config

`GRPC_ADDR` (`:50053`), `DATABASE_URL` (required), `APP_ENV`, `JWT_PUBLIC_KEY` (PEM, required), `PAYMENT_MAX_CENTS` (default `1000000`, the largest amount the simulated processor accepts).

## Data model

`payments`: `id uuid pk`, `order_id uuid not null`, `amount_cents bigint not null check (amount_cents > 0)`, `currency char(3) not null`, `status smallint not null` (check it is a valid enum value other than UNSPECIFIED), `failure_reason text not null default ''`, `refund_reason text not null default ''`, `created_by uuid not null` (the caller's JWT `sub`), `idempotency_key text not null`, `created_at`, `updated_at`. Unique `(created_by, idempotency_key)`. Indexes on `(order_id, created_at desc, id desc)` and `(created_by, created_at desc, id desc)`. No foreign keys to other services.

Payments are a ledger. **There is no update or delete path for amount, currency, or order.** The store exposes only: insert, get, list, and a status transition. Add a database trigger or equivalent that rejects any `UPDATE` changing `amount_cents`, `currency`, `order_id`, or `created_by`, and rejects all `DELETE`s. Test it.

## Behaviour

Every method requires a valid token. The proto's `Payment` has no owner field, so ownership is `created_by` (stored, not returned).

### `ProcessPayment` (any authenticated user)

1. Validate: `order_id` is a UUID; `amount_cents` is `> 0`; `currency` is one of `USD`, `EUR`, `GBP` (upper-case, exact); `idempotency_key` is required, 1 to 128 printable ASCII chars. Otherwise `InvalidArgument`.
2. **Idempotency.** Insert inside one transaction using the `(created_by, idempotency_key)` unique key (`INSERT ... ON CONFLICT DO NOTHING`, then read back). If the key already exists:
   - same `order_id`, `amount_cents`, `currency`: return the **original payment** with OK status, no new row;
   - any parameter differs: `InvalidArgument` "idempotency key reused with different parameters".
   Concurrent calls with the same key must produce exactly one row. Test with goroutines against real Postgres.
3. **Simulated outcome**, decided inside the same transaction: `amount_cents <= PAYMENT_MAX_CENTS` gives `SUCCEEDED`; otherwise `FAILED` with `failure_reason = "amount exceeds limit"`. A declined payment is a **normal response containing a `FAILED` payment**, not a gRPC error.
4. Return the stored payment. The PENDING status exists in the enum but this service never leaves a row in it; say so in the README.

### `GetPayment` (owner or admin)
Someone else's payment returns `NotFound`.

### `ListPayments` (owner or admin)
Non-admin: always restricted to `created_by = caller`, with an optional `order_id` filter. Admin: all payments, optional `order_id` filter. Keyset pagination per prompt 00; filters come from the request on every page, not from the token.

### `RefundPayment` (admin only)
Non-admin: `PermissionDenied`. In one transaction: `SELECT ... FOR UPDATE` the row, require status `SUCCEEDED` (otherwise `FailedPrecondition`, including for an already-`REFUNDED` payment, so a repeat refund is rejected rather than silently repeated), set status `REFUNDED`, store `reason` in `refund_reason` (trimmed, at most 500 chars), bump `updated_at`. Unknown id: `NotFound`. Concurrent refunds of the same payment: exactly one succeeds. Test it.

Full refunds only; the contract has no partial amount.

## Required tests

- Service tests with a fake Store: validation table (bad UUID, zero and negative amount, bad currency, missing and oversized key); auth matrix for every RPC; declined-payment path returns `FAILED` not an error; state-machine table (`SUCCEEDED` to `REFUNDED` only).
- Store tests against real Postgres: idempotent replay returns the same payment; same key with different params rejected; **concurrent same-key calls create one row**; **concurrent refunds, one wins**; the immutability trigger; keyset pagination and filters.
- One in-process `bufconn` test through the real auth interceptor: customer pays, retries with the same key and gets the same id, cannot read another customer's payment, cannot refund; admin refunds; second refund fails.

## Also deliver

`README.md` (env vars, the simulated-processor rule, idempotency semantics, that PENDING is unused, and run and test instructions), and the `cmd/payments` wiring including the `healthcheck` subcommand.

### Dockerfile: `services/payments/Dockerfile`

Follow "Dockerfile (all services)" in prompt 00 exactly, with these specifics:
- Binary name `payments`, `EXPOSE 50053`, `ENTRYPOINT ["/payments"]`, `HEALTHCHECK CMD ["/payments", "healthcheck"]`.
- Build: `docker build -f services/payments/Dockerfile -t payments:dev .` from the repo root.
- Runtime env, none baked in: `DATABASE_URL`, `JWT_PUBLIC_KEY`. Non-secret `PAYMENT_MAX_CENTS` may have an image default of `1000000`. The image sets `APP_ENV=production`.
- **Its own database:** this service owns the `payments` database and never connects to any other service's database. The immutability trigger lives in a migration, so it is applied by the container itself on startup. The README shows a `docker run` pairing with a throwaway `postgres:17` container for a smoke test, and notes that the future compose file gives it a dedicated Postgres service and volume. That volume matters most here, since this is the ledger.

## Done when

`gofmt -l .` is empty; `go build ./... && go vet ./... && go test -short ./...` pass; `go test ./services/payments/...` passes with Docker; `docker build` succeeds and the image runs as non-root; against a throwaway Postgres container migrations (including the trigger) apply and the health check reports healthy; the container stops cleanly on `docker stop`; nothing under `proto/` or `gen/` changed.
