# microservices-app-go

Four Go gRPC services, a SvelteKit gateway in front of them, and one Postgres database per service. It's a small shop: users sign up, browse inventory, place orders and pay for them. The interesting part isn't the shop. It's the shape of the thing: separate services, separate databases, one shared token format and one public port.

| Service | Port | What it owns | README |
|---|---|---|---|
| `users` | 50051 | Accounts, passwords, JWT issuing, refresh tokens | [services/users](services/users/README.md) |
| `orders` | 50052 | Orders and their line items | [services/orders](services/orders/README.md) |
| `payments` | 50053 | An append-only payments ledger | [services/payments](services/payments/README.md) |
| `inventory` | 50054 | Stock items and quantities | [services/inventory](services/inventory/README.md) |
| `gateway` | 3000 | The web UI. The only thing exposed outside the compose network | [services/gateway](services/gateway/README.md) |

The Go services talk gRPC with the gateway, not with each other. The gateway, a SvelteKit server, calls them on behalf of the browser, so the browser never speaks gRPC and the backends are never reachable directly. Whether that's a security boundary or a very polite suggestion depends on your network, so see [architecture](docs/architecture.md) before relying on it.

## Quick start

You need Docker, Go (the version in `go.mod`) and a few minutes.

```sh
./start.sh
```

That generates a keypair into `.env` (only if there isn't one already; `FORCE_KEYS=1 ./start.sh` regenerates) and runs `docker compose up --build`. The manual route is `go run ./tools/keygen`, then putting `JWT_PRIVATE_KEY` and `JWT_PUBLIC_KEY` in `.env` yourself.

Then open <http://localhost:3000>. Compose refuses to start without both keys, which is the intended behaviour. Multi-line PEMs in `.env` files have their own opinions about quoting; [local development](docs/local-development.md) covers the details, plus running services individually and creating the first admin.

Be aware of two things:

- The `users` service only runs with the log-only mailer (`APP_ENV=development`), and compose sets that for you. Verification and reset tokens are written to its logs instead of being emailed. Real email delivery doesn't exist yet.
- Based on reading the code, the `orders` and `inventory` Docker health checks call the health RPC without a token and probably get rejected, so those containers likely report `unhealthy`. Nothing has been fixed. Each service README has the details.

## Repository layout

```
proto/            Protobuf contracts (the source of truth)
gen/go/           Generated Go bindings (don't edit)
internal/platform shared Go packages: authn, config, grpcx, pagination, pgxutil
services/         one directory per service, plus the gateway
tools/keygen/     Ed25519 keypair generator
prompts/          The build prompts the services were written from
docs/             Everything that didn't belong in a README
docker-compose.yml
```

Everything Go lives in a single module at the root. The gateway is its own npm project.

## Docs

- [Architecture](docs/architecture.md): how the pieces fit, what's shared, and the trade-offs.
- [Authentication and authorisation](docs/auth.md): tokens, keys, roles, sessions.
- [Local development](docs/local-development.md): running, testing, bootstrapping an admin.
- [Protobuf and code generation](docs/protobuf-and-codegen.md): changing a contract without breaking everything.

## Tests

```sh
go test ./...
```

Store tests start a real Postgres in a container, so they need Docker. They skip themselves under `-short`, or when Docker or the container can't be started, so a green run without Docker proves less than it appears to. The service-level tests use fakes or in-memory gRPC and run anywhere. The gateway has a single unit test file; see its README for how to run it.
