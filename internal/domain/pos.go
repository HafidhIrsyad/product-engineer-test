package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// PosStatus is the result reported per transaction in a sync response (§13.2).
type PosStatus string

const (
	PosAccepted           PosStatus = "ACCEPTED"
	PosAcceptedWithIssues PosStatus = "ACCEPTED_WITH_ISSUES"
	PosDuplicate          PosStatus = "DUPLICATE"
	PosRejected           PosStatus = "REJECTED"
)

// PosIssue is why a scanned serial is not a valid sale at this store. The sale is still
// recorded: money was taken and the unit left the shelf (§13.2).
type PosIssue string

const (
	IssueSerialNotFound PosIssue = "SERIAL_NOT_FOUND"
	IssueNotAUnit       PosIssue = "SERIAL_NOT_A_UNIT"
	IssueSKUMismatch    PosIssue = "SERIAL_SKU_MISMATCH"
	IssueNotAtStore     PosIssue = "SERIAL_NOT_AT_STORE"
	IssueNotInStock     PosIssue = "SERIAL_NOT_IN_STOCK"
)

// PosRejection is why a transaction is technically invalid. The device must not retry it.
type PosRejection string

const (
	RejectInvalidPayload PosRejection = "INVALID_PAYLOAD"
	RejectUnknownSKU     PosRejection = "UNKNOWN_SKU"
	RejectIDReused       PosRejection = "ID_REUSED_WITH_DIFFERENT_PAYLOAD"
	RejectLocalSeqReused PosRejection = "LOCAL_SEQ_REUSED"
)

// Validate checks a transaction is technically well formed. Problems with the serials themselves
// are not checked here: they never reject a sale (see PosIssue).
func (t PosTxn) Validate() error {
	switch {
	case t.ID == uuid.Nil:
		return fmt.Errorf("%w: transaction id is required", ErrInvalidInput)
	case t.LocalSeq < 0:
		return fmt.Errorf("%w: local_seq must not be negative", ErrInvalidInput)
	case t.PaymentMethod != "CASH" && t.PaymentMethod != "CARD" && t.PaymentMethod != "QRIS":
		return fmt.Errorf("%w: payment_method must be CASH, CARD or QRIS", ErrInvalidInput)
	case t.TotalAmount < 0:
		return fmt.Errorf("%w: total_amount must not be negative", ErrInvalidInput)
	case len(t.Items) == 0:
		return fmt.Errorf("%w: a transaction needs at least one item", ErrInvalidInput)
	}
	for i, it := range t.Items {
		if it.SerialCode == "" || it.SKUCode == "" || it.Price < 0 {
			return fmt.Errorf("%w: item %d needs serial_code, sku_code and a non-negative price", ErrInvalidInput, i+1)
		}
	}
	return nil
}

// SerialSnapshot is what the database says about a scanned serial, at lock time.
type SerialSnapshot struct {
	Level      SerialLevel
	SKUID      int64
	LocationID int64
	Status     SerialStatus
}

// ClassifyPosItem decides whether a scanned serial is a valid sale at a store (§13.3).
// A nil snapshot means the code is not in the database. It returns "" when valid.
func ClassifyPosItem(s *SerialSnapshot, wantSKUID, storeID int64) PosIssue {
	switch {
	case s == nil:
		return IssueSerialNotFound
	case s.Level != LevelUnit:
		return IssueNotAUnit
	case s.SKUID != wantSKUID:
		return IssueSKUMismatch
	case s.LocationID != storeID:
		return IssueNotAtStore
	case s.Status != SerialInStock:
		return IssueNotInStock
	}
	return ""
}
