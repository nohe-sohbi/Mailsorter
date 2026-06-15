package crypto

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	e := NewEncryptor("master-key")
	plaintext := "super-secret-client-secret"

	ct, err := e.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if ct == plaintext {
		t.Fatal("ciphertext should not equal plaintext")
	}

	got, err := e.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plaintext {
		t.Fatalf("round trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	e := NewEncryptor("master-key")
	a, _ := e.Encrypt("same")
	b, _ := e.Encrypt("same")
	if a == b {
		t.Fatal("expected a fresh nonce per encryption, got identical ciphertext")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	ct, _ := NewEncryptor("key-a").Encrypt("data")
	if _, err := NewEncryptor("key-b").Decrypt(ct); err == nil {
		t.Fatal("decrypting with the wrong key should fail")
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	e := NewEncryptor("master-key")
	if _, err := e.Decrypt("not-base64!!"); err == nil {
		t.Fatal("expected error decrypting invalid base64")
	}
	if _, err := e.Decrypt("YWJj"); err == nil {
		t.Fatal("expected error decrypting too-short ciphertext")
	}
}
