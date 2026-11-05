-- name: ListStockOverview :many
-- Stock per SKU per location, including locations a shipment is heading to that hold nothing yet,
-- with what is on its way in and what was sold in the last 14 days.
WITH places AS (
    SELECT sku_id, location_id FROM stock_balance
  UNION
    SELECT sku_id, location_id FROM serial WHERE level = 'UNIT' AND status = 'IN_TRANSIT'
)
SELECT l.code AS location_code, l.type AS location_type, k.code AS sku_code,
       COALESCE(sb.on_hand, 0)::int AS on_hand,
       COALESCE(sb.reserved, 0)::int AS reserved,
       COALESCE(sb.quarantine, 0)::int AS quarantine,
       (SELECT COUNT(*) FROM serial x
         WHERE x.level = 'UNIT' AND x.status = 'IN_TRANSIT' AND x.location_id = l.id AND x.sku_id = k.id)::int AS inbound,
       COALESCE((SELECT -SUM(m.delta_on_hand) FROM stock_movement m
         WHERE m.sku_id = k.id AND m.location_id = l.id
           AND m.reason IN ('SALE_POS', 'SALE_ONLINE', 'SELL_OUT_INFERRED')
           AND m.created_at >= now() - interval '14 days'), 0)::bigint AS sold_14d
FROM places pl
JOIN location l ON l.id = pl.location_id
JOIN sku k ON k.id = pl.sku_id
LEFT JOIN stock_balance sb ON sb.sku_id = pl.sku_id AND sb.location_id = pl.location_id
WHERE (sqlc.narg(sku_code)::text IS NULL OR k.code = sqlc.narg(sku_code))
  AND (sqlc.narg(location_code)::text IS NULL OR l.code = sqlc.narg(location_code))
ORDER BY l.code, k.code;

-- name: ListAnomalies :many
-- Open anomalies, newest first.
SELECT a.id, a.type, ser.code AS serial_code, l.code AS location_code, p.name AS partner_name,
       a.ref_type, a.ref_id, a.details, a.created_at
FROM anomaly a
LEFT JOIN serial ser ON ser.id = a.serial_id
LEFT JOIN location l ON l.id = a.location_id
LEFT JOIN partner p ON p.id = a.partner_id
WHERE a.resolved_at IS NULL
  AND (sqlc.narg(type)::text IS NULL OR a.type = sqlc.narg(type))
  AND (sqlc.narg(partner_name)::text IS NULL OR p.name = sqlc.narg(partner_name))
  AND a.created_at >= sqlc.arg(since)::timestamptz
ORDER BY a.created_at DESC, a.id DESC
LIMIT 200;

-- name: SellOutSignal :many
-- Per distributor per week: units shipped to it, and units of its allocation that consumers scanned.
-- Input for a geo-holdout comparison (PRODUCT_DEFENSE.md case 3), not proof of cause.
WITH shipped AS (
    SELECT e.partner_id, date_trunc('week', e.created_at) AS week, COUNT(*)::int AS units
    FROM serial_event e
    JOIN serial s ON s.id = e.serial_id AND s.level = 'UNIT'
    WHERE e.to_status = 'IN_TRANSIT' AND e.partner_id IS NOT NULL
      AND e.created_at >= sqlc.arg(from_time)::timestamptz AND e.created_at < sqlc.arg(to_time)::timestamptz
    GROUP BY e.partner_id, date_trunc('week', e.created_at)
), scanned AS (
    SELECT s.partner_id, date_trunc('week', se.scanned_at) AS week, COUNT(DISTINCT se.serial_id)::int AS units
    FROM scan_event se
    JOIN serial s ON s.id = se.serial_id
    WHERE s.partner_id IS NOT NULL
      AND se.scanned_at >= sqlc.arg(from_time)::timestamptz AND se.scanned_at < sqlc.arg(to_time)::timestamptz
    GROUP BY s.partner_id, date_trunc('week', se.scanned_at)
)
SELECT p.name AS partner, w.week::timestamptz AS week,
       COALESCE(h.units, 0)::int AS shipped_units, COALESCE(c.units, 0)::int AS scanned_units
FROM (SELECT partner_id, week FROM shipped UNION SELECT partner_id, week FROM scanned) w
JOIN partner p ON p.id = w.partner_id
LEFT JOIN shipped h ON h.partner_id = w.partner_id AND h.week = w.week
LEFT JOIN scanned c ON c.partner_id = w.partner_id AND c.week = w.week
ORDER BY p.name, w.week;
