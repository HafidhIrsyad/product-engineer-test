-- name: GetSerialForVerify :one
SELECT s.id, s.status, s.location_id, s.partner_id, s.first_verified_at,
       k.code AS sku_code, k.name AS sku_name,
       b.code AS batch_code, b.expires_on,
       p.region_lat, p.region_lng, p.region_radius_km
FROM serial s
JOIN sku k ON k.id = s.sku_id
JOIN production_batch b ON b.id = s.batch_id
LEFT JOIN partner p ON p.id = s.partner_id
WHERE s.code = $1
FOR UPDATE OF s;

-- name: GetPreviousOtherDeviceScan :one
-- Latest scan of this serial made by ANOTHER device (rule R1 compares only across devices).
SELECT scanned_at, lat, lng
FROM scan_event
WHERE serial_id = sqlc.arg(serial_id) AND device_id <> sqlc.arg(device_id)
ORDER BY scanned_at DESC, id DESC
LIMIT 1;

-- name: CountOtherScanDevices :one
SELECT COUNT(DISTINCT se.device_id)::int FROM scan_event se
WHERE se.serial_id = sqlc.arg(serial_id) AND se.device_id <> sqlc.arg(device_id)
  AND se.scanned_at >= COALESCE(
      (SELECT MIN(e.created_at) FROM serial_event e WHERE e.serial_id = sqlc.arg(serial_id) AND e.to_status IN ('SOLD', 'VERIFIED')),
      'infinity'::timestamptz);

-- name: CountScans :one
SELECT COUNT(*)::int FROM scan_event WHERE serial_id = $1;

-- name: InsertScanEvent :exec
INSERT INTO scan_event (serial_id, raw_code, device_id, lat, lng, verdict, scanned_at)
VALUES (sqlc.arg(serial_id), sqlc.arg(raw_code), sqlc.arg(device_id), sqlc.arg(lat), sqlc.arg(lng), sqlc.arg(verdict), sqlc.arg(scanned_at));

-- name: LeakageByPallet :many
WITH unit_pallet AS (
    SELECT u.id AS serial_id, u.partner_id, pal.code AS pallet
    FROM serial u
    JOIN serial ctn ON ctn.id = u.parent_id   AND ctn.level = 'CARTON'
    JOIN serial pal ON pal.id = ctn.parent_id AND pal.level = 'PALLET'
    WHERE u.level = 'UNIT' AND u.partner_id IS NOT NULL
)
SELECT p.name AS distributor,
       up.pallet,
       COUNT(DISTINCT se.serial_id)::int AS scanned_units,
       COUNT(DISTINCT a.serial_id)::int  AS units_outside_region
FROM unit_pallet up
JOIN partner p     ON p.id = up.partner_id
JOIN scan_event se ON se.serial_id = up.serial_id
LEFT JOIN anomaly a ON a.serial_id = up.serial_id AND a.type = 'OUT_OF_REGION'
GROUP BY p.name, up.pallet
HAVING COUNT(DISTINCT a.serial_id) >= sqlc.arg(min_units)::int
   AND COUNT(DISTINCT a.serial_id) >= sqlc.arg(min_ratio)::float8 * COUNT(DISTINCT se.serial_id)
ORDER BY units_outside_region DESC, up.pallet;
