package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

const (
	PointsPerUnit    = 10
	ClaimReviewHours = 72 // points can be redeemed only after this review window
	MaxClaimsPerDay  = 10 // more claims than this from one member are held for review

	innerCodeLen = 8
)

// NewInnerCode returns the random code printed under the seal of a unit.
func NewInnerCode() string {
	b := make([]byte, innerCodeLen)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	for i := range b {
		b[i] = crockfordAlphabets[b[i]%32]
	}
	return string(b)
}

// HashInnerCode is what is stored instead of the code. The input is normalized like a scan.
func HashInnerCode(code string) []byte {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return sum[:]
}

// InnerCodeMatches compares a given code with a stored hash without leaking where they differ.
func InnerCodeMatches(stored []byte, given string) bool {
	return len(stored) > 0 && subtle.ConstantTimeCompare(stored, HashInnerCode(given)) == 1
}
