// Package totp implements RFC 6238 TOTP (Authy/Google Authenticator
// compatible) using Go's standard library crypto/hmac and
// encoding/base32 - the same algorithm V1 had to hand-implement in
// pure POSIX shell/awk (no compiler was available anywhere on this
// project), validated there against every official RFC 4226 and RFC
// 6238 test vector. This re-hosts the identical algorithm and
// parameters in Go, validated against the same vectors, but without
// the ~384-subprocess-fork performance problem the shell version hit
// on its first live test.
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	digits = 6
	step   = 30 // seconds, RFC 6238 default
)

func hotp(secret []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, code%mod)
}

func decodeSecret(secretB32 string) ([]byte, error) {
	clean := strings.ToUpper(strings.TrimSpace(secretB32))
	clean = strings.ReplaceAll(clean, " ", "")
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
}

// At returns the 6-digit code for the given base32 secret at the
// given unix time.
func At(secretB32 string, unixTime int64) (string, error) {
	secret, err := decodeSecret(secretB32)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	counter := uint64(unixTime) / step
	return hotp(secret, counter), nil
}

// Verify accepts a match at the current step or either neighbor (+-1
// step), same clock-drift/network-delay tolerance every real TOTP
// verifier uses. Uses a constant-time comparison for each candidate -
// confirmed live 2026-07-12 during a security audit that the original
// plain `==` string comparison was a textbook timing-attack vector
// (leaks how many leading digits matched via response-time
// differences), inconsistent with the already-timing-safe bcrypt
// password check used right alongside this in the login handler.
func Verify(secretB32, submitted string, unixTime int64) bool {
	submittedBytes := []byte(submitted)
	for _, offset := range []int64{0, -step, step} {
		expected, err := At(secretB32, unixTime+offset)
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(expected), submittedBytes) == 1 {
			return true
		}
	}
	return false
}
