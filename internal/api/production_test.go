package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// T26: the production endpoints keep their contract.
func TestIntegration_ProductionHTTP(t *testing.T) {
	s := newServer(t, 0)
	testdb.InsertFactory(t, s.pool, "FACTORY-01")
	expires := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	body := func(batch string, pallets string, location string) string {
		return `{"sku_code":"MGN-PROT-500","batch_code":"` + batch + `","location_code":"` + location + `","expires_on":"` + expires + `","pallets":` + pallets + `}`
	}

	status, created := s.post(t, "/v1/production/batches", "", body("B-1", "1", "FACTORY-01"))
	require.Equal(t, http.StatusCreated, status)
	assert.EqualValues(t, 480, created["units"])
	pallet := created["pallets"].([]any)[0].(map[string]any)["code"].(string)

	t.Run("trace a pallet", func(t *testing.T) {
		status, trace := s.get(t, "/v1/serials/"+pallet)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "PALLET", trace["level"])
		assert.Equal(t, "PRODUCED", trace["status"])
		assert.EqualValues(t, 480, trace["units"])
	})

	t.Run("errors", func(t *testing.T) {
		status, out := s.post(t, "/v1/production/batches", "", body("B-1", "1", "FACTORY-01"))
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "BATCH_EXISTS", errorCode(t, out))

		for name, b := range map[string]string{
			"no pallets":       body("B-2", "0", "FACTORY-01"),
			"too many pallets": body("B-2", "11", "FACTORY-01"),
			"not a factory":    body("B-2", "1", "WH-JKT"),
			"empty batch code": body("", "1", "FACTORY-01"),
			"unknown field":    `{"sku_code":"MGN-PROT-500","nope":1}`,
		} {
			status, _ := s.post(t, "/v1/production/batches", "", b)
			assert.Equal(t, http.StatusUnprocessableEntity, status, name)
		}

		status, out = s.get(t, "/v1/serials/MG00000000000")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "SERIAL_NOT_FOUND", errorCode(t, out))

		status, _ = s.get(t, "/v1/serials/not-a-code")
		assert.Equal(t, http.StatusUnprocessableEntity, status)
	})
}

func TestIntegration_ShipmentHTTP(t *testing.T) {
	s := newServer(t, 0)
	testdb.InsertFactory(t, s.pool, "FACTORY-01")
	testdb.InsertStore(t, s.pool, "STORE-BDG-01", 0)
	expires := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	_, created := s.post(t, "/v1/production/batches", "", `{"sku_code":"MGN-PROT-500","batch_code":"B-1","location_code":"FACTORY-01","expires_on":"`+expires+`","pallets":1}`)
	carton := created["pallets"].([]any)[0].(map[string]any)["cartons"].([]any)[0].(string)

	status, out := s.post(t, "/v1/shipments/receive", "", `{"container_code":"`+carton+`","location_code":"WH-JKT"}`)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "IN_WAREHOUSE", out["status"])
	assert.EqualValues(t, 12, out["units"])

	status, out = s.post(t, "/v1/shipments/receive", "", `{"container_code":"`+carton+`","location_code":"WH-JKT"}`)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "INVALID_SERIAL_STATUS", errorCode(t, out))

	status, out = s.post(t, "/v1/shipments/ship", "", `{"container_code":"`+carton+`","to_location_code":"STORE-BDG-01"}`)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "IN_TRANSIT", out["status"])

	status, out = s.post(t, "/v1/shipments/receive", "", `{"container_code":"`+carton+`","location_code":"WH-JKT"}`)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "WRONG_LOCATION", errorCode(t, out), "an in-transit container is not received by a location it was not sent to")

	status, out = s.post(t, "/v1/shipments/ship", "", `{"container_code":"MG00000000000","to_location_code":"WH-JKT"}`)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "SERIAL_NOT_FOUND", errorCode(t, out))
}
