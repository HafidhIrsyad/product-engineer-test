-- name: GetSKUByCode :one
SELECT id, units_per_carton, cartons_per_pallet FROM sku WHERE code = $1;

-- name: InsertBatch :one
INSERT INTO production_batch (code, sku_id, produced_at, expires_on)
VALUES (sqlc.arg(code), sqlc.arg(sku_id), now(), sqlc.arg(expires_on))
ON CONFLICT (code) DO NOTHING
RETURNING id;

-- name: InsertSerials :many
-- One statement per level. Codes and parent ids line up by position; parent id 0 means no parent.
INSERT INTO serial (code, level, sku_id, batch_id, location_id, parent_id)
SELECT c.code, sqlc.arg(level), sqlc.arg(sku_id), sqlc.arg(batch_id), sqlc.arg(location_id), NULLIF(p.parent, 0)
FROM unnest(sqlc.arg(codes)::text[]) WITH ORDINALITY AS c(code, n)
JOIN unnest(sqlc.arg(parent_ids)::bigint[]) WITH ORDINALITY AS p(parent, n) ON p.n = c.n
RETURNING id, code, parent_id;

-- name: InsertBatchSerialEvents :exec
-- One history row per serial of the batch, written in a single statement.
INSERT INTO serial_event (serial_id, to_status, location_id, ref_type, ref_id)
SELECT id, 'PRODUCED', location_id, 'PACKING', sqlc.arg(ref_id)::text
FROM serial WHERE batch_id = sqlc.arg(batch_id);

-- name: GetSerialTrace :one
SELECT s.id, s.code, s.level, s.status, s.first_verified_at,
       k.code AS sku_code, b.code AS batch_code, l.code AS location_code, p.name AS partner_name
FROM serial s
JOIN sku k ON k.id = s.sku_id
JOIN production_batch b ON b.id = s.batch_id
JOIN location l ON l.id = s.location_id
LEFT JOIN partner p ON p.id = s.partner_id
WHERE s.code = $1;

-- name: ListSerialAncestors :many
-- The containers above a serial, nearest first (unit: carton, pallet).
WITH RECURSIVE up AS (
    SELECT x.id, x.parent_id, x.code, x.level, 0 AS depth FROM serial x WHERE x.id = sqlc.arg(id)
  UNION ALL
    SELECT s.id, s.parent_id, s.code, s.level, up.depth + 1
    FROM serial s JOIN up ON s.id = up.parent_id
)
SELECT code, level FROM up WHERE depth > 0 ORDER BY depth;

-- name: CountUnitsUnder :one
WITH RECURSIVE tree AS (
    SELECT x.id, x.level FROM serial x WHERE x.parent_id = $1
  UNION ALL
    SELECT c.id, c.level FROM serial c JOIN tree t ON c.parent_id = t.id
)
SELECT COUNT(*)::int FROM tree WHERE level = 'UNIT';

-- name: ListSerialEvents :many
SELECT e.from_status, e.to_status, l.code AS location_code, e.ref_type, e.ref_id, e.created_at
FROM serial_event e
LEFT JOIN location l ON l.id = e.location_id
WHERE e.serial_id = $1
ORDER BY e.id;

-- name: ListContainerTree :many
-- A container and everything packed under it, locked in id order (global lock order).
WITH RECURSIVE tree AS (
    SELECT x.id FROM serial x WHERE x.code = $1
  UNION ALL
    SELECT c.id FROM serial c JOIN tree t ON c.parent_id = t.id
)
SELECT s.id, s.level, s.status, s.location_id, s.sku_id
FROM serial s
JOIN tree t ON t.id = s.id
ORDER BY s.id
FOR UPDATE OF s;

-- name: MoveSerials :execrows
-- Moves many serials of one status at once. The status guard makes a stale caller affect fewer rows.
UPDATE serial
SET status      = sqlc.arg(to_status)::serial_status,
    location_id = sqlc.arg(location_id),
    partner_id  = COALESCE(sqlc.narg(partner_id), partner_id),
    updated_at  = now()
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND status = sqlc.arg(from_status);

-- name: InsertSerialEvents :exec
INSERT INTO serial_event (serial_id, from_status, to_status, location_id, partner_id, ref_type, ref_id)
SELECT t.id, sqlc.arg(from_status)::serial_status, sqlc.arg(to_status)::serial_status,
       sqlc.arg(location_id), sqlc.narg(partner_id), sqlc.arg(ref_type)::text, sqlc.arg(ref_id)::text
FROM unnest(sqlc.arg(ids)::bigint[]) AS t(id);

-- name: GetLocationByID :one
SELECT * FROM location WHERE id = $1;
