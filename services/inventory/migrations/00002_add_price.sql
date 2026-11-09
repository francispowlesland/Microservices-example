-- +goose Up
ALTER TABLE inventory_items ADD COLUMN price_cents bigint NOT NULL DEFAULT 0 CHECK (price_cents >= 0);

-- +goose Down
ALTER TABLE inventory_items DROP COLUMN price_cents;
