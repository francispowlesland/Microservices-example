# Authentication and authorisation

This page describes what the code does today. It covers token issuance, verification, key handling, per-service permission checks, refresh tokens and the gateway's session cookies.

## Overview

- The **users** service is the only issuer. It signs short-lived access tokens (JWT, EdDSA / Ed25519) and manages opaque refresh tokens.
- **orders**, **payments** and **inventory** (and users, for its own protected methods) verify access tokens locally with the public key. They never call the users service to do it.
- The **gateway** holds the access and refresh tokens in cookies and attaches the access token as `authorization: Bearer <jwt>` on every call to a backend.

## Access tokens

Implemented in `internal/platform/authn`.

| Property | Value |
|---|---|
| Algorithm | EdDSA (Ed25519) only |
| Issuer (`iss`) | `microservices-app/users` |
| Audience (`aud`) | `microservices-app` |
| Subject (`sub`) | the user's UUID |
| Custom claim | `roles` (array of strings; omitted when empty) |
| Other claims | `exp`, `iat` |
| Lifetime | `ACCESS_TOKEN_TTL`, default `15m` |

There is no `jti`, `nbf` or key ID (`kid`) in the token.

`Verifier.Verify` rejects a token unless the signing method is EdDSA, the signature matches the configured public key, and issuer, audience and expiry are valid. Anything else (`alg=none`, HMAC, a different key) fails.

`UnaryInterceptor` is **default-deny**. For every unary call it checks the method name against the service's public-method list; if it isn't listed, it requires an `authorization` metadata value starting with `Bearer ` and a token that verifies. Failures return `Unauthenticated`. On success the claims (`Subject`, `Roles`) are put in the request context. Handlers then make their own authorisation decisions.

Because verification is local, a token stays valid until it expires even if the user is deleted or their account state changes afterwards. The access-token lifetime is the bound on that window.

## Key distribution

`go run ./tools/keygen` generates a fresh Ed25519 key pair and prints two PEM blocks:

- `JWT_PRIVATE_KEY`: PKCS#8 (`-----BEGIN PRIVATE KEY-----`). **Users service only.**
- `JWT_PUBLIC_KEY`: PKIX (`-----BEGIN PUBLIC KEY-----`). Every verifying service.

| Service | Reads | Notes |
|---|---|---|
| users | `JWT_PRIVATE_KEY` | Required. It derives the public key from this and uses it to verify its own protected methods. It does not read `JWT_PUBLIC_KEY`. |
| orders, payments, inventory | `JWT_PUBLIC_KEY` | Required. A malformed key stops the service at startup. |
| gateway | neither | It never verifies signatures; see below. |

`docker-compose.yml` is wired this way: only `users` receives the private key, and compose refuses to start if `JWT_PRIVATE_KEY` or `JWT_PUBLIC_KEY` is unset (`${VAR:?…}`). See [local-development.md](local-development.md) for how to supply them.

**Handle the private key as a secret.** Anyone holding it can mint a token for any user with any role, and every backend will accept it. Notes:

- The repository root has no `.gitignore`, so a `.env` file placed there is not ignored. Don't commit it.
- Docker Compose passes the values as container environment variables, so they're visible to anyone who can run `docker inspect` on the host.
- There is no key rotation mechanism: one key, no `kid`. To change keys, redeploy every service with the new pair. Outstanding access tokens then fail verification at the backends. Refresh tokens don't depend on the signing key (they're random values checked against the database). Note that the gateway doesn't check signatures, so it will keep treating an old-key access cookie as a live session until that token's `exp`; calls made with it get `Unauthenticated` from the backends in the meantime.

## Roles and permission checks

Roles are strings in the `roles` claim. The code uses two: `admin` and `customer`.

- Self-signup (`CreateUser`) always assigns `["customer"]`. Roles and status are fixed by the server, not taken from the request.
- The only way the code creates an `admin` is `BOOTSTRAP_ADMIN_EMAIL` / `BOOTSTRAP_ADMIN_PASSWORD` at users-service startup (roles `admin` and `customer`). There is no RPC for granting or changing roles, and `UpdateUser` can only change `name` and `username`.

Where a non-admin asks for another person's single record, the services return `NotFound` rather than `PermissionDenied`, so IDs can't be probed.

### users

Public (no token): `CreateUser`, `Login`, `RefreshToken`, `Logout`, `RequestPasswordReset`, `ResetPassword`, `VerifyEmail`, and the gRPC health `Check`.

| Method | Rule |
|---|---|
| `GetUser`, `UpdateUser`, `DeleteUser` | The caller's `sub` equals the target ID, or the caller is `admin`. Otherwise `NotFound`. |
| `ListUsers` | `admin` only (`PermissionDenied`). |
| `ChangePassword` | `sub` must equal `user_id`. Admins are not exempt. |

`DeleteUser` refuses to remove the last remaining admin (`FailedPrecondition`); the check locks all admin rows so two concurrent deletes can't both pass.

### orders

No public methods. Every call needs a valid token with a UUID `sub`.

| Method | Rule |
|---|---|
| `CreateOrder` | `user_id` defaults to the caller. Setting it to someone else needs `admin` (`PermissionDenied`). |
| `GetOrder` | Owner or `admin`; otherwise `NotFound`. |
| `UpdateOrder`, `DeleteOrder` | `admin` only. |
| `ListOrders` | Non-admins are always restricted to their own orders. Asking for another user's gives `PermissionDenied`. Admins see everything, or can filter by `user_id`. |

### payments

Public: the gRPC health `Check` only.

| Method | Rule |
|---|---|
| `ProcessPayment` | Any authenticated user. The payment is recorded with `created_by` = caller. |
| `GetPayment` | The user who created it, or `admin`; otherwise `NotFound`. |
| `ListPayments` | Non-admins see only payments they created; admins see all. |
| `RefundPayment` | `admin` only. |

Ownership is by `created_by`, not by who owns the order. Payments does not check that the caller owns, or even that there exists, the order referenced by `order_id` (there's no inter-service call; see [architecture.md](architecture.md)).

### inventory

No public methods.

| Method | Rule |
|---|---|
| `GetInventoryItem`, `ListInventoryItems` | Any authenticated user. |
| `CreateInventoryItem`, `UpdateInventoryItem`, `DeleteInventoryItem` | `admin` only (`PermissionDenied`). |

## Passwords and login

- Passwords are hashed with argon2id (64 MiB, 3 iterations, parallelism 2) and stored as a PHC string. Policy: 12 to 128 bytes, and not equal to the email or username. Nothing else is enforced.
- `Login` accepts an email or username. Every credential failure returns the same `Unauthenticated: invalid credentials`. An unknown identifier still performs one argon2id verification against a precomputed dummy hash, to keep timing similar.
- After 5 failed attempts the account is locked for 15 minutes; a locked account also returns `invalid credentials`. `ChangePassword` with a wrong current password counts as a failed attempt too.
- Account state is checked only after the password is correct: `PENDING_VERIFICATION` returns `FailedPrecondition` ("email not verified"), `SUSPENDED` returns `PermissionDenied` ("account suspended"). Only `ACTIVE` accounts get tokens.
- Email verification tokens last 24 hours; password reset tokens last 1 hour. Both are 32 random bytes, stored only as SHA-256 hashes, and single-use. Requesting a new reset token invalidates earlier unused ones.
- `RequestPasswordReset` always returns success, and the lookup and token write happen on a background worker, so the response doesn't reveal whether the email exists. The worker queue holds 64 jobs; when it is full, jobs are dropped with a warning.

### Email delivery is a log line

The only mailer is `LogMailer`, which **writes verification and reset tokens to the service log**. It is development-only. The configuration validator enforces two things at once: `MAILER` must be `log` (the sole implementation), and `MAILER=log` is rejected when `APP_ENV=production`. The practical result is that the users service **cannot currently start with `APP_ENV=production`**. A real mailer is needed before it can. The users Dockerfile defaults to `APP_ENV=production`, which is why compose overrides it to `development`.

## Refresh tokens

Implemented in the users service (`internal/service/auth.go`, `internal/store/store.go`).

- A refresh token is 32 random bytes, base64url-encoded, opaque to clients. Only its SHA-256 hash is stored (`refresh_tokens.token_hash`).
- Lifetime: `REFRESH_TOKEN_TTL`, default `720h` (30 days).
- Every login starts a new **family** (`family_id`). Each `RefreshToken` call rotates: the presented token is marked revoked and a new one in the same family is issued, along with a new access token.
- **Reuse detection.** Presenting a token that was already revoked revokes the entire family and returns `Unauthenticated: invalid refresh token`; the service logs a warning. The legitimate holder and the thief both lose access, and the user has to log in again.
- Refresh fails with the same generic error if the token is unknown, expired, or the user is no longer `ACTIVE`.
- `Logout` revokes the presented token's family. It succeeds for unknown or already-revoked tokens, and it's a public method.
- `ChangePassword` and `ResetPassword` revoke every refresh token the user has.
- Deleting a user cascades to their refresh tokens.

Revoking refresh tokens does not revoke access tokens that have already been issued; those expire on their own schedule.

## How the gateway stores sessions

Implemented in `services/gateway/src/lib/server/session.ts` and `src/hooks.server.ts`. There is no server-side session store.

- Two cookies: `ob_access` and `ob_refresh`. Both are `httpOnly`, `SameSite=Lax`, `Path=/`, and expire when their token does. The `Secure` attribute is not set explicitly, so SvelteKit's default applies (in the installed version: `Secure` unless the page is served from `http://localhost` or in dev). Serving over plain HTTP on any other hostname will cause browsers to refuse the cookies.
- On every request, the `handle` hook reads `ob_access`, decodes the JWT payload and treats the session as valid if it has a string `sub`, a numeric `exp`, and more than 30 seconds of life left.
- **The gateway does not verify the signature.** It only reads the claims to decide what UI to show (for example, whether the user is an admin). A forged cookie can change what the UI displays, but every backend verifies the token itself, so it can't read or change data. The admin-only `/users` pages check `isAdmin` for a friendlier error; the users service enforces the real rule.
- If the access token is missing or about to expire, the hook calls `AuthService.RefreshToken` with `ob_refresh` and sets both cookies from the response. If the backend answers `Unauthenticated`, both cookies are deleted. If the backend is merely unreachable, the cookies are kept and the request is treated as signed out.
- **Concurrent refreshes are deduplicated.** Because rotation plus reuse detection would otherwise log a user out when two parallel requests present the same refresh token, the gateway shares one in-flight refresh promise per refresh token (kept for 10 seconds after it settles). That map is per process. With more than one gateway replica, use sticky sessions or a shared store; the code carries a `shortcut:` comment saying so.
- Unauthenticated users are redirected to `/login?next=…` for anything except `/login`, `/signup`, `/verify`, `/forgot` and `/reset`. The `next` value is only honoured if it is a same-site path (starts with `/`, not `//` or `/\`).
- Logout calls `AuthService.Logout` with the refresh token (ignoring failure), then deletes both cookies and redirects to `/login`.
