-- +goose Up
CREATE TABLE users (
    id                 uuid PRIMARY KEY,
    email              text        NOT NULL,
    username           text        NOT NULL,
    name               text        NOT NULL,
    password_hash      text        NOT NULL,
    -- 1 = PENDING_VERIFICATION, 2 = ACTIVE, 3 = SUSPENDED (user.v1.UserStatus)
    status             smallint    NOT NULL CHECK (status IN (1, 2, 3)),
    email_verified     boolean     NOT NULL DEFAULT false,
    roles              text[]      NOT NULL,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    last_login_at      timestamptz,
    failed_login_count integer     NOT NULL DEFAULT 0,
    locked_until       timestamptz
);

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
CREATE UNIQUE INDEX users_username_lower_key ON users (lower(username));
CREATE INDEX users_created_at_id_idx ON users (created_at, id);

CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id  uuid        NOT NULL,
    token_hash bytea       NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);

CREATE TABLE one_time_tokens (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('reset', 'verify')),
    token_hash bytea       NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX one_time_tokens_user_kind_idx ON one_time_tokens (user_id, kind);

-- +goose Down
DROP TABLE one_time_tokens;
DROP TABLE refresh_tokens;
DROP TABLE users;
