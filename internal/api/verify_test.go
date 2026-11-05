package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

const (
	jakartaCoords = `"lat":-6.2088,"lng":106.8456`
	medanCoords   = `"lat":3.5952,"lng":98.6722`
)

func verifyBody(code, device, coords string) string {
	if coords == "" {
		return fmt.Sprintf(`{"code":%q,"device_id":%q}`, code, device)
	}
	return fmt.Sprintf(`{"code":%q,"device_id":%q,%s}`, code, device, coords)
}

func (s server) scalar(t *testing.T, query string, args ...any) string {
	t.Helper()
	var v string
	require.NoError(t, s.pool.QueryRow(context.Background(), query, args...).Scan(&v))
	return v
}

// T12: POST /v1/verify end to end, against a real database (ARCHITECTURE.md §10, §16.2).
func TestIntegration_VerifyHTTP(t *testing.T) {
	s := newServer(t, 1) // warehouse WH-JKT (id 1) and SKU MGN-PROT-500 (id 1)
	sold := func() string {
		_, code := testdb.InsertSerial(t, s.pool, testdb.SerialSpec{SKUID: 1, LocationID: 1, Status: "SOLD"})
		return code
	}

	t.Run("first scan after the sale verifies the unit", func(t *testing.T) {
		code := sold()
		status, body := s.post(t, "/v1/verify", "", verifyBody(code, "d1", jakartaCoords))
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, "GENUINE", body["verdict"])
		assert.EqualValues(t, 1, body["scan_count"])
		assert.NotEmpty(t, body["first_verified_at"])
		assert.Equal(t, "MGN-PROT-500", body["product"].(map[string]any)["sku"])
		assert.Equal(t, "VERIFIED", s.scalar(t, `SELECT status FROM serial WHERE code = $1`, code))

		status, body = s.post(t, "/v1/verify", "", verifyBody(code, "d2", jakartaCoords))
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, "GENUINE", body["verdict"], "a second device is not suspicious yet")
		assert.EqualValues(t, 2, body["scan_count"])

		_, body = s.post(t, "/v1/verify", "", verifyBody(code, "d3", jakartaCoords))
		assert.Equal(t, "GENUINE_WITH_WARNING", body["verdict"], "three different devices")
		assert.NotEmpty(t, body["message"])
	})

	t.Run("the same device scanning again is not suspicious", func(t *testing.T) {
		code := sold()
		for range 5 {
			_, body := s.post(t, "/v1/verify", "", verifyBody(code, "d1", jakartaCoords))
			assert.Equal(t, "GENUINE", body["verdict"])
		}
	})

	t.Run("a clone seen far away moments later is a warning and an anomaly, with no rule shown", func(t *testing.T) {
		code := sold()
		s.post(t, "/v1/verify", "", verifyBody(code, "d1", jakartaCoords))
		status, raw := s.postRaw(t, "/v1/verify", "", verifyBody(code, "d2", medanCoords))
		require.Equal(t, http.StatusOK, status)
		assert.Contains(t, string(raw), "GENUINE_WITH_WARNING")
		assert.NotContains(t, string(raw), "IMPOSSIBLE", "the consumer must not learn which rule fired")
		assert.NotContains(t, string(raw), "anomal")
		assert.Equal(t, "1", s.scalar(t, `SELECT COUNT(*)::text FROM anomaly a JOIN serial x ON x.id = a.serial_id WHERE x.code = $1 AND a.type = 'IMPOSSIBLE_TRAVEL'`, code))
	})

	t.Run("a unit still in the warehouse is a warning and does not change status", func(t *testing.T) {
		_, code := testdb.InsertSerial(t, s.pool, testdb.SerialSpec{SKUID: 1, LocationID: 1, Status: "IN_WAREHOUSE"})
		_, body := s.post(t, "/v1/verify", "", verifyBody(code, "d1", jakartaCoords))
		assert.Equal(t, "GENUINE_WITH_WARNING", body["verdict"])
		assert.Equal(t, "IN_WAREHOUSE", s.scalar(t, `SELECT status FROM serial WHERE code = $1`, code))
		assert.Equal(t, "1", s.scalar(t, `SELECT COUNT(*)::text FROM anomaly WHERE type = 'SCAN_BEFORE_RELEASE'`))
	})

	t.Run("a shelf unit at a partner scanned outside its region: warning, but still on the shelf", func(t *testing.T) {
		var partner, loc int64
		require.NoError(t, s.pool.QueryRow(context.Background(), `
			INSERT INTO partner (name, region_name, region_lat, region_lng, region_radius_km)
			VALUES ('Distributor Medan', 'Sumatera Utara', 3.5952, 98.6722, 150) RETURNING id`).Scan(&partner))
		require.NoError(t, s.pool.QueryRow(context.Background(), `
			INSERT INTO location (code, name, type, partner_id, lat, lng)
			VALUES ('DIST-MDN', 'Gudang Medan', 'PARTNER', $1, 3.5952, 98.6722) RETURNING id`, partner).Scan(&loc))
		_, code := testdb.InsertSerial(t, s.pool, testdb.SerialSpec{SKUID: 1, LocationID: loc, Status: "IN_STOCK", PartnerID: &partner})

		_, body := s.post(t, "/v1/verify", "", verifyBody(code, "d1", jakartaCoords))
		assert.Equal(t, "GENUINE_WITH_WARNING", body["verdict"])
		assert.Equal(t, "IN_STOCK", s.scalar(t, `SELECT status FROM serial WHERE code = $1`, code), "an outer scan never marks a unit sold")
		assert.Equal(t, fmt.Sprint(partner), s.scalar(t, `SELECT partner_id::text FROM anomaly WHERE type = 'OUT_OF_REGION'`), "the anomaly is tied to the distributor")

		_, body = s.post(t, "/v1/verify", "", verifyBody(code, "d1", medanCoords))
		assert.Equal(t, "GENUINE", body["verdict"], "inside its region the same unit is fine")
	})

	t.Run("an unknown code is NOT_FOUND, logged as a scan only", func(t *testing.T) {
		status, body := s.post(t, "/v1/verify", "", verifyBody("MG99999999999", "d1", ""))
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, "NOT_FOUND", body["verdict"])
		assert.Equal(t, "1", s.scalar(t, `SELECT COUNT(*)::text FROM scan_event WHERE serial_id IS NULL AND verdict = 'NOT_FOUND'`))
		assert.Equal(t, "0", s.scalar(t, `SELECT COUNT(*)::text FROM anomaly WHERE serial_id IS NULL`), "unknown codes never flood the anomaly table")
	})

	t.Run("location is optional", func(t *testing.T) {
		status, body := s.post(t, "/v1/verify", "", verifyBody(sold(), "d1", ""))
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "GENUINE", body["verdict"])
	})

	t.Run("invalid input is 422", func(t *testing.T) {
		for name, body := range map[string]string{
			"broken JSON":      `{`,
			"bad code format":  verifyBody("hello", "d1", ""),
			"no device":        `{"code":"MG7K2Q9XHD4PZ"}`,
			"lat without lng":  `{"code":"MG7K2Q9XHD4PZ","device_id":"d1","lat":-6.2}`,
			"lat out of range": verifyBody("MG7K2Q9XHD4PZ", "d1", `"lat":95,"lng":10`),
		} {
			status, _ := s.post(t, "/v1/verify", "", body)
			assert.Equal(t, http.StatusUnprocessableEntity, status, name)
		}
	})
}

// Two devices scanning a freshly sold unit at the same moment: exactly one is "first".
func TestIntegration_VerifyHTTP_FirstScanOnce(t *testing.T) {
	s := newServer(t, 1)
	_, code := testdb.InsertSerial(t, s.pool, testdb.SerialSpec{SKUID: 1, LocationID: 1, Status: "SOLD"})

	done := make(chan struct{}, 6)
	for i := range 6 {
		go func() {
			defer func() { done <- struct{}{} }()
			status, _ := s.post(t, "/v1/verify", "", verifyBody(code, fmt.Sprintf("d%d", i), jakartaCoords))
			assert.Equal(t, http.StatusOK, status)
		}()
	}
	for range 6 {
		<-done
	}
	assert.Equal(t, "1", s.scalar(t, `SELECT COUNT(*)::text FROM serial_event e JOIN serial x ON x.id = e.serial_id WHERE x.code = $1 AND e.to_status = 'VERIFIED'`, code),
		"the SOLD → VERIFIED move happens exactly once")
	assert.Equal(t, "6", s.scalar(t, `SELECT COUNT(*)::text FROM scan_event WHERE raw_code = $1`, code))
}
