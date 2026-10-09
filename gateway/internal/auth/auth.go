// Package auth holds the security primitives: password hashing, session tokens, API keys and encryption of
// stored vendor passwords.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// CheckPassword verifies a password against a bcrypt hash. Hashes from the legacy portal ($2y$) verify too.
func CheckPassword(hash, pw string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// NewToken returns a random URL-safe token and its SHA-256 hash (only the hash is stored).
func NewToken() (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewAPIKey returns a key shown once to the user, its display prefix and its hash.
func NewAPIKey() (key, prefix string, hash []byte) {
	token, _ := NewToken()
	key = "tvz_" + token
	return key, key[:12], HashToken(key)
}

// EqualHash compares hashes in constant time.
func EqualHash(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// Cipher encrypts secrets that must be recoverable (vendor bind passwords) with AES-256-GCM.
type Cipher struct{ aead cipher.AEAD }

func NewCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plain string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return "v1:" + base64.RawStdEncoding.EncodeToString(c.aead.Seal(nonce, nonce, []byte(plain), nil))
}

func (c *Cipher) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(enc, "v1:")
	if !ok {
		return "", errors.New("unknown secret format")
	}
	data, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil || len(data) < c.aead.NonceSize() {
		return "", errors.New("corrupt secret")
	}
	plain, err := c.aead.Open(nil, data[:c.aead.NonceSize()], data[c.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("cannot decrypt secret (wrong GATEWAY_SECRET?)")
	}
	return string(plain), nil
}

// Roles that see only their own and their team's clients and vendors.
func IsTeamRole(role string) bool {
	return role == "manager" || role == "team_lead" || role == "sales"
}

// Principal is the logged-in user.
type Principal struct {
	UserID   int64  `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	ClientID int64  `json:"client_id,omitempty"`
}

func (p *Principal) IsAdmin() bool  { return p.Role == "admin" }
func (p *Principal) IsClient() bool { return p.Role == "client" }

// CanManage reports whether the role may change configuration (clients, vendors, routes, rates).
func (p *Principal) CanManage() bool {
	return p.Role == "admin" || p.Role == "manager" || p.Role == "noc"
}

// CanSeeAll reports whether the role sees every client and vendor.
func (p *Principal) CanSeeAll() bool {
	return p.Role == "admin" || p.Role == "finance" || p.Role == "noc"
}
