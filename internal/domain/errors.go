package domain

import "errors"

var (
	// ErrInvalidInput is wrapped with fmt.Errorf("%w: reason", ErrInvalidInput). → 422
	ErrInvalidInput = errors.New("invalid input")

	ErrUnknownSKU      = errors.New("unknown sku")
	ErrUnknownLocation = errors.New("unknown location")
	ErrStoreNotFound   = errors.New("store not found") // unknown code, or not an OWN_STORE

	ErrInsufficientStock   = errors.New("insufficient stock")
	ErrOrderNotFound       = errors.New("order not found")
	ErrOrderExpired        = errors.New("order expired")
	ErrInvalidOrderStatus  = errors.New("invalid order status")
	ErrOrderNotCancellable = errors.New("order not cancellable")

	ErrInvalidTransition = errors.New("invalid status transition")

	ErrStaleStatus = errors.New("status changed by another process")

	ErrDeviceStoreMismatch = errors.New("device registered at another store")
)
