package domain

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PosTxn is one cashier transaction recorded on a POS device, possibly while offline (§13.2).
// ID is created on the device when the sale is made and is the idempotency key.
// The JSON tags match the sync request body; api decodes straight into this type.
type PosTxn struct {
	ID            uuid.UUID `json:"id"`
	LocalSeq      int64     `json:"local_seq"`
	OccurredAt    time.Time `json:"occurred_at"`
	PaymentMethod string    `json:"payment_method"`
	TotalAmount   int64     `json:"total_amount"`
	Items         []PosItem `json:"items"`
}

// PosItem is one scanned unit. Its line number is its 1-based position in Items.
type PosItem struct {
	SerialCode string `json:"serial_code"`
	SKUCode    string `json:"sku_code"`
	Price      int64  `json:"price"`
}

// canonicalTxn fixes field order and time zone so the same sale always hashes the same.
type canonicalTxn struct {
	ID            string          `json:"id"`
	LocalSeq      int64           `json:"local_seq"`
	OccurredAt    string          `json:"occurred_at"`
	PaymentMethod string          `json:"payment_method"`
	TotalAmount   int64           `json:"total_amount"`
	Items         []canonicalItem `json:"items"`
}

type canonicalItem struct {
	LineNo     int    `json:"line_no"`
	SerialCode string `json:"serial_code"`
	SKUCode    string `json:"sku_code"`
	Price      int64  `json:"price"`
}

// PayloadHash returns the SHA-256 of the canonical form of a transaction (§13.4).
// It is computed from the decoded struct, not the raw body, so field order, spacing and the
// time-zone offset of a retried request do not matter. The same UUID with a different hash
// means a device bug: the transaction is REJECTED.
func PayloadHash(t PosTxn) []byte {
	c := canonicalTxn{
		ID:            t.ID.String(),
		LocalSeq:      t.LocalSeq,
		OccurredAt:    t.OccurredAt.UTC().Format(time.RFC3339Nano),
		PaymentMethod: t.PaymentMethod,
		TotalAmount:   t.TotalAmount,
		Items:         make([]canonicalItem, len(t.Items)),
	}
	for i, it := range t.Items {
		c.Items[i] = canonicalItem{LineNo: i + 1, SerialCode: it.SerialCode, SKUCode: it.SKUCode, Price: it.Price}
	}
	b, err := json.Marshal(c)
	if err != nil {
		// Unreachable: canonicalTxn holds only strings and integers.
		panic("domain: marshal canonical transaction: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return sum[:]
}
