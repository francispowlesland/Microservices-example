-- +goose Up
CREATE TABLE orders (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL,
    total_cents bigint      NOT NULL CHECK (total_cents >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Per-user list.
CREATE INDEX orders_user_created_idx ON orders (user_id, created_at DESC, id DESC);
-- Admin list.
CREATE INDEX orders_created_idx ON orders (created_at DESC, id DESC);

-- +goose Down
DROP TABLE orders;
