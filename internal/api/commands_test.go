package api_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s server) unitCodes(t *testing.T) []string {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT code FROM serial WHERE level = 'UNIT' ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		codes = append(codes, c)
	}
	return codes
}

// T47: the status codes and error codes of the warehouse, order and command-center endpoints.
func TestIntegration_OperationsHTTP(t *testing.T) {
	s := newServer(t, 5)
	codes := s.unitCodes(t)
	expect := func(status int, path, body, code string) {
		t.Helper()
		got, out := s.post(t, path, "", body)
		assert.Equal(t, status, got, path)
		if code != "" {
			assert.Equal(t, code, errorCode(t, out), path)
		}
	}

	t.Run("quarantine and release", func(t *testing.T) {
		expect(http.StatusUnprocessableEntity, "/v1/serials/"+codes[0]+"/quarantine", `{"reason":""}`, "INVALID_INPUT")
		expect(http.StatusUnprocessableEntity, "/v1/serials/bad/quarantine", `{"reason":"x"}`, "INVALID_INPUT")
		expect(http.StatusNotFound, "/v1/serials/MG00000000000/quarantine", `{"reason":"x"}`, "SERIAL_NOT_FOUND")
		status, out := s.post(t, "/v1/serials/"+codes[0]+"/quarantine", "", `{"reason":"seal broken"}`)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "QUARANTINE", out["status"])
		expect(http.StatusConflict, "/v1/serials/"+codes[0]+"/quarantine", `{"reason":"again"}`, "INVALID_SERIAL_STATUS")
		status, out = s.post(t, "/v1/serials/"+codes[0]+"/release", "", ``)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "IN_WAREHOUSE", out["status"])
	})

	t.Run("fulfill", func(t *testing.T) {
		status, order := s.post(t, "/v1/orders", uuid.NewString(), orderBody) // 2 units
		require.Equal(t, http.StatusCreated, status)
		path := "/v1/orders/" + jsonNumber(order["id"])
		fulfill := `{"serial_codes":["` + codes[1] + `","` + codes[2] + `"]}`

		expect(http.StatusConflict, path+"/fulfill", fulfill, "INVALID_ORDER_STATUS")
		expect(http.StatusNotFound, "/v1/orders/9999/fulfill", fulfill, "ORDER_NOT_FOUND")
		expect(http.StatusConflict, path+"/reallocate", `{"location_code":"WH-JKT"}`, "INVALID_ORDER_STATUS")
		expect(http.StatusOK, path+"/pay", ``, "")

		expect(http.StatusUnprocessableEntity, path+"/fulfill", `{"serial_codes":["`+codes[1]+`"]}`, "INVALID_INPUT")
		expect(http.StatusUnprocessableEntity, path+"/fulfill", `{"serial_codes":["nope"]}`, "INVALID_INPUT")
		status, out := s.post(t, path+"/fulfill", "", fulfill)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "FULFILLED", out["status"])
		status, _ = s.post(t, path+"/fulfill", "", fulfill)
		assert.Equal(t, http.StatusOK, status, "the same units again")
	})

	t.Run("an Idempotency-Key reused for another request", func(t *testing.T) {
		key := uuid.NewString()
		status, _ := s.post(t, "/v1/orders", key, orderBody)
		require.Equal(t, http.StatusCreated, status)
		status, out := s.post(t, "/v1/orders", key, `{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":1}]}`)
		assert.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", errorCode(t, out))
	})

	t.Run("command center", func(t *testing.T) {
		status, out := s.get(t, "/v1/stock?location=WH-JKT")
		assert.Equal(t, http.StatusOK, status)
		assert.Len(t, out["stock"], 1)
		status, out = s.get(t, "/v1/stock?sku=NOPE")
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, out["stock"])

		status, _ = s.get(t, "/v1/anomalies")
		assert.Equal(t, http.StatusOK, status)
		status, out = s.get(t, "/v1/anomalies?type=NOPE")
		assert.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Equal(t, "INVALID_INPUT", errorCode(t, out))
		status, _ = s.get(t, "/v1/anomalies?since=yesterday")
		assert.Equal(t, http.StatusUnprocessableEntity, status)

		status, _ = s.get(t, "/v1/reports/leakage")
		assert.Equal(t, http.StatusOK, status)
		status, _ = s.get(t, "/v1/reports/sell-out-signal")
		assert.Equal(t, http.StatusOK, status)
		status, _ = s.get(t, "/v1/reports/sell-out-signal?from=2026-12-01T00:00:00Z&to=2026-11-01T00:00:00Z")
		assert.Equal(t, http.StatusUnprocessableEntity, status)

		status, out = s.get(t, "/v1/pos/gaps?store=NOPE")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "STORE_NOT_FOUND", errorCode(t, out))
	})

	t.Run("points", func(t *testing.T) {
		expect(http.StatusUnprocessableEntity, "/v1/points/claims", `{"member_id":0,"serial_code":"`+codes[3]+`","inner_code":"XXXXXXXXXXXX"}`, "INVALID_INPUT")
		expect(http.StatusUnprocessableEntity, "/v1/points/claims", `{"member_id":1,"serial_code":"`+codes[3]+`","inner_code":"WRONG1234567"}`, "PROOF_REJECTED")
		status, out := s.get(t, "/v1/points/members/1")
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, out["claims"])
		status, _ = s.get(t, "/v1/points/members/abc")
		assert.Equal(t, http.StatusUnprocessableEntity, status)
	})
}

func jsonNumber(v any) string { return strconv.FormatInt(int64(v.(float64)), 10) }
