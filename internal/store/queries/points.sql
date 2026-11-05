-- name: SetInnerCodeHashes :exec
UPDATE serial s
SET inner_code_hash = h.hash
FROM unnest(sqlc.arg(ids)::bigint[]) WITH ORDINALITY AS i(id, n)
JOIN unnest(sqlc.arg(hashes)::bytea[]) WITH ORDINALITY AS h(hash, n) ON h.n = i.n
WHERE s.id = i.id;

-- name: CountRecentClaims :one
SELECT COUNT(*)::int FROM point_claim WHERE member_id = $1 AND claimed_at > now() - interval '24 hours';

-- name: InsertPointClaim :one
INSERT INTO point_claim (serial_id, member_id, proof_type, points, status, available_at, expires_on)
VALUES (sqlc.arg(serial_id), sqlc.arg(member_id), 'INNER_CODE', sqlc.arg(points), sqlc.arg(status),
        now() + sqlc.arg(wait_hours)::int * interval '1 hour', current_date + 365)
RETURNING id, available_at;

-- name: ListMemberClaims :many
-- PENDING becomes AVAILABLE once the review window has passed.
SELECT c.id, s.code AS serial_code, c.points,
       (CASE WHEN c.status = 'PENDING' AND c.available_at <= now() THEN 'AVAILABLE' ELSE c.status END)::text AS status,
       c.available_at, c.claimed_at
FROM point_claim c
JOIN serial s ON s.id = c.serial_id
WHERE c.member_id = $1
ORDER BY c.id DESC;

-- name: LockMember :exec
-- Claims of one member are counted one at a time, so the velocity limit cannot be raced.
SELECT pg_advisory_xact_lock(sqlc.arg(member_id)::bigint);
