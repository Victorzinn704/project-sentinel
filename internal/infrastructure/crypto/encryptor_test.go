package crypto

import (
	"bytes"
	"testing"
)

func TestAESGCMEncryptorRoundTripAndTamperDetection(t *testing.T) {
	t.Parallel()

	encryptor, err := NewAESGCMEncryptor([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	plaintext := []byte(`{"account_id":"acc_test","password":"secret"}`)
	ciphertext, err := encryptor.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("secret")) {
		t.Fatalf("ciphertext contains plaintext secret")
	}

	decrypted, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted payload mismatch")
	}

	ciphertext[len(ciphertext)-1] ^= 0xff
	if _, err := encryptor.Decrypt(ciphertext); err == nil {
		t.Fatalf("decrypt tampered ciphertext succeeded")
	}
}
