-- +goose Up
-- paid_at: an order that loses its stock to a physical sale must come back as PAID if it was paid.
-- request_hash: the same Idempotency-Key with a different request is refused, like POS sync does.
ALTER TABLE orders
    ADD COLUMN paid_at      TIMESTAMPTZ,
    ADD COLUMN request_hash BYTEA NOT NULL DEFAULT '';
