package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

func syncBody(txnID, serial string, seq int) string {
	return fmt.Sprintf(`{"store_code":"STORE-BDG-01","device_id":"pos-a","transactions":[{
		"id":%q,"local_seq":%d,"occurred_at":"2026-11-05T09:15:22+07:00","payment_method":"CASH","total_amount":450000,
		"items":[{"serial_code":%q,"sku_code":"MGN-PROT-500","price":450000}]}]}`, txnID, seq, serial)
}

// HTTP contract of the POS endpoints (ARCHITECTURE.md §13.2, §15).
func TestIntegration_PosHTTP(t *testing.T) {
	s := newServer(t, 1) // also creates warehouse WH-JKT and SKU MGN-PROT-500
	store := testdb.InsertStore(t, s.pool, "STORE-BDG-01", 0)
	serials := testdb.SetStockAs(t, s.pool, 1, store, 2, "IN_STOCK")

	t.Run("heartbeat", func(t *testing.T) {
		status, _ := s.postRaw(t, "/v1/pos/heartbeat", "", `{"store_code":"STORE-BDG-01","device_id":"pos-a"}`)
		assert.Equal(t, http.StatusNoContent, status)

		status, body := s.post(t, "/v1/pos/heartbeat", "", `{"store_code":"NOPE","device_id":"pos-a"}`)
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "STORE_NOT_FOUND", errorCode(t, body))

		status, body = s.post(t, "/v1/pos/heartbeat", "", `{"store_code":"WH-JKT","device_id":"pos-a"}`)
		assert.Equal(t, http.StatusNotFound, status, "a warehouse has no POS")
		assert.Equal(t, "STORE_NOT_FOUND", errorCode(t, body))

		status, _ = s.post(t, "/v1/pos/heartbeat", "", `{"store_code":"STORE-BDG-01"}`)
		assert.Equal(t, http.StatusUnprocessableEntity, status)
	})

	t.Run("sync returns one result per transaction, and a retry is DUPLICATE", func(t *testing.T) {
		id := uuid.NewString()
		body := syncBody(id, serials[0], 1)

		status, out := s.post(t, "/v1/pos/sync", "", body)
		require.Equal(t, http.StatusOK, status)
		results := out["results"].([]any)
		require.Len(t, results, 1)
		assert.Equal(t, "ACCEPTED", results[0].(map[string]any)["status"])

		status, out = s.post(t, "/v1/pos/sync", "", body)
		require.Equal(t, http.StatusOK, status)
		first := out["results"].([]any)[0].(map[string]any)
		assert.Equal(t, "DUPLICATE", first["status"])
		assert.Equal(t, "ACCEPTED", first["original_result"])
	})

	t.Run("a bad serial is still HTTP 200, with an issue", func(t *testing.T) {
		status, out := s.post(t, "/v1/pos/sync", "", syncBody(uuid.NewString(), "MG99999999999", 2))
		require.Equal(t, http.StatusOK, status)
		first := out["results"].([]any)[0].(map[string]any)
		assert.Equal(t, "ACCEPTED_WITH_ISSUES", first["status"])
		issue := first["issues"].([]any)[0].(map[string]any)
		assert.Equal(t, "SERIAL_NOT_FOUND", issue["issue"])
	})

	t.Run("a malformed envelope is 422", func(t *testing.T) {
		for name, body := range map[string]string{
			"broken JSON":     `{`,
			"no transactions": `{"store_code":"STORE-BDG-01","device_id":"pos-a","transactions":[]}`,
			"no device_id":    `{"store_code":"STORE-BDG-01","transactions":[{}]}`,
			"unknown field":   `{"store_code":"STORE-BDG-01","device_id":"pos-a","transactions":[{}],"x":1}`,
		} {
			status, _ := s.post(t, "/v1/pos/sync", "", body)
			assert.Equal(t, http.StatusUnprocessableEntity, status, name)
		}
	})

	t.Run("input limits and strict bodies are 422", func(t *testing.T) {
		huge := `{"store_code":"STORE-BDG-01","device_id":"` + strings.Repeat("x", 2<<20) + `","transactions":[{}]}`
		status, _ := s.post(t, "/v1/pos/sync", "", huge)
		assert.Equal(t, http.StatusUnprocessableEntity, status, "a body over 1 MiB is refused")

		many := strings.Repeat(`{},`, 600)
		status, _ = s.post(t, "/v1/pos/sync", "", `{"store_code":"STORE-BDG-01","device_id":"pos-a","transactions":[`+many+`{}]}`)
		assert.Equal(t, http.StatusUnprocessableEntity, status, "more than 500 transactions per sync")

		status, _ = s.post(t, "/v1/pos/heartbeat", "", `{"store_code":"STORE-BDG-01","device_id":"pos-a"}{"x":1}`)
		assert.Equal(t, http.StatusUnprocessableEntity, status, "trailing data after the JSON object")
	})

	t.Run("an unknown store is 404", func(t *testing.T) {
		status, body := s.post(t, "/v1/pos/sync", "", `{"store_code":"NOPE","device_id":"pos-a","transactions":[{"id":"`+uuid.NewString()+`"}]}`)
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "STORE_NOT_FOUND", errorCode(t, body))
	})
}
