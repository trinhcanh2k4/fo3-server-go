package store

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestCipherRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"squad":[1,2,3],"ep":20900000}`)
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc == string(plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("roundtrip mismatch: %s", dec)
	}
	// A different key must not open the value.
	key2 := make([]byte, 32)
	_, _ = rand.Read(key2)
	c2, _ := NewCipher(key2)
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatal("wrong key decrypted successfully")
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("matkhau123")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("matkhau123", h) {
		t.Fatal("correct password rejected")
	}
	if CheckPassword("sai-mat-khau", h) {
		t.Fatal("wrong password accepted")
	}
}
