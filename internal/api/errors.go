package api

import (
	"errors"
	"net/http"

	"github.com/rs/zerolog/hlog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// classify is the ONLY place where domain errors become HTTP status codes (ARCHITECTURE.md §16.1).
// Anything it does not know is a bug or an infrastructure failure: 500 (Die).
func classify(err error) (status int, code string) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusUnprocessableEntity, "INVALID_INPUT"
	case errors.Is(err, domain.ErrUnknownSKU):
		return http.StatusUnprocessableEntity, "UNKNOWN_SKU"
	case errors.Is(err, domain.ErrUnknownLocation):
		return http.StatusUnprocessableEntity, "UNKNOWN_LOCATION"
	case errors.Is(err, domain.ErrOrderNotFound):
		return http.StatusNotFound, "ORDER_NOT_FOUND"
	case errors.Is(err, domain.ErrStoreNotFound):
		return http.StatusNotFound, "STORE_NOT_FOUND"
	case errors.Is(err, domain.ErrInsufficientStock):
		return http.StatusConflict, "INSUFFICIENT_STOCK"
	case errors.Is(err, domain.ErrOrderExpired):
		return http.StatusConflict, "ORDER_EXPIRED"
	case errors.Is(err, domain.ErrInvalidOrderStatus):
		return http.StatusConflict, "INVALID_ORDER_STATUS"
	case errors.Is(err, domain.ErrOrderNotCancellable):
		return http.StatusConflict, "ORDER_NOT_CANCELLABLE"
	case errors.Is(err, domain.ErrDeviceStoreMismatch):
		return http.StatusConflict, "DEVICE_STORE_MISMATCH"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := classify(err)
	message := err.Error()
	if status == http.StatusInternalServerError {
		hlog.FromRequest(r).Error().Err(err).Msg("unhandled error") // details stay in the log
		message = "internal error"
	}
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}
