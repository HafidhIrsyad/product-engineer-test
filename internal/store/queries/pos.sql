-- name: TouchHeartbeat :execrows
-- A new device registers itself as active. 0 rows = the device_id belongs to another store.
INSERT INTO pos_device (id, store_id, last_heartbeat_at)
VALUES (sqlc.arg(device_id), sqlc.arg(store_id), now())
ON CONFLICT (id) DO UPDATE SET last_heartbeat_at = now()
WHERE pos_device.store_id = EXCLUDED.store_id;

-- name: InsertPosTransaction :execrows
-- ON CONFLICT DO NOTHING (no target) covers BOTH unique keys: the transaction id (a retry) and
-- (device_id, local_seq) (a device bug). A conflict inserts nothing, so the transaction stays usable.
-- A concurrent insert of the same id waits for the first to commit, then does nothing.
INSERT INTO pos_transaction (id, store_id, device_id, local_seq, occurred_at, payload_hash, total_amount, payment_method, result)
VALUES (
    sqlc.arg(id), sqlc.arg(store_id), sqlc.arg(device_id), sqlc.arg(local_seq), sqlc.arg(occurred_at),
    sqlc.arg(payload_hash), sqlc.arg(total_amount), sqlc.arg(payment_method), 'ACCEPTED'
)
ON CONFLICT DO NOTHING;

-- name: GetPosTransaction :one
SELECT payload_hash, result FROM pos_transaction WHERE id = $1;

-- name: SetPosTransactionResult :exec
UPDATE pos_transaction SET result = $2 WHERE id = $1;

-- name: InsertPosTransactionItem :exec
INSERT INTO pos_transaction_item (pos_transaction_id, line_no, serial_code, serial_id, sku_id, price, issue)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: LockSerialsByCodes :many
-- One query, ORDER BY id: serials are always locked in id order (ARCHITECTURE.md §12.4).
SELECT id, code, level, sku_id, location_id, status
FROM serial
WHERE code = ANY(sqlc.arg(codes)::text[])
ORDER BY id
FOR UPDATE;

-- name: TransitionSerial :one
-- No row (ErrNoRows) = the status changed since it was read (§9.3).
-- The first move to VERIFIED also stamps first_verified_at.
UPDATE serial
SET status = sqlc.arg(to_status)::serial_status,
    first_verified_at = CASE WHEN sqlc.arg(to_status)::serial_status = 'VERIFIED' THEN now() ELSE first_verified_at END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status)
RETURNING first_verified_at;

-- name: InsertSerialEvent :exec
INSERT INTO serial_event (serial_id, from_status, to_status, location_id, ref_type, ref_id)
VALUES (sqlc.arg(serial_id), sqlc.arg(from_status), sqlc.arg(to_status), sqlc.arg(location_id), sqlc.arg(ref_type), sqlc.arg(ref_id));

-- name: InsertAnomaly :exec
INSERT INTO anomaly (type, serial_id, location_id, partner_id, ref_type, ref_id, details)
VALUES (sqlc.arg(type), sqlc.arg(serial_id), sqlc.arg(location_id), sqlc.arg(partner_id), sqlc.arg(ref_type), sqlc.arg(ref_id), sqlc.arg(details));

-- name: DecreaseOnHand :execrows
-- A POS sale takes physical units out of the store. CHECK (on_hand >= 0) guards the counter.
UPDATE stock_balance SET on_hand = on_hand - sqlc.arg(qty)::int, updated_at = now()
WHERE sku_id = sqlc.arg(sku_id) AND location_id = sqlc.arg(location_id);

-- name: LockStoreOversell :exec
-- Only one oversell resolution per store at a time (§13.5). Held until the transaction ends.
SELECT pg_advisory_xact_lock(sqlc.arg(location_id)::bigint);

-- name: GetShortfall :one
-- Positive when more is reserved than physically available.
SELECT (reserved - (on_hand - quarantine))::int AS shortfall
FROM stock_balance
WHERE sku_id = sqlc.arg(sku_id) AND location_id = sqlc.arg(location_id);

-- name: PickOversellVictim :one
-- The newest open order of the store that holds this SKU (§13.5). Not locked here: SELECT ... LIMIT 1
-- FOR UPDATE returns no row when the candidate is cancelled while waiting for the lock, even if other
-- candidates exist. The caller locks the order and re-checks its status instead.
SELECT o.id
FROM orders o
JOIN order_item oi ON oi.order_id = o.id AND oi.sku_id = sqlc.arg(sku_id)
WHERE o.location_id = sqlc.arg(location_id)
  AND o.status IN ('PENDING_PAYMENT', 'PAID')
ORDER BY o.created_at DESC, o.id DESC
LIMIT 1;

-- name: GetSerialStatus :one
SELECT status FROM serial WHERE id = $1;
