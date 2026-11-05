-- Demo data for `make seed`. Starts with TRUNCATE, so running it again resets the demo.
-- Not used by tests: every test creates its own data (ARCHITECTURE.md §18.4).
-- Master data only for now; serials, stock and orders are added with their use cases.

BEGIN;

TRUNCATE sku, partner, location, pos_device, production_batch, orders, order_item,
         serial, serial_event, stock_balance, stock_movement,
         pos_transaction, pos_transaction_item, scan_event, anomaly
RESTART IDENTITY CASCADE;

INSERT INTO sku (code, name, units_per_carton, cartons_per_pallet) VALUES
    ('MGN-PROT-500', 'mGanik Protein 500g', 12, 40),
    ('MGN-COLL-200', 'mGanik Collagen 200g', 24, 30);

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
    ('B2611-02', 2, '2026-11-01 08:00:00+07', '2028-05-01');

COMMIT;
