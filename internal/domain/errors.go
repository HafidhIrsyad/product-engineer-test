// Package domain is the core: types, statuses, pure rules, and every sentinel error.
// It does no I/O and imports nothing from the other layers (ARCHITECTURE.md §4.2).
package domain

import "errors"

// All sentinel errors live here. The store translates driver errors into these,
// app returns them, and api/errors.go is the only place that maps them to HTTP (§16.1).
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

	// ErrStaleStatus: a conditional UPDATE matched no row because another process
	// changed the status first (§9.3).
	ErrStaleStatus = errors.New("status changed by another process")

	ErrDeviceStoreMismatch = errors.New("device registered at another store")
)
