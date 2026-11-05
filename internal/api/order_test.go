package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/api"
	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/config"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

type server struct {
	url  string
	pool *pgxpool.Pool
}

// newServer starts the real router and handlers on a real database (no mocks, ARCHITECTURE.md §18.4).
func newServer(t *testing.T, stock int) server {
	t.Helper()
	pool := testdb.Pool(t)
	loc := testdb.InsertWarehouse(t, pool, "WH-JKT")
	sku := testdb.InsertSKU(t, pool, "MGN-PROT-500")
	testdb.SetStock(t, pool, sku, loc, stock)

	router := api.NewRouter(app.NewService(testdb.Store(t, pool)), zerolog.Nop(), config.HTTP{RequestTimeout: 5 * time.Second})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return server{url: srv.URL, pool: pool}
}

const orderBody = `{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":2}]}`

func (s server) post(t *testing.T, path, key, body string) (int, map[string]any) {
	t.Helper()
	status, raw := s.postRaw(t, path, key, body)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "response must be JSON: %s", raw)
	return status, out
}

func (s server) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(s.url + path)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "response must be JSON: %s", raw)
	return res.StatusCode, out
}

func (s server) postRaw(t *testing.T, path, key, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.url+path, strings.NewReader(body))
	require.NoError(t, err)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, raw
}

func errorCode(t *testing.T, body map[string]any) string {
	t.Helper()
	detail, ok := body["error"].(map[string]any)
	require.True(t, ok, "body must have an error object: %v", body)
	return detail["code"].(string)
}

// T21: HTTP contract of the order endpoints (ARCHITECTURE.md §15).
func TestIntegration_OrderHTTP(t *testing.T) {
	s := newServer(t, 3)
	key := uuid.NewString()

	t.Run("create returns 201 and the order", func(t *testing.T) {
		status, body := s.post(t, "/v1/orders", key, orderBody)
		require.Equal(t, http.StatusCreated, status)
		assert.Equal(t, "PENDING_PAYMENT", body["status"])
		assert.Equal(t, "WH-JKT", body["location_code"])
		assert.NotEmpty(t, body["expires_at"])
	})

	t.Run("same Idempotency-Key returns 200 and the same order", func(t *testing.T) {
		status, body := s.post(t, "/v1/orders", key, orderBody)
		assert.Equal(t, http.StatusOK, status)
		assert.EqualValues(t, 1, body["id"])
	})

	t.Run("not enough stock is 409", func(t *testing.T) {
		status, body := s.post(t, "/v1/orders", uuid.NewString(), orderBody) // 2 reserved, 3 on hand, asks 2 more
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "INSUFFICIENT_STOCK", errorCode(t, body))
	})

	t.Run("invalid input is 422", func(t *testing.T) {
		cases := map[string]struct{ key, body string }{
			"missing Idempotency-Key": {"", orderBody},
			"key is not a UUID":       {"abc", orderBody},
			"broken JSON":             {uuid.NewString(), `{`},
			"unknown field":           {uuid.NewString(), `{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":1}],"x":1}`},
			"bad channel":             {uuid.NewString(), `{"channel":"POS","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":1}]}`},
			"no items":                {uuid.NewString(), `{"channel":"D2C","location_code":"WH-JKT","items":[]}`},
			"qty zero":                {uuid.NewString(), `{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":0}]}`},
			"same sku twice":          {uuid.NewString(), `{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":1},{"sku_code":"MGN-PROT-500","qty":1}]}`},
			"unknown sku":             {uuid.NewString(), `{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"NOPE","qty":1}]}`},
			"unknown location":        {uuid.NewString(), `{"channel":"D2C","location_code":"NOPE","items":[{"sku_code":"MGN-PROT-500","qty":1}]}`},
		}
		for name, c := range cases {
			status, body := s.post(t, "/v1/orders", c.key, c.body)
			assert.Equal(t, http.StatusUnprocessableEntity, status, name)
			assert.NotEmpty(t, errorCode(t, body), name)
		}
	})

	t.Run("get returns the order, 404 when unknown", func(t *testing.T) {
		status, body := s.get(t, "/v1/orders/1")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "PENDING_PAYMENT", body["status"])
		assert.EqualValues(t, 1, body["id"])

		status, body = s.get(t, "/v1/orders/999")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "ORDER_NOT_FOUND", errorCode(t, body))
	})

	t.Run("pay returns 200, and 200 again", func(t *testing.T) {
		for range 2 {
			status, body := s.post(t, "/v1/orders/1/pay", "", "")
			assert.Equal(t, http.StatusOK, status)
			assert.Equal(t, "PAID", body["status"])
		}
	})

	t.Run("cancel twice returns 200 both times", func(t *testing.T) {
		for range 2 {
			status, body := s.post(t, "/v1/orders/1/cancel", "", "")
			assert.Equal(t, http.StatusOK, status)
			assert.Equal(t, "CANCELLED", body["status"])
		}
	})

	t.Run("pay after expires_at is 409 ORDER_EXPIRED", func(t *testing.T) {
		status, body := s.post(t, "/v1/orders", uuid.NewString(), orderBody)
		require.Equal(t, http.StatusCreated, status)
		id := int64(body["id"].(float64))
		_, err := s.pool.Exec(context.Background(), `UPDATE orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, id)
		require.NoError(t, err)

		status, body = s.post(t, "/v1/orders/"+strconv.FormatInt(id, 10)+"/pay", "", "")
		assert.Equal(t, http.StatusConflict, status)
		assert.Equal(t, "ORDER_EXPIRED", errorCode(t, body))
	})

	t.Run("unknown order is 404, bad id is 422", func(t *testing.T) {
		status, body := s.post(t, "/v1/orders/999/pay", "", "")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "ORDER_NOT_FOUND", errorCode(t, body))

		status, _ = s.post(t, "/v1/orders/abc/cancel", "", "")
		assert.Equal(t, http.StatusUnprocessableEntity, status)
	})
}

// While draining, /healthz fails so a load balancer stops sending traffic; other routes still answer.
func TestIntegration_HealthWhileDraining(t *testing.T) {
	pool := testdb.Pool(t)
	svc := app.NewService(testdb.Store(t, pool))
	srv := httptest.NewServer(api.NewRouter(svc, zerolog.Nop(), config.HTTP{RequestTimeout: 5 * time.Second}))
	t.Cleanup(srv.Close)
	s := server{url: srv.URL, pool: pool}

	status, body := s.get(t, "/healthz")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", body["status"])

	svc.Drain()
	status, body = s.get(t, "/healthz")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "shutting_down", body["status"])
}
