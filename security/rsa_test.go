package security_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/valentin-kaiser/go-core/security"
)

func TestRSACipher_GenerateKeyPair(t *testing.T) {
	tests := []struct {
		name    string
		bits    int
		wantErr bool
	}{
		{"2048 bits", 2048, false},
		{"3072 bits", 3072, false},
		{"4096 bits", 4096, false},
		{"too small", 1024, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cipher := security.NewRSACipher()
			cipher.GenerateKeyPair(tt.bits)

			if tt.wantErr {
				if cipher.Error == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if cipher.Error != nil {
				t.Errorf("unexpected error: %v", cipher.Error)
				return
			}

			if cipher.GetKeySize() != tt.bits {
				t.Errorf("key size = %d, want %d", cipher.GetKeySize(), tt.bits)
			}

			if cipher.GetPrivateKey() == nil {
				t.Error("private key is nil")
			}

			if cipher.GetPublicKey() == nil {
				t.Error("public key is nil")
			}
		})
	}
}

func TestRSACipher_SignAndVerify(t *testing.T) {
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)

	if cipher.Error != nil {
		t.Fatalf("failed to generate key pair: %v", cipher.Error)
	}

	testData := []byte("Hello, World! This is a test message for signing.")

	// Sign the data
	signature, err := cipher.Sign(testData)
	if err != nil {
		t.Fatalf("failed to sign data: %v", err)
	}

	if len(signature) == 0 {
		t.Error("signature is empty")
	}

	// Verify the signature
	valid, err := cipher.Verify(testData, signature)
	if err != nil {
		t.Fatalf("failed to verify signature: %v", err)
	}

	if !valid {
		t.Error("signature verification failed")
	}

	// Test with modified data
	modifiedData := []byte("Modified message")
	valid, err = cipher.Verify(modifiedData, signature)
	if err != nil {
		t.Fatalf("failed to verify modified data: %v", err)
	}

	if valid {
		t.Error("signature verification should have failed for modified data")
	}

	// Test with modified signature
	modifiedSignature := make([]byte, len(signature))
	copy(modifiedSignature, signature)
	modifiedSignature[0] ^= 0xFF

	valid, err = cipher.Verify(testData, modifiedSignature)
	if err != nil {
		t.Fatalf("failed to verify with modified signature: %v", err)
	}

	if valid {
		t.Error("signature verification should have failed for modified signature")
	}
}

func TestRSACipher_EncryptAndDecrypt(t *testing.T) {
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)

	if cipher.Error != nil {
		t.Fatalf("failed to generate key pair: %v", cipher.Error)
	}

	testData := []byte("Secret message to encrypt")

	// Encrypt the data
	ciphertext, err := cipher.Encrypt(testData)
	if err != nil {
		t.Fatalf("failed to encrypt data: %v", err)
	}

	if len(ciphertext) == 0 {
		t.Error("ciphertext is empty")
	}

	// Decrypt the data
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("failed to decrypt data: %v", err)
	}

	if !bytes.Equal(plaintext, testData) {
		t.Errorf("decrypted data = %s, want %s", plaintext, testData)
	}
}

func TestRSACipher_ExportAndLoadKeys(t *testing.T) {
	// Generate a key pair
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)

	if cipher.Error != nil {
		t.Fatalf("failed to generate key pair: %v", cipher.Error)
	}

	// Export keys
	privateKeyPEM, err := cipher.ExportPrivateKey(nil)
	if err != nil {
		t.Fatalf("failed to export private key: %v", err)
	}

	publicKeyPEM, err := cipher.ExportPublicKey()
	if err != nil {
		t.Fatalf("failed to export public key: %v", err)
	}

	// Create new cipher and load keys
	newCipher := security.NewRSACipher()
	newCipher.LoadPrivateKey(privateKeyPEM, nil)

	if newCipher.Error != nil {
		t.Fatalf("failed to load private key: %v", newCipher.Error)
	}

	// Test signing with loaded key
	testData := []byte("Test message")
	signature, err := newCipher.Sign(testData)
	if err != nil {
		t.Fatalf("failed to sign with loaded key: %v", err)
	}

	// Load public key separately
	pubCipher := security.NewRSACipher()
	pubCipher.LoadPublicKey(publicKeyPEM)

	if pubCipher.Error != nil {
		t.Fatalf("failed to load public key: %v", pubCipher.Error)
	}

	// Verify with loaded public key
	valid, err := pubCipher.Verify(testData, signature)
	if err != nil {
		t.Fatalf("failed to verify with loaded key: %v", err)
	}

	if !valid {
		t.Error("signature verification failed with loaded keys")
	}
}

func TestRSACipher_ExportAndLoadKeysWithPassphrase(t *testing.T) {
	passphrase := []byte("test-passphrase-123")

	// Generate a key pair
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)

	if cipher.Error != nil {
		t.Fatalf("failed to generate key pair: %v", cipher.Error)
	}

	// Export private key with passphrase
	privateKeyPEM, err := cipher.ExportPrivateKey(passphrase)
	if err != nil {
		t.Fatalf("failed to export private key: %v", err)
	}

	// Create new cipher and load encrypted key
	newCipher := security.NewRSACipher()
	newCipher.LoadPrivateKey(privateKeyPEM, passphrase)

	if newCipher.Error != nil {
		t.Fatalf("failed to load encrypted private key: %v", newCipher.Error)
	}

	// Test signing with loaded key
	testData := []byte("Test message")
	signature, err := newCipher.Sign(testData)
	if err != nil {
		t.Fatalf("failed to sign with loaded key: %v", err)
	}

	// Verify with original cipher
	valid, err := cipher.Verify(testData, signature)
	if err != nil {
		t.Fatalf("failed to verify: %v", err)
	}

	if !valid {
		t.Error("signature verification failed")
	}

	// Test loading with wrong passphrase
	wrongCipher := security.NewRSACipher()
	wrongCipher.LoadPrivateKey(privateKeyPEM, []byte("wrong-passphrase"))

	if wrongCipher.Error == nil {
		t.Error("expected error when loading with wrong passphrase")
	}
}

func TestRSACipher_SaveAndLoadFromFile(t *testing.T) {
	tempDir := t.TempDir()
	privateKeyPath := filepath.Join(tempDir, "private.pem")
	publicKeyPath := filepath.Join(tempDir, "public.pem")
	passphrase := []byte("file-test-passphrase")

	// Generate and save keys
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)

	if cipher.Error != nil {
		t.Fatalf("failed to generate key pair: %v", cipher.Error)
	}

	cipher.SavePrivateKey(privateKeyPath, passphrase)
	if cipher.Error != nil {
		t.Fatalf("failed to save private key: %v", cipher.Error)
	}

	cipher.SavePublicKey(publicKeyPath)
	if cipher.Error != nil {
		t.Fatalf("failed to save public key: %v", cipher.Error)
	}

	// Verify files exist
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Error("private key file was not created")
	}

	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Error("public key file was not created")
	}

	// Load keys from files
	newCipher := security.NewRSACipher()
	newCipher.LoadPrivateKeyFromFile(privateKeyPath, passphrase)

	if newCipher.Error != nil {
		t.Fatalf("failed to load private key from file: %v", newCipher.Error)
	}

	pubCipher := security.NewRSACipher()
	pubCipher.LoadPublicKeyFromFile(publicKeyPath)

	if pubCipher.Error != nil {
		t.Fatalf("failed to load public key from file: %v", pubCipher.Error)
	}

	// Test signing and verification with loaded keys
	testData := []byte("File test message")
	signature, err := newCipher.Sign(testData)
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}

	valid, err := pubCipher.Verify(testData, signature)
	if err != nil {
		t.Fatalf("failed to verify: %v", err)
	}

	if !valid {
		t.Error("signature verification failed with keys loaded from files")
	}
}

func TestRSACipher_ErrorChaining(t *testing.T) {
	// Test error propagation in chained calls
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(1024) // This should fail (too small)

	if cipher.Error == nil {
		t.Fatal("expected error for small key size")
	}

	// Subsequent operations should also return error
	_, err := cipher.Sign([]byte("test"))
	if err == nil {
		t.Error("expected error to propagate")
	}

	_, err = cipher.Encrypt([]byte("test"))
	if err == nil {
		t.Error("expected error to propagate")
	}
}

func TestRSACipher_NoKeyLoaded(t *testing.T) {
	cipher := security.NewRSACipher()

	// Try to sign without a private key
	_, err := cipher.Sign([]byte("test"))
	if err == nil {
		t.Error("expected error when signing without private key")
	}

	// Try to verify without a public key
	_, err = cipher.Verify([]byte("test"), []byte("signature"))
	if err == nil {
		t.Error("expected error when verifying without public key")
	}

	// Try to encrypt without a public key
	_, err = cipher.Encrypt([]byte("test"))
	if err == nil {
		t.Error("expected error when encrypting without public key")
	}

	// Try to decrypt without a private key
	_, err = cipher.Decrypt([]byte("ciphertext"))
	if err == nil {
		t.Error("expected error when decrypting without private key")
	}
}

func BenchmarkRSACipher_Sign_2048(b *testing.B) {
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)
	testData := []byte("Benchmark test message for RSA signing")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cipher.Sign(testData)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRSACipher_Verify_2048(b *testing.B) {
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(2048)
	testData := []byte("Benchmark test message for RSA verification")
	signature, _ := cipher.Sign(testData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cipher.Verify(testData, signature)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRSACipher_Sign_4096(b *testing.B) {
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(4096)
	testData := []byte("Benchmark test message for RSA signing")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cipher.Sign(testData)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRSACipher_Verify_4096(b *testing.B) {
	cipher := security.NewRSACipher()
	cipher.GenerateKeyPair(4096)
	testData := []byte("Benchmark test message for RSA verification")
	signature, _ := cipher.Sign(testData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cipher.Verify(testData, signature)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRSACipher_GenerateKeyPair_2048(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cipher := security.NewRSACipher()
		cipher.GenerateKeyPair(2048)
		if cipher.Error != nil {
			b.Fatal(cipher.Error)
		}
	}
}

func BenchmarkRSACipher_GenerateKeyPair_4096(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cipher := security.NewRSACipher()
		cipher.GenerateKeyPair(4096)
		if cipher.Error != nil {
			b.Fatal(cipher.Error)
		}
	}
}
