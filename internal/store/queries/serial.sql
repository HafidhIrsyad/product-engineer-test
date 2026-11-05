-- name: GetSerialForUpdate :one
SELECT s.id, s.level, s.status, s.sku_id, s.location_id, l.type AS location_type
FROM serial s
JOIN location l ON l.id = s.location_id
WHERE s.code = $1
FOR UPDATE OF s;

-- name: SetSerialsOrder :exec
UPDATE serial SET order_id = sqlc.arg(order_id), updated_at = now() WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: ListOrderSerialCodes :many
SELECT code FROM serial WHERE order_id = $1 ORDER BY code;
