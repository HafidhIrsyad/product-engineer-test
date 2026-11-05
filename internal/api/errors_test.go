package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

// T19: every sentinel error maps to one HTTP status and code, in one place.
func TestClassify(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrInvalidInput, http.StatusUnprocessableEntity, "INVALID_INPUT"},
		{fmt.Errorf("%w: qty must be positive", domain.ErrInvalidInput), http.StatusUnprocessableEntity, "INVALID_INPUT"},
		{domain.ErrUnknownSKU, http.StatusUnprocessableEntity, "UNKNOWN_SKU"},
		{domain.ErrUnknownLocation, http.StatusUnprocessableEntity, "UNKNOWN_LOCATION"},
		{domain.ErrOrderNotFound, http.StatusNotFound, "ORDER_NOT_FOUND"},
		{domain.ErrStoreNotFound, http.StatusNotFound, "STORE_NOT_FOUND"},
		{domain.ErrInsufficientStock, http.StatusConflict, "INSUFFICIENT_STOCK"},
		{domain.ErrOrderExpired, http.StatusConflict, "ORDER_EXPIRED"},
		{domain.ErrInvalidOrderStatus, http.StatusConflict, "INVALID_ORDER_STATUS"},
		{domain.ErrOrderNotCancellable, http.StatusConflict, "ORDER_NOT_CANCELLABLE"},
		{domain.ErrInvalidTransition, http.StatusConflict, "INVALID_SERIAL_STATUS"},
		{domain.ErrStaleStatus, http.StatusConflict, "SERIAL_STATUS_CHANGED"},
		{domain.ErrDeviceStoreMismatch, http.StatusConflict, "DEVICE_STORE_MISMATCH"},
		{errors.New("connection refused"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			status, code := classify(tt.err)
			assert.Equal(t, tt.status, status)
			assert.Equal(t, tt.code, code)
		})
	}
}
