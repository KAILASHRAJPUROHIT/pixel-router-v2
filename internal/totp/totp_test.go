package totp

import "testing"

// RFC 4226 Appendix D official HOTP test vectors - secret is the
// ASCII string "12345678901234567890", base32-encoded as
// "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" (the same standard demo secret
// used by RFC 6238 and most TOTP client test suites). hotp() itself is
// unexported and counter-based, so we test it indirectly via At() at
// times chosen so unixTime/30 lands on each counter value.
func TestHotpVectorsViaAt(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	want := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}
	for counter, expected := range want {
		got, err := At(secret, int64(counter)*step)
		if err != nil {
			t.Fatalf("counter %d: %v", counter, err)
		}
		if got != expected {
			t.Errorf("counter %d: got %s want %s", counter, got, expected)
		}
	}
}

// RFC 6238 Appendix B official TOTP test vectors (last 6 digits of the
// 8-digit reference values - this implementation uses 6-digit codes,
// matching every real Authy/Google Authenticator deployment).
func TestTotpRfc6238Vectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unixTime int64
		want     string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		got, err := At(secret, c.unixTime)
		if err != nil {
			t.Fatalf("t=%d: %v", c.unixTime, err)
		}
		if got != c.want {
			t.Errorf("t=%d: got %s want %s", c.unixTime, got, c.want)
		}
	}
}

func TestVerifyWindow(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := int64(1234567890)
	code, _ := At(secret, now)
	if !Verify(secret, code, now) {
		t.Error("exact-time code should verify")
	}
	if !Verify(secret, code, now+29) {
		t.Error("code should still verify within the same 30s step")
	}
	if Verify(secret, "000000", now) {
		t.Error("wrong code must not verify")
	}
}
