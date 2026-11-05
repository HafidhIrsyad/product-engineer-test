-- Demo data for `make seed`. Starts with TRUNCATE, so running it again resets the demo.
-- Not used by tests: every test creates its own data (ARCHITECTURE.md §18.4).
-- Serial codes are MG + 11 digits, so the demo can type them by hand (see scripts/demo.sh).

BEGIN;

TRUNCATE sku, partner, location, pos_device, production_batch, orders, order_item,
         serial, serial_event, stock_balance, stock_movement,
         pos_transaction, pos_transaction_item, scan_event, anomaly
RESTART IDENTITY CASCADE;

INSERT INTO sku (code, name, units_per_carton, cartons_per_pallet) VALUES
    ('MGN-PROT-500', 'mGanik Protein 500g', 12, 40),
    ('MGN-COLL-200', 'mGanik Collagen 200g', 24, 30),
    ('MGN-OMEGA-100', 'mGanik Omega 100g', 12, 40);

-- Regional distributor whose official region is a circle around Medan (§10.3, R3).
INSERT INTO partner (name, region_name, region_lat, region_lng, region_radius_km) VALUES
    ('Distributor Medan', 'Sumatera Utara', 3.5952, 98.6722, 150);

INSERT INTO location (code, name, type, partner_id, lat, lng, online_sellable, online_buffer) VALUES
    ('FACTORY-01',   'Pabrik Bogor',             'FACTORY',           NULL, -6.5950, 106.8166, false, 0),
    ('WH-JKT',       'Gudang Pusat Jakarta',     'CENTRAL_WAREHOUSE', NULL, -6.2088, 106.8456, true,  0),
    -- Demo store uses buffer 0 so the split-brain conflict can be shown (§19, T7, demo #4).
    ('STORE-BDG-01', 'OwnStore Bandung',         'OWN_STORE',         NULL, -6.9175, 107.6191, true,  0),
    ('DIST-MDN',     'Gudang Distributor Medan', 'PARTNER',           1,     3.5952,  98.6722, false, 0);

INSERT INTO production_batch (code, sku_id, produced_at, expires_on) VALUES
    ('B2611-01', 1, '2026-11-01 08:00:00+07', '2028-05-01'),
    ('B2611-02', 2, '2026-11-01 08:00:00+07', '2028-05-01'),
    ('B2611-03', 3, '2026-11-01 08:00:00+07', '2028-05-01');

-- ---------------------------------------------------------------------------
-- Units. Counters must match the serials (invariant I1, ARCHITECTURE.md §11.2).
-- ---------------------------------------------------------------------------
INSERT INTO serial (code, level, sku_id, batch_id, status, location_id, partner_id)
SELECT 'MG' || lpad(n::text, 11, '0'), 'UNIT',
       (SELECT id FROM sku WHERE code = v.sku), (SELECT id FROM production_batch WHERE sku_id = (SELECT id FROM sku WHERE code = v.sku)),
       v.status::serial_status, (SELECT id FROM location WHERE code = v.loc),
       CASE WHEN v.loc = 'DIST-MDN' THEN 1 END
FROM (VALUES
    -- Demo 1, verify: three units already sold at the warehouse
    (1,   'MGN-PROT-500', 'SOLD',         'WH-JKT'),
    (2,   'MGN-PROT-500', 'SOLD',         'WH-JKT'),
    (3,   'MGN-PROT-500', 'SOLD',         'WH-JKT'),
    -- Demo 2, order surge: 10 units in the central warehouse
    (201, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (202, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (203, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (204, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (205, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (206, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (207, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (208, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (209, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    (210, 'MGN-COLL-200', 'IN_WAREHOUSE', 'WH-JKT'),
    -- Demo 3, offline POS: 3 units on the shelf of the Bandung store
    (101, 'MGN-PROT-500', 'IN_STOCK',     'STORE-BDG-01'),
    (102, 'MGN-PROT-500', 'IN_STOCK',     'STORE-BDG-01'),
    (103, 'MGN-PROT-500', 'IN_STOCK',     'STORE-BDG-01'),
    -- Demo 4, split-brain: one last unit of another product at the same store
    (301, 'MGN-OMEGA-100','IN_STOCK',     'STORE-BDG-01'),
    -- Allocated to Distributor Medan, on the shelf of its warehouse (scan from Jakarta → out of region)
    (401, 'MGN-PROT-500', 'IN_STOCK',     'DIST-MDN')
) AS v(n, sku, status, loc);

INSERT INTO stock_balance (sku_id, location_id, on_hand)
SELECT sku_id, location_id, COUNT(*)
FROM serial
WHERE level = 'UNIT' AND status IN ('IN_WAREHOUSE', 'IN_STOCK', 'QUARANTINE')
GROUP BY sku_id, location_id;

COMMIT;
