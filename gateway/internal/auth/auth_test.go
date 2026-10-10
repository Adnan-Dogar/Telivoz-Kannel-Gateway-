package auth

import (
	"testing"
	"time"
)

func TestPasswordAndLegacyHash(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil || !CheckPassword(h, "s3cret") || CheckPassword(h, "nope") {
		t.Fatal("bcrypt round trip failed")
	}
	// Yii2 generatePasswordHash output for "test123" ($2y$ prefix)
	legacy := "$2y$13$6S5rJwz4.0z2kB2Qe9a0F.1kN0JYUzvH0ZcB4w6sQfTYFJvYxTq9W"
	if CheckPassword(legacy, "wrong") {
		t.Fatal("wrong password accepted")
	}
	h2y := "$2y$" + h[4:]
	if !CheckPassword(h2y, "s3cret") {
		t.Fatal("$2y$ hashes must verify")
	}
}

func TestCipher(t *testing.T) {
	c, _ := NewCipher(make([]byte, 32))
	enc := c.Encrypt("vendorpw")
	if enc == "vendorpw" {
		t.Fatal("not encrypted")
	}
	if got, err := c.Decrypt(enc); err != nil || got != "vendorpw" {
		t.Fatalf("got %q %v", got, err)
	}
	other, _ := NewCipher(append(make([]byte, 31), 1))
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestAPIKey(t *testing.T) {
	key, prefix, hash := NewAPIKey()
	if len(key) < 40 || prefix != key[:12] || !EqualHash(hash, HashToken(key)) {
		t.Fatal("api key mismatch")
	}
}

func TestTOTPRFC6238Vector(t *testing.T) {
	// RFC 6238 test secret "12345678901234567890" (base32 GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ) at T=59s -> 287082 (6 digits).
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := TOTPCode(secret, time.Unix(59, 0))
	if err != nil || code != "287082" {
		t.Fatalf("got %q %v", code, err)
	}
	if !VerifyTOTP(secret, "287082", time.Unix(80, 0)) || VerifyTOTP(secret, "287082", time.Unix(200, 0)) {
		t.Fatal("drift window wrong")
	}
	if len(NewTOTPSecret()) != 32 {
		t.Fatal("secret length")
	}
}
