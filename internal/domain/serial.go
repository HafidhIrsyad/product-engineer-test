package domain

import (
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
)

type SerialLevel string

const (
	LevelUnit   SerialLevel = "UNIT"
	LevelCarton SerialLevel = "CARTON"
	LevelPallet SerialLevel = "PALLET"
)

type SerialStatus string

const (
	SerialProduced    SerialStatus = "PRODUCED"
	SerialInWarehouse SerialStatus = "IN_WAREHOUSE"
	SerialInTransit   SerialStatus = "IN_TRANSIT"
	SerialInStock     SerialStatus = "IN_STOCK"
	SerialSold        SerialStatus = "SOLD"
	SerialVerified    SerialStatus = "VERIFIED"
	SerialQuarantine  SerialStatus = "QUARANTINE"
)

// SerialStatuses lists every status, in lifecycle order.
var SerialStatuses = []SerialStatus{
	SerialProduced, SerialInWarehouse, SerialInTransit, SerialInStock,
	SerialSold, SerialVerified, SerialQuarantine,
}

var allowed = map[SerialStatus][]SerialStatus{
	SerialProduced:    {SerialInWarehouse},
	SerialInWarehouse: {SerialInTransit, SerialSold, SerialQuarantine},
	SerialInTransit:   {SerialInStock},
	SerialInStock:     {SerialInTransit, SerialSold, SerialVerified, SerialQuarantine},
	SerialSold:        {SerialVerified},
	SerialQuarantine:  {SerialInWarehouse, SerialInStock},
	SerialVerified:    {}, // final
}

// CanTransition reports whether a serial may move from one status to another.
func CanTransition(from, to SerialStatus) bool {
	return slices.Contains(allowed[from], to)
}

type SerialCode string

const (
	serialCodeLen      = 13
	serialCodePrefix   = "MG"
	crockfordAlphabets = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // no I, L, O, U
)

// NewSerialCode returns a random code: MG plus 11 Crockford characters (55 bits, not sequential).
func NewSerialCode() SerialCode {
	b := make([]byte, serialCodeLen-len(serialCodePrefix))
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	for i := range b {
		b[i] = crockfordAlphabets[b[i]%32] // 256 is a multiple of 32, so every character is equally likely
	}
	return SerialCode(serialCodePrefix + string(b))
}

// ParseSerialCode normalizes (trim, upper-case) and validates a scanned code.
func ParseSerialCode(raw string) (SerialCode, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != serialCodeLen || !strings.HasPrefix(code, serialCodePrefix) {
		return "", fmt.Errorf("%w: serial code must be %d characters starting with %s", ErrInvalidInput, serialCodeLen, serialCodePrefix)
	}
	for _, r := range code[len(serialCodePrefix):] {
		if !strings.ContainsRune(crockfordAlphabets, r) {
			return "", fmt.Errorf("%w: serial code has an invalid character %q", ErrInvalidInput, r)
		}
	}
	return SerialCode(code), nil
}
