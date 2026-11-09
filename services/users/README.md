# users service

The identity provider. It implements `user.v1.UserService` and `user.v1.AuthService` (`proto/user/v1/user.proto`, generated package `userv1`), stores users and argon2id password hashes, signs EdDSA JWT access tokens and manages rotating refresh tokens. Every other service only *verifies* the tokens issued here; this is the only one that holds the private key.

It listens on `:50051` and owns its own Postgres database (`users`). Nothing else connects to that database.

For how tokens are consumed elsewhere, see [auth](../../docs/auth.md). For the wider picture, see [architecture](../../docs/architecture.md).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `GRPC_ADDR` | `:50051` | |
| `DATABASE_URL` | required | |
| `APP_ENV` | `development` | `development` or `production`. gRPC reflection is off in production |
| `JWT_PRIVATE_KEY` | required | PKCS#8 Ed25519 PEM. The public key is derived from it. Only this service holds the private key |
| `ACCESS_TOKEN_TTL` | `15m` | |
| `REFRESH_TOKEN_TTL` | `720h` | |
| `MAILER` | `log` | `log` is the only implementation. **The service refuses to start with `MAILER=log` and `APP_ENV=production`** |
| `BOOTSTRAP_ADMIN_EMAIL`, `BOOTSTRAP_ADMIN_PASSWORD` | unset | Set both or neither. Creates an admin at startup if none exists |

Because the only mailer is the log mailer, **the service cannot run in production mode yet**. That is deliberate: a silent no-op mailer would hide broken signup. Real delivery is out of scope.

Both TTLs must be positive, and `APP_ENV` must be exactly `development` or `production`. Anything else fails at startup.

### Keys

```sh
go run ./tools/keygen
```

Use the private PEM as `JWT_PRIVATE_KEY` here, and the public PEM as `JWT_PUBLIC_KEY` for the other services.

### Bootstrap admin

`BOOTSTRAP_ADMIN_*` creates a user with username `admin`, name `Administrator`, roles `admin` and `customer`, already verified and active. It only does so if no admin exists, and it takes an advisory lock so several replicas starting together don't each create one. A failure (a weak password, say) aborts startup. The log records that an admin was created, never the credentials.

## Running

Locally:

```sh
export APP_ENV=development
export DATABASE_URL='postgres://postgres:pw@localhost:5432/users?sslmode=disable'
export JWT_PRIVATE_KEY="$(cat private.pem)"
go run ./services/users/cmd/users
```

Migrations are embedded and applied on startup. The dev `LogMailer` writes verification and reset tokens to the JSON log (`verification_token`, `reset_token`), so you can finish flows with `grpcurl`. This is dev-only and is the one place a token is logged.

With reflection on (any `APP_ENV` other than `production`), `grpcurl` needs no `-proto` flag. See [local development](../../docs/local-development.md) for the full-stack route.

### Docker

The build context is the repo root:

```sh
docker build -f services/users/Dockerfile -t users:dev .
```

The image is distroless, runs as non-root and sets `APP_ENV=production`, so **local runs and compose must override it with `APP_ENV=development`** or the service exits with a clear error. `DATABASE_URL`, `JWT_PRIVATE_KEY` and `BOOTSTRAP_ADMIN_*` are runtime-only. The image's `HEALTHCHECK` runs `/users healthcheck`, which dials `127.0.0.1` on the port from `GRPC_ADDR` and exits 0 when the health service reports `SERVING`.

The root `docker-compose.yml` already does the overriding. It gives this service a dedicated `users-db` Postgres with its own volume, and no other service gets its credentials. Two things it doesn't do: it publishes no port for `users`, and it passes no `BOOTSTRAP_ADMIN_*`. Add either if you need them.

Smoke test with a throwaway Postgres:

```sh
docker network create users-net
docker run -d --name users-pg --network users-net \
  -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=users postgres:17
docker run -d --name users --network users-net -p 50051:50051 \
  -e APP_ENV=development \
  -e DATABASE_URL='postgres://postgres:pw@users-pg:5432/users?sslmode=disable' \
  -e JWT_PRIVATE_KEY="$(cat private.pem)" \
  users:dev
docker exec users /users healthcheck      # exit 0 when SERVING

grpcurl -plaintext -d '{"email":"a@example.com","username":"alice","name":"Alice","password":"a-long-password"}' \
  localhost:50051 user.v1.UserService/CreateUser
docker logs users | grep verification_token          # copy the token
grpcurl -plaintext -d '{"verification_token":"<token>"}' localhost:50051 user.v1.AuthService/VerifyEmail
grpcurl -plaintext -d '{"identifier":"alice","password":"a-long-password"}' localhost:50051 user.v1.AuthService/Login
```

## API surface

Authentication is default-deny. The authn interceptor rejects any method that isn't in `service.PublicMethods()`, and for everything else it requires `authorization: Bearer <access token>` metadata. Handlers then apply their own rules on top. Request and response shapes live in the [proto file](../../proto/user/v1/user.proto); see [protobuf and codegen](../../docs/protobuf-and-codegen.md) for regenerating the bindings.

### UserService

| RPC | Auth | Rule and notable errors |
|---|---|---|
| `CreateUser` | public | Self-signup. Roles and status are set by the server: `customer`, `PENDING_VERIFICATION`. `InvalidArgument` on bad input; `AlreadyExists` for a taken email or username |
| `GetUser` | token | Self or admin. Someone else's ID returns `NotFound`, not `PermissionDenied`, so IDs can't be probed |
| `UpdateUser` | token | Self or admin. `update_mask` is required and may contain only `name` and `username`; anything else is `InvalidArgument`. Mask validation happens *before* the authorisation check. `AlreadyExists` for a taken username |
| `DeleteUser` | token | Self or admin. Hard delete, cascading to the user's tokens. Refuses to delete the last admin (`FailedPrecondition`) |
| `ListUsers` | token | Admin only (`PermissionDenied` otherwise). Keyset pagination on `(created_at, id)`; page size defaults to 50, capped at 200. A malformed `page_token` is `InvalidArgument` |

### AuthService

| RPC | Auth | Rule and notable errors |
|---|---|---|
| `Login` | public | `identifier` is an email (if it contains `@`) or a username. Every credential failure is the same `Unauthenticated`. Status is only checked after the password is correct: `FailedPrecondition` for unverified email, `PermissionDenied` for suspended |
| `RefreshToken` | public | Rotates the refresh token. Any problem is `Unauthenticated` ("invalid refresh token") |
| `Logout` | public | Revokes the presented token's whole family. Idempotent: unknown or already-revoked tokens succeed |
| `ChangePassword` | token | `sub` must equal `user_id`. **Admins are not exempt.** Wrong current password is `PermissionDenied` and counts towards lockout. Revokes all the user's refresh tokens |
| `RequestPasswordReset` | public | Always returns the same empty success. See below |
| `ResetPassword` | public | Consumes a reset token. `InvalidArgument` for an unknown, expired or used token. Clears lockout and revokes all refresh tokens |
| `VerifyEmail` | public | Consumes a verification token, activates a pending user and returns the user. Never un-suspends |

`/grpc.health.v1.Health/Check` is also in the public set. See [Platform issues](#platform-issues-found-worked-around-not-fixed) for why.

### Validation

- **Email**: trimmed, lower-cased, at most 254 characters, must parse as a bare address (no display name).
- **Username**: trimmed, lower-cased, `^[a-z0-9_]{3,32}$`.
- **Name**: trimmed, 1 to 100 characters, no control characters.
- **Password**: 12 to 128 *bytes*, and not equal to the email or username (case-insensitively). That is the whole policy.

## Tokens

**Access tokens** are JWTs signed with EdDSA, carrying `sub` (the user ID), `roles`, and the issuer and audience constants from `internal/platform/authn` (`microservices-app/users` and `microservices-app`). They are stateless, so they can't be revoked: they remain valid for up to `ACCESS_TOKEN_TTL` after logout, suspension or a role change.

**Refresh tokens** are 32 random bytes, base64url-encoded. Only the SHA-256 of each is stored, so a database leak doesn't hand out live sessions. Verification and reset tokens are generated and stored the same way.

Rotation works like this:

1. `Login` issues a token pair and starts a new *family* (a UUID shared by every token descended from that login).
2. `RefreshToken` locks the presented token's row, revokes it, inserts a successor in the same family and returns a fresh pair, all in one transaction. Concurrent attempts on the same token are serialised by the row lock: one wins, and the others find a revoked token and are treated as reuse (step 4).
3. It also re-reads the user, so the new access token carries current roles, and it requires the user to be `ACTIVE`. A suspended user's refresh fails (the token itself is left unrevoked).
4. Presenting a token that has *already been revoked* is treated as theft: the whole family is revoked. That write commits even though the call returns `Unauthenticated`, and a warning is logged.
5. Unknown and expired tokens fail with the same error as everything else.

A consequence of step 4 worth knowing: a client that loses the response to a successful refresh and retries with the old token will kill its own session. Clients should store the new pair before doing anything else.

`ChangePassword` and `ResetPassword` revoke every refresh token the user has.

## Password hashing

Argon2id via `golang.org/x/crypto/argon2`, 16-byte random salt, 32-byte key, stored as a self-describing PHC string (`$argon2id$v=19$m=..,t=..,p=..$salt$key`). Production cost is 64 MiB, 3 iterations, parallelism 2. A deliberately cheap profile exists for tests only.

`Verify` reads the parameters from the stored hash, so hashes created under older costs keep working, and it compares in constant time. It also bounds what it will accept from a stored hash (at most 1 GiB, 16 iterations, parallelism 64), so a corrupted row can't make a login allocate without limit. I found no code that re-hashes on login, so raising the default cost only affects new hashes.

Unknown login identifiers are verified against a precomputed dummy hash, so they cost one argon2id run, just like a wrong password. It's 64 MiB of memory spent on proving that nobody is there, which is the price of not telling anyone.

## Data model

Three tables, created by `migrations/00001_init.sql` (goose, embedded in the binary and applied on startup):

- **`users`**: `id` (uuid), `email`, `username`, `name`, `password_hash`, `status` (1 pending verification, 2 active, 3 suspended; enforced by a `CHECK`), `email_verified`, `roles` (`text[]`), timestamps, `last_login_at`, `failed_login_count`, `locked_until`. Email and username are each unique on `lower(...)`, and there is an index on `(created_at, id)` for listing.
- **`refresh_tokens`**: `id`, `user_id`, `family_id`, `token_hash` (unique), `expires_at`, `revoked_at`, `created_at`.
- **`one_time_tokens`**: `id`, `user_id`, `kind` (`reset` or `verify`), `token_hash` (unique), `expires_at`, `used_at`, `created_at`.

Both token tables cascade on user delete. Verification tokens live for 24 hours, reset tokens for one hour; issuing a new reset token invalidates any earlier unused one.

## Testing

```sh
gofmt -l services/users && go build ./services/users/... && go vet ./services/users/...
go test -short ./services/users/...   # no Docker: store tests skip
go test ./services/users/...          # needs Docker for the store tests
```

- `internal/service`: a fake `Store` and cheap argon2 parameters. Covers the authorisation matrix, every `update_mask` rejection, lockout, refresh rotation and reuse, logout idempotence, password change/reset, enumeration resistance, last-admin delete, bootstrap, the full mail queue not blocking, and `VerifyEmail` not un-suspending. `e2e_test.go` is the in-process `bufconn` test through the real authn interceptor: signup, verify, login, authenticated read, refresh, logout.
- `internal/store`: real Postgres via `pgxtest`. Case-insensitive uniqueness, keyset ordering and boundaries, transactional and concurrent refresh rotation, cascade delete, last-admin protection, concurrent bootstrap, one-time tokens.
- `internal/password` and `internal/config` have their own small unit tests (hash round trips, salting, embedded parameters, malformed hashes; config validation).

Podman users: testcontainers' Ryuk reaper can fail with "network bridge not found". Run with `TESTCONTAINERS_RYUK_DISABLED=true`.

## Behaviour notes and trade-offs

- **Lockout is a denial-of-service lever.** Five wrong passwords lock an account for 15 minutes, so anyone who knows an email or username can keep a victim locked out. Accepted: it stops online guessing. A failed `ChangePassword` current-password check also counts. A successful login, password change or password reset clears the counter. A lock that has expired restarts the count at 1.
- A locked account cannot be unlocked by a correct password until `locked_until` passes, but a password reset clears the lock. While locked, `Login` returns the generic error *without* running argon2id, so a locked account answers measurably faster than a wrong password. That makes a locked account's existence observable by timing.
- **Signup reveals which emails and usernames exist** (`AlreadyExists`). Accepted for signup UX over enumeration resistance. Login and `RequestPasswordReset` do *not* reveal existence: same error or response, and unknown users still pay for one argon2id verification.
- `RequestPasswordReset` does all of its lookup, token and mail work on a bounded background queue (64 jobs, dropped with a warning when full), so the response and its timing do not depend on whether the email exists. Queued jobs, including verification emails from `CreateUser`, are lost if the process dies before they run. Suspended users get no reset token.
- Role and status changes have no RPC; they are database-only.
- Passwords are limited to 128 *bytes*, so very long non-ASCII passphrases fit fewer characters.

## Known limitations

- **No way to resend a verification email.** There is no such RPC, and verification tokens expire after 24 hours. A user who misses the window stays `PENDING_VERIFICATION`; a password reset changes their password but doesn't verify them.
- **Expired and used token rows are never purged.** Nothing deletes from `refresh_tokens` or `one_time_tokens` except the cascade on user delete.
- **Memory under load.** Each hash or verify allocates 64 MiB and nothing limits concurrency, so a burst of simultaneous logins costs roughly 64 MiB each. No rate limiting exists in this service.
- **A deleted or suspended user's access token keeps working** until it expires, as described under Tokens. `ChangePassword` also doesn't check account status, so a suspended user holding a live access token can still change their password.
- **Production mode is unreachable** until a real mailer exists (see Configuration).

## Platform issues found (worked around, not fixed)

1. **`grpcx` authenticates the unary `grpc.health.v1.Health/Check` call.** It installs the authn interceptor on all unary RPCs, so the `healthcheck` subcommand got `Unauthenticated`. This service lists `/grpc.health.v1.Health/Check` in its public methods (`service.PublicMethods`). Other services using `grpcx` with a verifier will hit the same thing: their Docker `HEALTHCHECK` will fail unless they do likewise, or `grpcx` adds it to the public set itself.
2. **`golang.org/x/crypto` is `// indirect` in `go.mod`.** This service imports `golang.org/x/crypto/argon2` directly. It builds, but `go mod tidy` should promote it to a direct requirement.
3. `pgxtest` pins `postgres:16-alpine`, while compose and the smoke test above use Postgres 17. The migrations are version-agnostic.
