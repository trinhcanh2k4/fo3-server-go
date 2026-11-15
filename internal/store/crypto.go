package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Cipher encrypts user data at rest with AES-256-GCM under the master
// key (see plan: /etc/fo3/master.key, mode 0400). MariaDB only ever
// sees ciphertext: base64(nonce || ciphertext).
type Cipher struct {
	gcm cipher.AEAD
}

func NewCipher(masterKey []byte) (*Cipher, error) {
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

// Encrypt seals plaintext for storage.
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := c.gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt opens a value produced by Encrypt.
func (c *Cipher) Decrypt(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	n := c.gcm.NonceSize()
	if len(raw) < n {
		return nil, errors.New("ciphertext too short")
	}
	return c.gcm.Open(nil, raw[:n], raw[n:], nil)
}

// HashPassword returns an Argon2id PHC-style hash for a password
// (login + second password). Passwords are hashed, never encrypted.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, 2, 64*1024, 2, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=2$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(h)), nil
}

// CheckPassword verifies a password against a HashPassword value.
func CheckPassword(pw, phc string) bool {
	parts := splitPHC(phc)
	if parts == nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts.salt)
	want, err2 := base64.RawStdEncoding.DecodeString(parts.hash)
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, parts.t, parts.m, uint8(parts.p), uint32(len(want)))
	return subtleEqual(got, want)
}

type phcParts struct {
	m, t, p uint32
	salt    string
	hash    string
}

func splitPHC(s string) *phcParts {
	// $argon2id$v=19$m=65536,t=2,p=2$<salt>$<hash>
	var out phcParts
	var params string
	n, err := fmt.Sscanf(s, "$argon2id$v=19$%s", &params)
	_ = n
	_ = err
	rest := s
	// manual parse
	idx := 0
	fields := []string{}
	cur := ""
	for _, r := range rest {
		if r == '$' {
			fields = append(fields, cur)
			cur = ""
			idx++
			continue
		}
		cur += string(r)
	}
	fields = append(fields, cur)
	if len(fields) != 6 || fields[1] != "argon2id" {
		return nil
	}
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &out.m, &out.t, &out.p); err != nil {
		return nil
	}
	out.salt, out.hash = fields[4], fields[5]
	return &out
}

func subtleEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
