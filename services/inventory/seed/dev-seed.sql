-- Development sample data, loaded by the inventory-seed service in docker-compose.yml only.
-- Re-running is safe: existing SKUs keep their current stock and price.
INSERT INTO inventory_items (id, sku, quantity, price_cents) VALUES
    (gen_random_uuid(), 'WIDGET-S',   120,  499),
    (gen_random_uuid(), 'WIDGET-M',    80,  799),
    (gen_random_uuid(), 'WIDGET-L',    40, 1199),
    (gen_random_uuid(), 'GADGET-BLK',  60, 2499),
    (gen_random_uuid(), 'GADGET-WHT',  60, 2499),
    (gen_random_uuid(), 'CABLE-USBC',  300,  899),
    (gen_random_uuid(), 'CASE-HARD',   25, 3450),
    (gen_random_uuid(), 'STAND-ALU',   15, 4999),
    (gen_random_uuid(), 'LIMITED-ED',   3, 9999),
    (gen_random_uuid(), 'OUT-OF-STOCK', 0, 1500)
ON CONFLICT (sku) DO NOTHING;
