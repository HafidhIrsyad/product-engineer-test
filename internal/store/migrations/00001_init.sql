-- +goose Up
-- ============================================================
-- ENUM
-- ============================================================
CREATE TYPE location_type AS ENUM (
    'FACTORY',            -- pabrik (asal serial PRODUCED)
    'CENTRAL_WAREHOUSE',  -- gudang pusat
    'HUB',                -- hub distribusi
    'OWN_STORE',          -- toko fisik resmi (POS OwnStore)
    'PARTNER'             -- lokasi mitra: distributor, Indomaret, dll.
);

CREATE TYPE serial_level AS ENUM ('UNIT', 'CARTON', 'PALLET');

CREATE TYPE serial_status AS ENUM (
    'PRODUCED',      -- sudah dicetak dan ditempel di lini produksi
    'IN_WAREHOUSE',  -- diterima gudang pusat
    'IN_TRANSIT',    -- dalam perjalanan ke lokasi tujuan
    'IN_STOCK',      -- diterima di hub / toko / mitra
    'SOLD',          -- terjual dan penjualannya diketahui sistem (POS sendiri / D2C)
    'VERIFIED',      -- diverifikasi pertama kali oleh konsumen
    'QUARANTINE'     -- ditahan (QC gagal, rusak, dicurigai)
);

CREATE TYPE order_status AS ENUM (
    'PENDING_PAYMENT',
    'PAID',
    'FULFILLED',
    'CANCELLED',
    'EXPIRED',            -- tidak dibayar sampai expires_at; reservasi dilepas sweeper
    'NEEDS_REALLOCATION'  -- stok yang dijanjikan hilang karena konflik dengan POS offline
);

CREATE TYPE movement_reason AS ENUM (
    'RECEIVE',             -- barang masuk lokasi
    'SHIP_OUT',            -- barang keluar lokasi (dikirim)
    'RESERVE',             -- reservasi order online
    'RELEASE',             -- reservasi dilepas (cancel / konflik)
    'SALE_ONLINE',         -- order online dipenuhi
    'SALE_POS',            -- penjualan kasir
    'SELL_OUT_INFERRED',   -- unit di mitra diverifikasi konsumen → dianggap terjual
    'QUARANTINE',
    'RELEASE_QUARANTINE'
);

-- ============================================================
-- MASTER
-- ============================================================
CREATE TABLE sku (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code                TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    units_per_carton    INT  NOT NULL CHECK (units_per_carton > 0),
    cartons_per_pallet  INT  NOT NULL CHECK (cartons_per_pallet > 0)
);

CREATE TABLE partner (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name              TEXT NOT NULL,
    -- Wilayah resmi disederhanakan menjadi lingkaran (titik pusat + radius).
    -- Produksi: polygon (PostGIS) atau kode wilayah administratif.
    region_name       TEXT NOT NULL,
    region_lat        DOUBLE PRECISION NOT NULL,
    region_lng        DOUBLE PRECISION NOT NULL,
    region_radius_km  DOUBLE PRECISION NOT NULL CHECK (region_radius_km > 0)
);

CREATE TABLE location (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code               TEXT NOT NULL UNIQUE,
    name               TEXT NOT NULL,
    type               location_type NOT NULL,
    partner_id         BIGINT REFERENCES partner(id),
    lat                DOUBLE PRECISION NOT NULL,
    lng                DOUBLE PRECISION NOT NULL,
    -- Boleh menjadi sumber stok order online?
    online_sellable    BOOLEAN NOT NULL DEFAULT false,
    -- Jumlah unit terakhir yang tidak dijual online (pencegahan konflik dengan toko).
    online_buffer      INT NOT NULL DEFAULT 0 CHECK (online_buffer >= 0),
    -- Lokasi mitra wajib punya partner; lokasi non-mitra tidak boleh.
    CHECK ((type = 'PARTNER') = (partner_id IS NOT NULL))
);

-- Heartbeat per perangkat POS (arch §13.6). Toko online hanya jika semua perangkat aktif segar.
CREATE TABLE pos_device (
    id                 TEXT PRIMARY KEY,                       -- device_id dari POS
    store_id           BIGINT NOT NULL REFERENCES location(id),
    active             BOOLEAN NOT NULL DEFAULT true,
    last_heartbeat_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE production_batch (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code         TEXT NOT NULL UNIQUE,
    sku_id       BIGINT NOT NULL REFERENCES sku(id),
    produced_at  TIMESTAMPTZ NOT NULL,
    expires_on   DATE NOT NULL,
    CHECK (expires_on > produced_at::date)
);

-- ============================================================
-- ORDER (dibuat sebelum serial karena serial.order_id merujuk ke sini)
-- ============================================================
CREATE TABLE orders (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Dikirim klien lewat header Idempotency-Key. Retry tidak membuat order ganda.
    idempotency_key  UUID NOT NULL UNIQUE,
    channel          TEXT NOT NULL CHECK (channel IN ('D2C', 'MARKETPLACE')),
    location_id      BIGINT NOT NULL REFERENCES location(id),
    status           order_status NOT NULL DEFAULT 'PENDING_PAYMENT',
    -- Batas bayar. Lewat dari ini, reservasi dilepas (§12.7).
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE order_item (
    order_id  BIGINT NOT NULL REFERENCES orders(id),
    sku_id    BIGINT NOT NULL REFERENCES sku(id),
    qty       INT    NOT NULL CHECK (qty > 0),
    PRIMARY KEY (order_id, sku_id)
);

-- Untuk mencari order terbaru di satu lokasi saat terjadi oversell (§13.5).
CREATE INDEX idx_orders_location_status ON orders (location_id, status, created_at DESC);
-- Untuk sweeper expiry (§12.7): hanya order yang masih menunggu bayar.
CREATE INDEX idx_orders_pending_expiry ON orders (expires_at) WHERE status = 'PENDING_PAYMENT';

-- ============================================================
-- SERIAL (identitas fisik)
-- ============================================================
CREATE TABLE serial (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Kode acak non-berurutan, isi QR. Contoh: MG7K2Q9XHD4PZ
    code         TEXT NOT NULL UNIQUE,
    level        serial_level  NOT NULL,
    sku_id       BIGINT NOT NULL REFERENCES sku(id),
    batch_id     BIGINT NOT NULL REFERENCES production_batch(id),
    -- Container tempat serial ini dikemas: unit → karton → pallet.
    parent_id    BIGINT REFERENCES serial(id),
    status       serial_status NOT NULL DEFAULT 'PRODUCED',
    -- Lokasi sekarang. Saat IN_TRANSIT berisi lokasi TUJUAN.
    location_id  BIGINT NOT NULL REFERENCES location(id),
    -- Distributor penerima alokasi. Dasar deteksi gray market (Kasus 1).
    partner_id   BIGINT REFERENCES partner(id),
    order_id     BIGINT REFERENCES orders(id),
    first_verified_at TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE INDEX idx_serial_parent ON serial (parent_id);
-- Untuk invariant check dan picking: unit per lokasi/SKU/status.
CREATE INDEX idx_serial_unit_loc ON serial (location_id, sku_id, status) WHERE level = 'UNIT';

CREATE TABLE serial_event (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    serial_id    BIGINT NOT NULL REFERENCES serial(id),
    from_status  serial_status,
    to_status    serial_status NOT NULL,
    location_id  BIGINT REFERENCES location(id),
    partner_id   BIGINT REFERENCES partner(id),
    ref_type     TEXT NOT NULL,   -- SHIPMENT | ORDER | POS_TXN | SCAN | QC | PACKING
    ref_id       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_serial_event_serial ON serial_event (serial_id, created_at);

-- ============================================================
-- STOK
-- ============================================================
CREATE TABLE stock_balance (
    sku_id       BIGINT NOT NULL REFERENCES sku(id),
    location_id  BIGINT NOT NULL REFERENCES location(id),
    on_hand      INT NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
    reserved     INT NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    quarantine   INT NOT NULL DEFAULT 0 CHECK (quarantine >= 0 AND quarantine <= on_hand),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (sku_id, location_id)
    -- SENGAJA TIDAK ADA: CHECK (on_hand - reserved - quarantine >= 0).
    -- Alasan: penjualan POS offline adalah fakta fisik yang harus bisa dicatat,
    -- walaupun sesaat membuat reserved > tersedia. Kondisi itu dideteksi dan
    -- diselesaikan di §13.5, bukan ditolak oleh constraint.
);

CREATE TABLE stock_movement (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku_id            BIGINT NOT NULL REFERENCES sku(id),
    location_id       BIGINT NOT NULL REFERENCES location(id),
    delta_on_hand     INT NOT NULL DEFAULT 0,
    delta_reserved    INT NOT NULL DEFAULT 0,
    delta_quarantine  INT NOT NULL DEFAULT 0,
    reason            movement_reason NOT NULL,
    ref_type          TEXT NOT NULL,
    ref_id            TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (delta_on_hand <> 0 OR delta_reserved <> 0 OR delta_quarantine <> 0)
);

CREATE INDEX idx_stock_movement_key ON stock_movement (sku_id, location_id, created_at);

-- ============================================================
-- POS
-- ============================================================
CREATE TABLE pos_transaction (
    -- UUIDv7 yang dibuat oleh perangkat POS = idempotency key.
    id              UUID PRIMARY KEY,
    store_id        BIGINT NOT NULL REFERENCES location(id),
    device_id       TEXT   NOT NULL,
    -- Nomor urut lokal di perangkat. Untuk urutan proses dan deteksi celah.
    local_seq       BIGINT NOT NULL,
    -- Waktu menurut perangkat. Hanya informasi, tidak dipakai untuk keputusan.
    occurred_at     TIMESTAMPTZ NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- SHA-256 dari payload kanonik. Mendeteksi UUID yang dipakai ulang untuk isi berbeda.
    payload_hash    BYTEA  NOT NULL,
    total_amount    BIGINT NOT NULL CHECK (total_amount >= 0),  -- rupiah, tanpa desimal
    payment_method  TEXT   NOT NULL CHECK (payment_method IN ('CASH', 'CARD', 'QRIS')),
    result          TEXT   NOT NULL CHECK (result IN ('ACCEPTED', 'ACCEPTED_WITH_ISSUES')),
    UNIQUE (device_id, local_seq)
);

CREATE TABLE pos_transaction_item (
    pos_transaction_id  UUID   NOT NULL REFERENCES pos_transaction(id),
    line_no             INT    NOT NULL,
    serial_code         TEXT   NOT NULL,         -- apa adanya dari scanner
    serial_id           BIGINT REFERENCES serial(id),  -- NULL jika kode tidak dikenal
    sku_id              BIGINT NOT NULL REFERENCES sku(id),
    price               BIGINT NOT NULL CHECK (price >= 0),
    issue               TEXT,                    -- NULL = valid
    PRIMARY KEY (pos_transaction_id, line_no)
);

-- ============================================================
-- SCAN KONSUMEN & ANOMALI
-- ============================================================
CREATE TABLE scan_event (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    serial_id   BIGINT REFERENCES serial(id),   -- NULL jika kode tidak dikenal
    raw_code    TEXT NOT NULL,
    device_id   TEXT NOT NULL,                  -- ID anonim dari cookie/localStorage
    lat         DOUBLE PRECISION,
    lng         DOUBLE PRECISION,
    verdict     TEXT NOT NULL CHECK (verdict IN ('GENUINE', 'GENUINE_WITH_WARNING', 'NOT_FOUND')),
    scanned_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_scan_event_serial ON scan_event (serial_id, scanned_at DESC);

CREATE TABLE anomaly (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type         TEXT NOT NULL CHECK (type IN (
                    'IMPOSSIBLE_TRAVEL',     -- serial sama, lokasi berjauhan, waktu tidak wajar
                    'SCAN_BEFORE_RELEASE',   -- konsumen memindai unit yang belum keluar gudang
                    'OUT_OF_REGION',         -- dipindai di luar wilayah distributor (gray market)
                    'POS_SERIAL_MISMATCH',   -- kasir menjual serial yang menurut data tidak di toko itu
                    'OVERSOLD'               -- konflik POS offline vs reservasi online
                 )),
    serial_id    BIGINT REFERENCES serial(id),
    location_id  BIGINT REFERENCES location(id),
    partner_id   BIGINT REFERENCES partner(id),
    ref_type     TEXT,
    ref_id       TEXT,
    details      JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ
);

CREATE INDEX idx_anomaly_open ON anomaly (type, created_at DESC) WHERE resolved_at IS NULL;
