# Build prompt 01: users service (UserService + AuthService)

**Prerequisite:** prompt `00-platform-and-conventions.md` has been completed. Read it first. Its conventions (layout, error mapping, logging, pagination, testing) apply here without being repeated.

## Goal

Build `services/users/`, a Go gRPC service that implements **both** `user.v1.UserService` and `user.v1.AuthService` from `proto/user/v1/user.proto` (generated package `userv1` at `github.com/francispowlesland/microservices-app-go/gen/go/user/v1`). It is the identity provider: it stores users and password hashes, issues JWT access tokens, and manages refresh tokens. Every other service only verifies the tokens it issues.

Port `:50051`. Database: its own Postgres database.

## Config

`GRPC_ADDR` (`:50051`), `DATABASE_URL` (required), `APP_ENV`, `JWT_PRIVATE_KEY` (PEM, required; the public key is derived from it), `ACCESS_TOKEN_TTL` (default `15m`), `REFRESH_TOKEN_TTL` (default `720h`), `MAILER` (`log` only; see below), `BOOTSTRAP_ADMIN_EMAIL` and `BOOTSTRAP_ADMIN_PASSWORD` (optional pair).

## Data model (goose migrations)

- `users`: `id uuid pk`, `email text not null`, `username text not null`, `name text not null`, `password_hash text not null`, `status smallint not null`, `email_verified bool not null default false`, `roles text[] not null`, `created_at`, `updated_at`, `last_login_at null`, `failed_login_count int not null default 0`, `locked_until timestamptz null`. Unique indexes on `lower(email)` and `lower(username)`. `CHECK` that `status` is a valid enum value.
- `refresh_tokens`: `id uuid pk`, `user_id uuid references users on delete cascade`, `family_id uuid not null`, `token_hash bytea not null unique` (SHA-256), `expires_at`, `revoked_at null`, `created_at`.
- `one_time_tokens`: `id uuid pk`, `user_id references users on delete cascade`, `kind` (`reset` or `verify`), `token_hash bytea not null unique`, `expires_at`, `used_at null`, `created_at`.
- Store only hashes of refresh and one-time tokens. The raw value exists only in the response or email.

## Passwords

- Hash with **argon2id** (`golang.org/x/crypto/argon2`), 64 MiB memory, 3 iterations, parallelism 2, 16-byte random salt, 32-byte key, stored as a self-describing PHC string. Make the cost parameters injectable so tests can use cheap ones.
- Compare with `subtle.ConstantTimeCompare`.
- Policy (enforced in `internal/service`): 12 to 128 bytes, must not equal the email or username. Nothing else (no composition rules). Violations return `InvalidArgument`.
- Passwords and tokens are never logged, never returned, and never appear in error messages.

## UserService behaviour

| RPC | Who | Rules |
|---|---|---|
| `CreateUser` | public | Self-signup. Normalise email (trim, lower-case) and validate with `net/mail`. Username 3 to 32 chars `[a-z0-9_]`, lower-cased. Name 1 to 100 chars. Always create with roles `["customer"]` and status `PENDING_VERIFICATION`, ignoring anything the caller implies. Issue a `verify` one-time token (24h) and hand it to the Mailer. Duplicate email or username returns `AlreadyExists` (accepted trade-off: signup UX over enumeration resistance). |
| `GetUser` | self or admin | Anyone else gets `NotFound`. |
| `UpdateUser` | self or admin | `update_mask` is **required** and non-empty. Allowed paths: `name`, `username`. Any other path, including `email`, `status`, `roles`, `id`, returns `InvalidArgument` naming the path. Bump `updated_at`. |
| `DeleteUser` | self or admin | Hard delete (tokens cascade). Refuse to delete the **last admin** with `FailedPrecondition`. Safe to repeat on a missing row: `NotFound`. |
| `ListUsers` | admin only | Keyset pagination per prompt 00. Non-admin gets `PermissionDenied`. |

Role and status changes have no RPC in this contract. They are database-only for now. Do not invent one.

## AuthService behaviour

- **`Login`** (public). `identifier` is an email or username (decide by presence of `@`). Failure handling must not reveal whether the account exists:
  - Unknown user: still run one argon2id verification against a precomputed dummy hash so timing matches, then return `Unauthenticated` "invalid credentials".
  - Wrong password: increment `failed_login_count`. At 5 failures set `locked_until = now+15m`. While locked, skip the password check and return the same generic `Unauthenticated`. Reset the counter on success. (Trade-off: an attacker can lock someone out. Note it in the README.)
  - Correct password but `PENDING_VERIFICATION`: `FailedPrecondition` "email not verified". `SUSPENDED`: `PermissionDenied`.
  - Success: update `last_login_at`, return the `User` and `Tokens`.
- **Tokens.** Access token: JWT via `authn.Signer` (claims `sub`, `roles`, `iss`, `aud`, `exp`, `iat`, `jti`). Refresh token: 32 random bytes, base64url, stored hashed, with a `family_id`.
- **`RefreshToken`** (public). Rotating: revoke the presented token and issue a new one in the same family and a new access token, in one transaction. If the presented token is **already revoked** (reuse), revoke the entire family and return `Unauthenticated`. Expired or unknown: `Unauthenticated`. Re-check the user is still `ACTIVE` and re-read roles from the database.
- **`Logout`** (public, takes the refresh token). Revoke that token's family. Idempotent: unknown or already-revoked tokens still return success.
- **`ChangePassword`** (authenticated). The caller's `sub` must equal `user_id`; **admins are not exempt**. Verify `current_password`, apply the policy to `new_password`, update the hash, and revoke all of the user's refresh tokens. A wrong current password returns `PermissionDenied` and counts toward the lockout.
- **`RequestPasswordReset`** (public). Always returns success, whether or not the email exists, with no observable difference in the response. If the user exists and is not suspended, create a `reset` token (1h), invalidating earlier unused reset tokens, and hand it to the Mailer. Do the mail work in a way that does not make the "exists" branch measurably slower (for example enqueue on a goroutine with a bounded channel).
- **`ResetPassword`** (public). Look up by token hash, check it is unused and unexpired, apply the policy, set the hash, mark the token used, revoke all refresh tokens, clear the lockout, all in one transaction. Bad token: `InvalidArgument` "invalid or expired token".
- **`VerifyEmail`** (public). Mark the token used, set `email_verified = true` and status `ACTIVE` (only from `PENDING_VERIFICATION`; a `SUSPENDED` user stays suspended). Returns the `User`.

Public methods (passed to `authn.UnaryInterceptor`): `UserService/CreateUser`, `AuthService/Login`, `RefreshToken`, `Logout`, `RequestPasswordReset`, `ResetPassword`, `VerifyEmail`. Everything else needs a token.

## Mailer

`Mailer` interface in `internal/service` with `SendVerification(email, token)` and `SendPasswordReset(email, token)`. Ship one implementation, `LogMailer`, which writes the token to the log so a developer can complete flows locally. **`main` must refuse to start with `MAILER=log` when `APP_ENV=production`**, with a clear error. Real email delivery is out of scope. This is deliberate: a silent no-op mailer in production would hide broken signup.

## Bootstrap admin

If `BOOTSTRAP_ADMIN_EMAIL` and `BOOTSTRAP_ADMIN_PASSWORD` are both set and **no admin exists**, create one (roles `["admin","customer"]`, `ACTIVE`, email verified) at startup. If an admin already exists, do nothing. Apply the normal password policy. Log only that an admin was created, never the credentials.

## Required tests

- Service tests with a fake Store and cheap argon2 params: the full authorisation matrix per RPC; every `update_mask` rejection; lockout after 5 failures and generic errors for unknown user / wrong password / locked; refresh rotation, **refresh reuse revoking the family**; logout idempotence; `ChangePassword` rejects non-self including admin; reset invalidates sessions and old reset tokens; `RequestPasswordReset` response identical for known and unknown emails; last-admin delete refused; `VerifyEmail` does not un-suspend.
- Store tests against real Postgres: unique email and username (case-insensitive), keyset pagination ordering and boundaries, transactional refresh rotation, cascade delete.
- One in-process integration test: start the server on `bufconn` with the real interceptor, sign up, verify, log in, call `GetUser` with the access token, refresh, log out.

## Also deliver

`README.md` (env vars, key generation via `go run ./tools/keygen`, the lockout and enumeration trade-offs, how to run and test), and the `cmd/users` wiring including the `healthcheck` subcommand.

### Dockerfile: `services/users/Dockerfile`

Follow "Dockerfile (all services)" in prompt 00 exactly, with these specifics:
- Binary name `users`, `EXPOSE 50051`, `ENTRYPOINT ["/users"]`, `HEALTHCHECK CMD ["/users", "healthcheck"]`.
- Build: `docker build -f services/users/Dockerfile -t users:dev .` from the repo root.
- Runtime env, none baked in: `DATABASE_URL`, `JWT_PRIVATE_KEY` (this service is the only one that holds the private key), optional `BOOTSTRAP_ADMIN_*`. The image sets `APP_ENV=production`, so `MAILER=log` makes it refuse to start. Say in the README that local runs and compose must set `APP_ENV=development`.
- **Its own database:** this service owns the `users` database. Nothing else connects to it. The README shows a `docker run` pairing with a throwaway `postgres:17` container for a smoke test, and notes that the future compose file gives it a dedicated Postgres service and volume.

## Done when

`gofmt -l .` is empty; `go build ./... && go vet ./... && go test -short ./...` pass; `go test ./services/users/...` passes with Docker; `docker build` succeeds and the image runs as non-root; against a throwaway Postgres container the health check reports healthy and `grpcurl` can sign up, verify via the logged token, and log in; the container stops cleanly on `docker stop`; nothing under `proto/` or `gen/` changed.
