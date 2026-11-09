# Build prompt 04: inventory service (InventoryService)

**Prerequisite:** prompt `00-platform-and-conventions.md` has been completed. Read it first. Its conventions apply here without being repeated.

## Goal

Build `services/inventory/`, a Go gRPC service implementing `inventory.v1.InventoryService` from `proto/inventory/v1/inventory.proto` (generated package `inventoryv1` at `github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1`). Port `:50054`, its own Postgres database.

## Contract limits you must work within

`InventoryItem` has only `id`, `sku` and `quantity`. `UpdateInventoryItemRequest` is a full replace with no field mask, and there is **no atomic adjust or reserve RPC.** Build exactly that. Do not edit the proto.

Consequence to document in the README under "Contract gaps": because `UpdateInventoryItem` sets an absolute `quantity`, two concurrent callers doing read-modify-write can lose an update. This service cannot fix that on its own. Recommend adding `AdjustQuantity(id, delta)` (atomic `quantity = quantity + $delta`, failing if the result would be negative) when orders start reserving stock. Do not build it now.

## Config

`GRPC_ADDR` (`:50054`), `DATABASE_URL` (required), `APP_ENV`, `JWT_PUBLIC_KEY` (PEM, required).

## Data model

`inventory_items`: `id uuid pk`, `sku text not null`, `quantity integer not null check (quantity >= 0)`, `created_at`, `updated_at`. Unique index on `sku`. Index on `(created_at desc, id desc)`.

## Behaviour

Every method requires a valid token. Inventory is shared catalogue data, not per-user data, so reads are open to any authenticated user and writes are admin-only.

| RPC | Who | Rules |
|---|---|---|
| `CreateInventoryItem` | admin only | Service generates the `id`. Normalise SKU: trim and upper-case; it must match `^[A-Z0-9][A-Z0-9._-]{0,63}$`. `quantity` must be `>= 0`. Duplicate SKU: `AlreadyExists`. |
| `GetInventoryItem` | any authenticated user | Unknown id: `NotFound`. |
| `UpdateInventoryItem` | admin only | Full replace of `sku` and `quantity` with the same validation. Changing to an SKU held by another item: `AlreadyExists`. Unknown id: `NotFound`. Bump `updated_at`. |
| `DeleteInventoryItem` | admin only | Unknown id: `NotFound`. |
| `ListInventoryItems` | any authenticated user | Keyset pagination per prompt 00. |

Non-admin write attempts return `PermissionDenied` (there is no ownership to hide, so no `NotFound` masking here).

## Required tests

- Service tests with a fake Store: auth matrix for every RPC; SKU normalisation and rejection table (empty, too long, bad characters, lower-case input accepted and upper-cased); negative quantity; invalid UUIDs; duplicate-SKU mapping.
- Store tests against real Postgres: CRUD, the unique SKU constraint (including SKUs differing only in case at input time), the `quantity >= 0` check, keyset pagination across ties on `created_at`.
- One in-process `bufconn` test through the real auth interceptor: admin creates, a customer reads and lists, a customer's write is denied, no token is `Unauthenticated`.

## Also deliver

`README.md` (env vars, the "Contract gaps" section, run and test instructions), and the `cmd/inventory` wiring including the `healthcheck` subcommand.

### Dockerfile: `services/inventory/Dockerfile`

Follow "Dockerfile (all services)" in prompt 00 exactly, with these specifics:
- Binary name `inventory`, `EXPOSE 50054`, `ENTRYPOINT ["/inventory"]`, `HEALTHCHECK CMD ["/inventory", "healthcheck"]`.
- Build: `docker build -f services/inventory/Dockerfile -t inventory:dev .` from the repo root.
- Runtime env, none baked in: `DATABASE_URL`, `JWT_PUBLIC_KEY`. The image sets `APP_ENV=production`.
- **Its own database:** this service owns the `inventory` database and never connects to any other service's database. The README shows a `docker run` pairing with a throwaway `postgres:17` container for a smoke test, and notes that the future compose file gives it a dedicated Postgres service and volume.

## Done when

`gofmt -l .` is empty; `go build ./... && go vet ./... && go test -short ./...` pass; `go test ./services/inventory/...` passes with Docker; `docker build` succeeds and the image runs as non-root; against a throwaway Postgres container migrations apply and the health check reports healthy; the container stops cleanly on `docker stop`; nothing under `proto/` or `gen/` changed.
