-- +goose Up
CREATE TABLE inventory_items (
    id         uuid PRIMARY KEY,
    sku        text        NOT NULL,
    quantity   integer     NOT NULL CHECK (quantity >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX inventory_items_sku_key ON inventory_items (sku);
CREATE INDEX inventory_items_created_at_id_idx ON inventory_items (created_at DESC, id DESC);

-- +goose Down
DROP TABLE inventory_items;
