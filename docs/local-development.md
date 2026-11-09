# Local development

Three ways to run this, in ascending order of effort: the whole stack in Docker Compose, individual services with `go run`, or just the gateway against whatever's running. Tests are a separate section, because "needs Docker" is a more nuanced statement than it first appears.

## Prerequisites

| Tool | Needed for | Evidence |
|---|---|---|
| Go 1.27.2 or later | Building and testing the services, running `keygen` | `go.mod` says `go 1.27.2`; Dockerfiles build on `golang:1.27` |
| Docker with Compose | Running the stack; Postgres for individual services; the Postgres-backed tests | `docker-compose.yml` |
| Node 24 | Gateway dev, build and checks | gateway Dockerfile uses `node:24-alpine`; `.npmrc` sets `engine-strict=true` |
| [buf](https://buf.build/docs/installation) | Only if you're changing protos | [protobuf-and-codegen.md](protobuf-and-codegen.md) |

## Generate keys

```sh
go run ./tools/keygen
```

It prints two labelled PEM blocks: a private key (for the users service only) and a public key (for everything that verifies tokens). See [auth.md](auth.md) for what each is used for. The private key is a signing secret; treat it like one and keep it out of version control. The repository has no root `.gitignore`, so a `.env` file at the root will not be ignored.

## Run everything with Docker Compose

`docker-compose.yml` refuses to start unless `JWT_PRIVATE_KEY`, `JWT_PUBLIC_KEY`, `SERVICE_PRIVATE_KEY` and `SERVICE_PUBLIC_KEY` are set, in the shell environment or a `.env` file next to the compose file. Those are the only variables it requires from you. `./start.sh` generates all four; the service pair comes from `go run ./tools/keygen service` and is added to an existing `.env` if missing.

A multi-line PEM needs to go in a `.env` file inside double quotes. The keygen output has headings mixed in with the PEM, so you can't just redirect it into `.env`. This does the splitting:

```sh
go run ./tools/keygen > /tmp/keys.txt
{
  printf 'JWT_PRIVATE_KEY="%s"\n' "$(sed -n '/BEGIN PRIVATE KEY/,/END PRIVATE KEY/p' /tmp/keys.txt)"
  printf 'JWT_PUBLIC_KEY="%s"\n'  "$(sed -n '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/p' /tmp/keys.txt)"
} > .env
rm /tmp/keys.txt
```

Then:

```sh
docker compose up --build
```

What you get:

| Container | Notes |
|---|---|
| `users-db`, `orders-db`, `payments-db`, `inventory-db` | `postgres:17-alpine`, each with a named volume. No published ports. User/password is `postgres`/`postgres`; this is for local use only. |
| `users` | Gets `JWT_PRIVATE_KEY`, and `APP_ENV=development` (the image defaults to `production`, which the users service rejects because of the log-only mailer). |
| `orders`, `payments`, `inventory` | Get `JWT_PUBLIC_KEY`. Orders also gets `SERVICE_PRIVATE_KEY` and `INVENTORY_ADDR`; inventory gets `SERVICE_PUBLIC_KEY`. They run with the image default `APP_ENV=production`, which only affects whether gRPC reflection is registered. |
| `inventory-seed` | One-shot `psql` run of `services/inventory/seed/dev-seed.sql` once inventory is healthy (ten sample SKUs with prices). Existing SKUs are left alone. Development only; it isn't a migration. |
| `gateway` | Published on **http://localhost:3000**. Reaches the services via `http://users:50051`, `http://orders:50052`, `http://payments:50053`, `http://inventory:50054`. |

Only the gateway publishes a port. If you want to poke a backend directly from your machine, add a `ports:` entry to that service (via an override file, below).

Services wait for their own database to be healthy, and retry the connection and migrations for up to 30 seconds anyway. Migrations run automatically on startup; nothing else to do.

Services wait on their databases' health; orders also waits for inventory to start, and the seed waits for inventory to be healthy. The `gateway` simply `depends_on` the four services starting.

Stop with `docker compose down`. To also delete the databases, add `-v`. That removes every user, order, payment and inventory item in the stack, so don't do it casually.

### Seeing the "emails"

There's no email delivery. In `APP_ENV=development` the users service logs the verification and reset tokens instead:

```sh
docker compose logs users | grep 'DEV MAILER'
```

Each line is JSON containing `to` and either `verification_token` or `reset_token`. The gateway's `/verify` and `/reset` pages accept the token as `?token=…` or pasted into the form.

### Bootstrapping an admin

New sign-ups are always `customer`, and there is no API to promote anyone. The only way to get an admin is to start the users service with both of these set:

- `BOOTSTRAP_ADMIN_EMAIL`
- `BOOTSTRAP_ADMIN_PASSWORD` (12 to 128 bytes, and not equal to the email or `admin`)

Setting only one is a startup error. Compose doesn't pass either, so add them with an override file. Compose merges `docker-compose.override.yml` automatically:

```yaml
# docker-compose.override.yml
services:
  users:
    environment:
      BOOTSTRAP_ADMIN_EMAIL: admin@example.com
      BOOTSTRAP_ADMIN_PASSWORD: change-me-please-123
```

```sh
docker compose up -d --build users
```

The created user has username `admin`, name `Administrator`, roles `admin` and `customer`, and is already active and verified. You can log in at `http://localhost:3000/login` with either `admin` or the email. Behaviour to know about:

- It runs on every users-service startup, but only creates a user if **no admin exists**. Changing the password in the env later does nothing once an admin is there.
- It is safe with several replicas starting together (advisory lock).
- A password that fails the policy aborts startup.
- Once the admin exists, remove the override. The password is otherwise sitting in your compose config.

## Run services individually

Useful for debugging a single service with a debugger or `go run`. Each service needs its own Postgres database. One container with four databases is enough locally (compose uses four containers, but nothing in the code requires that):

```sh
docker run -d --name dev-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:17-alpine
for db in users orders payments inventory; do
  docker exec dev-pg createdb -U postgres "$db"
done
```

(The Postgres entrypoint may need a few seconds before `createdb` works. Run it again if it complains.)

Set up the keys as shell variables, then start each service in its own terminal:

```sh
go run ./tools/keygen > /tmp/keys.txt
export JWT_PRIVATE_KEY="$(sed -n '/BEGIN PRIVATE KEY/,/END PRIVATE KEY/p' /tmp/keys.txt)"
export JWT_PUBLIC_KEY="$(sed -n '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/p' /tmp/keys.txt)"

# orders -> inventory service tokens
go run ./tools/keygen service > /tmp/svc-keys.txt
export SERVICE_PRIVATE_KEY="$(sed -n '/BEGIN PRIVATE KEY/,/END PRIVATE KEY/p' /tmp/svc-keys.txt)"
export SERVICE_PUBLIC_KEY="$(sed -n '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/p' /tmp/svc-keys.txt)"
```

```sh
# users: needs the private key; APP_ENV defaults to "development" when run outside the image
DATABASE_URL='postgres://postgres:postgres@localhost:5432/users?sslmode=disable' \
  go run ./services/users/cmd/users

# orders also needs SERVICE_PRIVATE_KEY (and INVENTORY_ADDR, default localhost:50054); inventory needs SERVICE_PUBLIC_KEY
DATABASE_URL='postgres://postgres:postgres@localhost:5432/orders?sslmode=disable' \
  go run ./services/orders/cmd/orders
DATABASE_URL='postgres://postgres:postgres@localhost:5432/payments?sslmode=disable' \
  go run ./services/payments/cmd/payments
DATABASE_URL='postgres://postgres:postgres@localhost:5432/inventory?sslmode=disable' \
  go run ./services/inventory/cmd/inventory
```

Default listen addresses are `:50051` (users), `:50052` (orders), `:50053` (payments) and `:50054` (inventory). Override with `GRPC_ADDR`. Other variables (token lifetimes, `PAYMENT_MAX_CENTS`, and so on) are in each service's README: [users](../services/users/README.md), [orders](../services/orders/README.md), [payments](../services/payments/README.md), [inventory](../services/inventory/README.md).

Outside the production setting, gRPC reflection is registered, so a reflection-aware client such as `grpcurl` can list and call methods. Remember that everything except a handful of public users methods needs a `Bearer` token.

## Tests

### Go

```sh
go test -short ./...      # no Docker required
go test ./...             # also runs the Postgres-backed tests
```

I ran `go test -short ./...` on this repo with the toolchain above and every package passed.

What needs Postgres: the `internal/store` tests of all four services and `internal/platform/pgxutil`. They call `pgxtest.StartPostgres`, which starts a throwaway `postgres:16-alpine` container through testcontainers (60 s setup timeout). It **skips** the test under `-short`, and it also skips (rather than fails) if the container can't be started, for example when Docker isn't running. So a green `go test ./...` on a machine without Docker proves less than it appears to; look for `SKIP` in `go test -v` output if you care.

What doesn't: everything else. Service-layer tests use in-memory fake stores and an in-process gRPC server over `bufconn`; `authn`, `config`, `grpcx`, `pagination`, and the users service's password and config packages are plain unit tests.

Run one service's tests with, for instance, `go test ./services/orders/...`.

### Gateway

```sh
cd services/gateway
npm ci
npm run dev        # Vite dev server (default port 5173)
npm run check      # svelte-kit sync + svelte-check
npm run lint       # prettier --check + eslint
npm run build      # adapter-node build into build/
node --test src/lib/format.test.ts   # the one unit test file
```

There is no `npm test` script; `format.test.ts` uses Node's built-in test runner directly (Node 24 can execute the TypeScript as is, which is why the Node version matters here).

The dev server needs the backends. Without configuration it talks to `http://localhost:50051` to `50054` (the defaults in `src/env.ts`), which matches running the services individually as above. To point it at something else, set `USERS_SERVICE_URL`, `ORDERS_SERVICE_URL`, `PAYMENTS_SERVICE_URL` and `INVENTORY_SERVICE_URL`. These are server-side only and never sent to the browser.

If you're running the compose stack and want the dev server on top of it, the backends aren't published, so you'd need to add `ports:` to them first. Alternatively, just rebuild the gateway container: `docker compose up -d --build gateway`.

The production gateway image runs `node build` on port 3000.
