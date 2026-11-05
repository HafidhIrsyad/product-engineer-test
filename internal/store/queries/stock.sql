-- name: ReserveStock :execrows
-- Atomic: check availability and add to reserved in ONE statement (ARCHITECTURE.md §12.3).
-- Store stock is only for D2C, and only when the store has active POS devices and all are online.
-- 0 rows = not enough stock (or location not sellable).
UPDATE stock_balance sb
SET reserved   = sb.reserved + sqlc.arg(qty)::int,
    updated_at = now()
FROM location l
WHERE l.id = sb.location_id
  AND sb.sku_id      = sqlc.arg(sku_id)
  AND sb.location_id = sqlc.arg(location_id)
  AND l.online_sellable
  AND (l.type <> 'OWN_STORE' OR (
        sqlc.arg(channel)::text = 'D2C'
        -- A store is online only if it has at least one active device and none of them is stale.
        -- A store with no device at all is NOT online: nothing proves its cashiers are connected.
        AND EXISTS (
            SELECT 1 FROM pos_device d WHERE d.store_id = l.id AND d.active)
        AND NOT EXISTS (
            SELECT 1 FROM pos_device d
            WHERE d.store_id = l.id AND d.active
              AND d.last_heartbeat_at < now() - interval '5 minutes')))
  AND sb.on_hand - sb.reserved - sb.quarantine - l.online_buffer >= sqlc.arg(qty)::int;

-- name: ReleaseStock :execrows
-- Releasing twice is prevented by the callers (the order row lock, or a conditional status UPDATE),
-- not by this statement. CHECK (reserved >= 0) only catches it when no other order holds stock here.
UPDATE stock_balance
SET reserved   = reserved - sqlc.arg(qty)::int,
    updated_at = now()
WHERE sku_id = sqlc.arg(sku_id)
  AND location_id = sqlc.arg(location_id);

-- name: InsertStockMovement :exec
INSERT INTO stock_movement (sku_id, location_id, delta_on_hand, delta_reserved, delta_quarantine, reason, ref_type, ref_id)
VALUES (
    sqlc.arg(sku_id), sqlc.arg(location_id), sqlc.arg(delta_on_hand), sqlc.arg(delta_reserved),
    sqlc.arg(delta_quarantine), sqlc.arg(reason), sqlc.arg(ref_type), sqlc.arg(ref_id)
);
