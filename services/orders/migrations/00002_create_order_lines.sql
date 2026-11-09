-- +goose Up
-- Orders placed before this migration have no lines; their total_cents stands as recorded.
CREATE TABLE order_lines (
    order_id         uuid    NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    position         integer NOT NULL,
    sku              text    NOT NULL,
    quantity         integer NOT NULL CHECK (quantity > 0),
    unit_price_cents bigint  NOT NULL CHECK (unit_price_cents >= 0),
    PRIMARY KEY (order_id, position)
);

-- +goose Down
DROP TABLE order_lines;
