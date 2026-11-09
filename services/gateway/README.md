# Gateway

A SvelteKit app (Svelte 5, Tailwind 4, `adapter-node`) that serves the web UI and talks to the four Go services over gRPC. It's the only thing in `docker-compose.yml` with a published port (`3000:3000`); the Go services and their databases are reachable only on the compose network.

There is no REST layer behind it, and no browser-side API client. Every page load and form action runs on the server, calls a backend over gRPC, and sends HTML back. The browser never sees an access token, a gRPC address or, mercifully, a protobuf.

See also: [architecture](../../docs/architecture.md), [auth](../../docs/auth.md), [local development](../../docs/local-development.md), [protobuf and codegen](../../docs/protobuf-and-codegen.md).

## How it talks to the services

- **Clients** live in `src/lib/server/clients.ts`. `clients(accessToken?)` builds Connect clients (`@connectrpc/connect-node`, gRPC transport over HTTP/2) for `AuthService`, `UserService` (both on the users service), `OrdersService`, `PaymentsService` and `InventoryService`. Routes use `api(locals)`, which passes the session's access token; the pre-login pages use `clients()` with none.
- **Auth to the backends**: when a token is present, an interceptor adds `authorization: Bearer <token>`. The gateway doesn't authorise anything itself; each backend verifies the JWT.
- **Connections**: one `Http2SessionManager` per backend URL, shared across requests. Each call has a 5 second deadline (`defaultTimeoutMs`).
- **Errors** (`src/lib/server/rpc.ts`): gRPC codes map to HTTP statuses (`InvalidArgument` 400, `Unauthenticated` 401, `PermissionDenied` 403, `NotFound` 404, `AlreadyExists`/`FailedPrecondition` 409, `Unavailable` 503, `DeadlineExceeded` 504, anything else 500). Loads turn them into an error page; actions turn them into a form message. `Unauthenticated` in either case redirects to `/login`.
- **Generated code** is in `src/lib/gen` (`user`, `orders`, `payments`, `inventory`) and is committed. It comes from `proto/buf.gen.yaml`, whose third plugin is `buf.build/bufbuild/es` with `target=ts`, writing to `services/gateway/src/lib/gen`. Don't edit it by hand; see [protobuf and codegen](../../docs/protobuf-and-codegen.md) for how to regenerate. (The `buf.build` plugins are remote, so regenerating needs network access.)

## Sessions and the auth hook

Login calls `AuthService.Login` and stores the returned tokens in two cookies:

| Cookie | Holds |
| --- | --- |
| `ob_access` | access token (JWT) |
| `ob_refresh` | refresh token |

Both are `httpOnly`, `sameSite=lax`, `path=/`, and expire when the corresponding token does. SvelteKit adds `secure` to cookies by default everywhere except dev mode and `http://localhost`, and `setTokens` doesn't override that.

`src/hooks.server.ts` runs on every request:

1. `resolveSession` (`src/lib/server/session.ts`) reads `ob_access`. If it parses and has more than 30 seconds left, that's the session (`userId`, `roles`, `isAdmin`, `accessToken`) and it lands in `event.locals.session`.
2. Otherwise, if there's an `ob_refresh`, it calls `AuthService.RefreshToken`, sets new cookies and uses the new access token. Concurrent requests with the same refresh token share one in-flight call, because the refresh tokens rotate and reusing a spent one revokes the whole family (per the comment in the code; that behaviour lives in the users service). The cookies are cleared only on `Unauthenticated`, so a backend outage doesn't sign everybody out.
3. No session and not a public path: `303` to `/login?next=<path>`. Public paths are `/login`, `/signup`, `/verify`, `/forgot`, `/reset`. `?next=` is accepted only if it's a same-site path.

The gateway decodes the JWT claims (`sub`, `exp`, `roles`) **without verifying the signature**. That is deliberate: the claims only decide what the UI shows, and every backend checks the token for real. A forged cookie buys you a different-looking page and nothing else.

The `(auth)` pages redirect to `/` if you're already signed in. Admin-only UI (`/users`, the Users link, order filtering by customer) keys off the `admin` role.

## Routes

All `(app)` routes require a session. Each route that touches a backend does so in `+page.server.ts`.

| Route | Loads | Actions |
| --- | --- | --- |
| `/` | `ListOrders` (5), `ListPayments` (5), `ListInventoryItems` (200), and `ListUsers` (5) for admins or `GetUser` for everyone else. Each is settled independently, so one dead service shows a message rather than a blank page. | none |
| `/orders` | `ListOrders` (25 per page; admins may filter with `?user=`) | `create` → `CreateOrder` |
| `/orders/[id]` | `GetOrder` + `ListPayments` for that order | `pay` → `ProcessPayment`; `update` → `UpdateOrder`; `delete` → `DeleteOrder` |
| `/payments` | `ListPayments` (25; `?order=` filter) | none |
| `/payments/[id]` | `GetPayment` | `refund` → `RefundPayment` |
| `/inventory` | `ListInventoryItems` (50) | `create` → `CreateInventoryItem` |
| `/inventory/[id]` | `GetInventoryItem` | `update` → `UpdateInventoryItem`; `delete` → `DeleteInventoryItem` |
| `/users` (admin only) | `ListUsers` (50) | none |
| `/users/[id]` (admin only) | `GetUser` + `ListOrders` for that user; redirects to `/account` for your own id | `profile` → `UpdateUser`; `delete` → `DeleteUser` |
| `/account` | `GetUser` (you) | `profile` → `UpdateUser`; `password` → `ChangePassword`; `delete` → `DeleteUser` |
| `/login` | none | `AuthService.Login` |
| `/signup` | none | `CreateUser` |
| `/verify` | none (token from `?token=`) | `VerifyEmail` |
| `/forgot` | none | `RequestPasswordReset` |
| `/reset` | none (token from `?token=`) | `ResetPassword` |
| `/logout` | none (no page; POST only) | `Logout`, then clears cookies |

Things worth knowing:

- Lists paginate with an opaque `?page=` token passed straight through as `pageToken`.
- Payment amounts are `bigint` cents. `parseCents` (`src/lib/format.ts`) accepts things like `24.99` or `12,34` and rejects negatives, more than two decimal places and anything over ten digits before the point.
- `/orders/[id]` generates a fresh idempotency key per page load, so re-submitting the same form can't charge twice. Reloading the page gets you a new key, so a reload followed by a second submit *can*.
- Profile updates send a field mask containing only the fields that changed.
- A password change or account deletion clears the cookies locally. The code notes that the users service revokes all refresh tokens on a password change.
- The gateway enforces a 12-character minimum on new passwords (signup, reset, change) before it calls the backend.

## Configuration

Defined in `src/env.ts` and read through `$app/env/private`, so they're server-only.

| Variable | Default | In `docker-compose.yml` |
| --- | --- | --- |
| `USERS_SERVICE_URL` | `http://localhost:50051` | `http://users:50051` |
| `ORDERS_SERVICE_URL` | `http://localhost:50052` | `http://orders:50052` |
| `PAYMENTS_SERVICE_URL` | `http://localhost:50053` | `http://payments:50053` |
| `INVENTORY_SERVICE_URL` | `http://localhost:50054` | `http://inventory:50054` |

The Dockerfile also sets `NODE_ENV=production` and `PORT=3000`. The gateway needs no JWT keys, because it never verifies a token. `.env*` files are git-ignored and excluded from the Docker build context.

The backend URLs are `http://`, so the gateway speaks plaintext HTTP/2 to the services. That's fine on the compose network, and something to revisit before it's anywhere else.

## Running it

Whole stack, from the repo root (needs `JWT_PRIVATE_KEY` and `JWT_PUBLIC_KEY`; the header of `docker-compose.yml` says to generate them with `go run ./tools/keygen`):

```sh
docker compose up --build
```

Then open http://localhost:3000.

The gateway image is a two-stage build on `node:24-alpine`: `npm ci`, `npm run build`, prune dev dependencies, then `node build` as the `node` user.

For development, run the backends however you normally do (see [local development](../../docs/local-development.md)), then:

```sh
cd services/gateway
npm ci
npm run dev
```

With the defaults above it expects the services on `localhost:50051`–`50054`. Override the `*_SERVICE_URL` variables if yours are elsewhere. The dev server uses Vite's default port, since nothing here configures one. `.npmrc` sets `engine-strict=true`; the Dockerfile uses Node 24, and that's what I'd use locally.

### Scripts

| Script | What it does |
| --- | --- |
| `npm run dev` | `vite dev` |
| `npm run build` | `vite build`, output in `build/` |
| `npm run preview` | `vite preview` |
| `npm run check` | `svelte-kit sync` then `svelte-check` |
| `npm run check:watch` | the same, in watch mode |
| `npm run lint` | `prettier --check .` then `eslint .` |
| `npm run format` | `prettier --write .` |

## Testing

There is one test file, `src/lib/format.test.ts`, covering `parseCents`, `formatCents` and `centsInput` with Node's built-in runner. There's no `test` script, so run it directly (Node 24 strips the TypeScript types itself):

```sh
node --test src/lib/format.test.ts
```

Type-checking is `npm run check`; style is `npm run lint`.

## Known limitations

- **Almost nothing is tested.** The session refresh logic, the auth hook and every route action have no automated tests. The refresh-token deduplication in particular is exactly the sort of thing that works until it doesn't.
- **Refresh deduplication is per process.** `inflight` is an in-memory map; with several gateway replicas, parallel requests landing on different replicas can each present the same refresh token. The code's own note suggests sticky sessions or a shared store.
- **Plaintext to the backends**, as above.
- **Idempotency keys are per page view**, not per intended payment.
- **Hard-coded page sizes**, and the overview loads up to 200 inventory items to summarise them.
- **No health endpoint** in the gateway, and the compose `depends_on` only waits for the backend containers to start, not for them to be ready.
- Behind a reverse proxy over plain HTTP on a non-localhost host, the `secure` cookies won't stick. Terminate TLS in front of it.
