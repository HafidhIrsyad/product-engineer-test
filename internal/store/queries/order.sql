-- name: GetLocationByCode :one
SELECT * FROM location WHERE code = $1;

-- name: GetSKUsByCodes :many
SELECT id, code FROM sku WHERE code = ANY(sqlc.arg(codes)::text[]);

-- name: GetOrderIDByIdempotencyKey :one
SELECT id FROM orders WHERE idempotency_key = $1;

-- name: InsertOrder :one
-- ON CONFLICT DO NOTHING: a concurrent request with the same Idempotency-Key waits for the
-- first one to commit, then inserts nothing (0 rows) and the caller returns the existing order.
-- Payment window: 15 minutes (ARCHITECTURE.md §19).
INSERT INTO orders (idempotency_key, channel, location_id, expires_at)
VALUES (sqlc.arg(idempotency_key), sqlc.arg(channel), sqlc.arg(location_id), now() + interval '15 minutes')
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id;

-- name: InsertOrderItem :exec
INSERT INTO order_item (order_id, sku_id, qty) VALUES ($1, $2, $3);

-- name: GetOrderView :one
SELECT o.id, o.channel, o.status, o.expires_at, o.location_id, l.code AS location_code
FROM orders o
JOIN location l ON l.id = o.location_id
WHERE o.id = $1;

-- name: GetOrderItems :many
-- Ordered by sku_id: stock rows are always locked in this order (ARCHITECTURE.md §12.4).
SELECT oi.sku_id, s.code AS sku_code, oi.qty
FROM order_item oi
JOIN sku s ON s.id = oi.sku_id
WHERE oi.order_id = $1
ORDER BY oi.sku_id;

-- name: LockOrder :one
-- Row lock: a concurrent cancel waits here, then reads the new status (§12.5).
SELECT id, location_id, status FROM orders WHERE id = $1 FOR UPDATE;

-- name: SetOrderStatus :exec
UPDATE orders SET status = sqlc.arg(status), updated_at = now() WHERE id = sqlc.arg(id);

-- name: PayOrder :execrows
-- Pay and expiry race is decided by this WHERE clause: only one wins (§12.7).
UPDATE orders SET status = 'PAID', updated_at = now()
WHERE id = $1 AND status = 'PENDING_PAYMENT' AND expires_at > now();

-- name: ListExpiredOrderIDs :many
SELECT id FROM orders WHERE status = 'PENDING_PAYMENT' AND expires_at < now() ORDER BY id;

-- name: ExpireOrder :one
-- 0 rows (ErrNoRows) = the order was paid or cancelled since it was listed: skip it (§12.7).
UPDATE orders SET status = 'EXPIRED', updated_at = now()
WHERE id = $1 AND status = 'PENDING_PAYMENT' AND expires_at < now()
RETURNING location_id;
