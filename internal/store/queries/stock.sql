-- name: ReserveStock :execrows
-- Atomic: check availability and add to reserved in ONE statement (ARCHITECTURE.md §12.3).
-- Store stock is only for D2C, and only when every active POS device is online.
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
        AND NOT EXISTS (
            SELECT 1 FROM pos_device d
            WHERE d.store_id = l.id AND d.active
              AND d.last_heartbeat_at < now() - interval '5 minutes')))
  AND sb.on_hand - sb.reserved - sb.quarantine - l.online_buffer >= sqlc.arg(qty)::int;

-- name: ReleaseStock :execrows
-- The CHECK (reserved >= 0) constraint turns a double release into an error instead of bad data.
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
