-- +goose Up
-- inner_code_hash: the code under the seal of a unit. Only its hash is stored, so a database leak
-- does not leak the proof of purchase.
ALTER TABLE serial ADD COLUMN inner_code_hash BYTEA;

-- One serial, one claim: the UNIQUE constraint is the multi-claim guard.
CREATE TABLE point_claim (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    serial_id    BIGINT NOT NULL UNIQUE REFERENCES serial(id),
    member_id    BIGINT NOT NULL,
    proof_type   TEXT   NOT NULL CHECK (proof_type IN ('INNER_CODE')),
    points       INT    NOT NULL CHECK (points > 0),
    claimed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Points can be redeemed only after this moment (the fraud review window).
    available_at TIMESTAMPTZ NOT NULL,
    expires_on   DATE   NOT NULL,
    status       TEXT   NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'HELD'))
);

CREATE INDEX idx_point_claim_member_time ON point_claim (member_id, claimed_at DESC);
