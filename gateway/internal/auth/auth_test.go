package auth

import "testing"

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
