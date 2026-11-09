# users service

Identity provider. Implements `user.v1.UserService` and `user.v1.AuthService`
(`proto/user/v1/user.proto`, generated package `userv1`). It stores users and
argon2id password hashes, signs EdDSA JWT access tokens, and manages rotating
refresh tokens. Every other service only verifies the tokens issued here.

Port `:50051`. Owns its own Postgres database (`users`). Nothing else connects to it.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `GRPC_ADDR` | `:50051` | |
| `DATABASE_URL` | required | |
| `APP_ENV` | `development` | `development` or `production`. Reflection is off in production |
| `JWT_PRIVATE_KEY` | required | PKCS#8 Ed25519 PEM. The public key is derived from it. Only this service holds the private key |
| `ACCESS_TOKEN_TTL` | `15m` | |
| `REFRESH_TOKEN_TTL` | `720h` | |
| `MAILER` | `log` | `log` is the only implementation. **The service refuses to start with `MAILER=log` and `APP_ENV=production`** |
| `BOOTSTRAP_ADMIN_EMAIL`, `BOOTSTRAP_ADMIN_PASSWORD` | unset | Set both or neither. Creates an admin at startup if none exists |

Because the only mailer is the log mailer, **the service cannot run in
production mode yet**. That is deliberate: a silent no-op mailer would hide
broken signup. Real delivery is out of scope.

## Keys

```sh
go run ./tools/keygen
```

Use the private PEM as `JWT_PRIVATE_KEY` here, and the public PEM as
`JWT_PUBLIC_KEY` for the other services.

## Run locally

```sh
export APP_ENV=development
export DATABASE_URL='postgres://postgres:pw@localhost:5432/users?sslmode=disable'
export JWT_PRIVATE_KEY="$(cat private.pem)"
go run ./services/users/cmd/users
```

Migrations are embedded and applied on startup. The dev `LogMailer` writes
verification and reset tokens to the JSON log (`verification_token`,
`reset_token`) so you can finish flows with `grpcurl`. This is dev-only and is
the one place a token is logged.

## Docker

Build context is the repo root:

```sh
docker build -f services/users/Dockerfile -t users:dev .
```

The image sets `APP_ENV=production`, so **local runs and compose must override
it with `APP_ENV=development`** or the service exits with a clear error.
`DATABASE_URL`, `JWT_PRIVATE_KEY` and `BOOTSTRAP_ADMIN_*` are runtime-only.

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

The future compose file should give this service a dedicated Postgres service
and volume. No other service gets its credentials.

## Tests

```sh
gofmt -l services/users && go build ./services/users/... && go vet ./services/users/...
go test -short ./services/users/...   # no Docker: store tests skip
go test ./services/users/...          # needs Docker for the store tests
```

- `internal/service`: fake `Store`, cheap argon2 parameters. Authorisation
  matrix, every `update_mask` rejection, lockout, refresh rotation and reuse,
  logout idempotence, password change/reset, enumeration resistance,
  last-admin delete, `VerifyEmail` not un-suspending. `e2e_test.go` is the
  in-process `bufconn` test through the real authn interceptor.
- `internal/store`: real Postgres via `pgxtest`. Case-insensitive uniqueness,
  keyset ordering and boundaries, transactional and concurrent refresh
  rotation, cascade delete, last-admin protection.

Podman users: testcontainers' Ryuk reaper can fail with "network bridge not
found". Run with `TESTCONTAINERS_RYUK_DISABLED=true`.

## Behaviour notes and trade-offs

- **Lockout is a denial-of-service lever.** Five wrong passwords lock an
  account for 15 minutes, so anyone who knows an email or username can keep a
  victim locked out. Accepted: it stops online guessing. A failed
  `ChangePassword` current-password check also counts. A successful login,
  password change or password reset clears the counter. A lock that has
  expired restarts the count at 1.
- **Signup reveals which emails and usernames exist** (`AlreadyExists`).
  Accepted for signup UX over enumeration resistance. Login and
  `RequestPasswordReset` do *not* reveal existence: same error/response, and
  unknown users still pay for one argon2id verification.
- `RequestPasswordReset` does all of its lookup, token and mail work on a
  bounded background queue (64 jobs, drops with a warning when full), so the
  response and its timing do not depend on whether the email exists. Queued
  resets are lost if the process dies before they run.
- A locked account cannot be unlocked by a correct password until `locked_until`
  passes, but a password reset clears the lock.
- Refresh reuse detection: presenting an already-revoked refresh token revokes
  its whole family. Rotation and reuse revocation commit even though the call
  returns `Unauthenticated`. Rotating also re-reads roles and requires
  `ACTIVE`.
- Access tokens are stateless and cannot be revoked; they live up to
  `ACCESS_TOKEN_TTL` after logout, suspension or role change.
- Role and status changes have no RPC; they are database-only.
- `BOOTSTRAP_ADMIN_*` creates the admin with username `admin`, name
  `Administrator`. Failure to bootstrap (e.g. weak password) aborts startup.
- Passwords are limited to 128 *bytes*, so very long non-ASCII passphrases fit
  fewer characters.

## Platform issues found (worked around, not fixed)

1. **`grpcx` authenticates the unary `grpc.health.v1.Health/Check` call.** It
   installs the authn interceptor on all unary RPCs, so the `healthcheck`
   subcommand got `Unauthenticated`. This service lists
   `/grpc.health.v1.Health/Check` in its public methods
   (`service.PublicMethods`). Other services using `grpcx` with a verifier
   will hit the same thing: their Docker `HEALTHCHECK` will fail unless they
   do likewise, or `grpcx` adds it to the public set itself.
2. **`golang.org/x/crypto` is `// indirect` in `go.mod`.** This service imports
   `golang.org/x/crypto/argon2` directly. It builds because the module is
   present in `go.sum`, but `go mod tidy` should promote it to a direct
   requirement. Not done here because `go.mod` is read-only for this task.
3. `pgxtest` pins `postgres:16-alpine`; the Docker smoke test above uses
   `postgres:17` as the prompt asks. The migrations are version-agnostic.
