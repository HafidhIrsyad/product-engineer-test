package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

func decodeTxn(t *testing.T, body string) domain.PosTxn {
	t.Helper()
	var txn domain.PosTxn
	require.NoError(t, json.Unmarshal([]byte(body), &txn))
	return txn
}

const baseTxn = `{
  "id": "0193a5f2-7c1e-7b3a-9d2e-4f6a8b1c2d3e",
  "local_seq": 1042,
  "occurred_at": "2026-11-05T09:15:22+07:00",
  "payment_method": "CASH",
  "total_amount": 450000,
  "items": [
    { "serial_code": "MG7K2Q9XHD4PZ", "sku_code": "MGN-PROT-500", "price": 450000 }
  ]
}`

// T17: the same sale hashes the same however the JSON is written; a different sale does not.
func TestPayloadHash(t *testing.T) {
	base := domain.PayloadHash(decodeTxn(t, baseTxn))
	require.Len(t, base, 32, "SHA-256")

	t.Run("field order, spacing and key order do not matter", func(t *testing.T) {
		reordered := `{"items":[{"price":450000,"sku_code":"MGN-PROT-500","serial_code":"MG7K2Q9XHD4PZ"}],
			"total_amount":450000,"payment_method":"CASH","occurred_at":"2026-11-05T09:15:22+07:00",
			"local_seq":1042,"id":"0193a5f2-7c1e-7b3a-9d2e-4f6a8b1c2d3e"}`
		assert.Equal(t, base, domain.PayloadHash(decodeTxn(t, reordered)))
	})

	t.Run("the same instant in another time zone offset is the same payload", func(t *testing.T) {
		utc := `{"id":"0193a5f2-7c1e-7b3a-9d2e-4f6a8b1c2d3e","local_seq":1042,"occurred_at":"2026-11-05T02:15:22Z",
			"payment_method":"CASH","total_amount":450000,
			"items":[{"serial_code":"MG7K2Q9XHD4PZ","sku_code":"MGN-PROT-500","price":450000}]}`
		assert.Equal(t, base, domain.PayloadHash(decodeTxn(t, utc)))
	})

	t.Run("one different item means a different hash", func(t *testing.T) {
		other := `{"id":"0193a5f2-7c1e-7b3a-9d2e-4f6a8b1c2d3e","local_seq":1042,"occurred_at":"2026-11-05T09:15:22+07:00",
			"payment_method":"CASH","total_amount":450000,
			"items":[{"serial_code":"MG7K2Q9XHD4PY","sku_code":"MGN-PROT-500","price":450000}]}`
		assert.NotEqual(t, base, domain.PayloadHash(decodeTxn(t, other)))
	})

	t.Run("an extra item means a different hash", func(t *testing.T) {
		txn := decodeTxn(t, baseTxn)
		txn.Items = append(txn.Items, domain.PosItem{SerialCode: "MG7K2Q9XHD4PY", SKUCode: "MGN-PROT-500", Price: 450000})
		assert.NotEqual(t, base, domain.PayloadHash(txn))
	})

	t.Run("a different amount or sequence means a different hash", func(t *testing.T) {
		amount := decodeTxn(t, baseTxn)
		amount.TotalAmount++
		assert.NotEqual(t, base, domain.PayloadHash(amount))

		seq := decodeTxn(t, baseTxn)
		seq.LocalSeq++
		assert.NotEqual(t, base, domain.PayloadHash(seq))
	})

	t.Run("item order is part of the payload", func(t *testing.T) {
		a := domain.PosItem{SerialCode: "MG7K2Q9XHD4PZ", SKUCode: "MGN-PROT-500", Price: 1}
		b := domain.PosItem{SerialCode: "MG7K2Q9XHD4PY", SKUCode: "MGN-PROT-500", Price: 1}
		first := decodeTxn(t, baseTxn)
		first.Items = []domain.PosItem{a, b}
		second := decodeTxn(t, baseTxn)
		second.Items = []domain.PosItem{b, a}
		assert.NotEqual(t, domain.PayloadHash(first), domain.PayloadHash(second))
	})
}
