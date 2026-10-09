package security_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/valentin-kaiser/go-core/security"
)

// The cipher is cached per passphrase; a changed passphrase has to take effect.
func TestAesCipherFollowsPassphraseChange(t *testing.T) {
	key1 := bytes.Repeat([]byte{1}, 32)
	key2 := bytes.Repeat([]byte{2}, 32)

	c := security.NewAesCipher().WithPassphrase(key1)
	var enc bytes.Buffer
	if c.Encrypt("secret", &enc); c.Error != nil {
		t.Fatal(c.Error)
	}

	var dec bytes.Buffer
	if c.Decrypt(enc.String(), &dec); c.Error != nil || dec.String() != "secret" {
		t.Fatalf("round trip: %q, %v", dec.String(), c.Error)
	}

	// A second key must not decrypt what the first one encrypted
	other := security.NewAesCipher().WithPassphrase(key2)
	if other.Decrypt(enc.String(), &bytes.Buffer{}); other.Error == nil {
		t.Fatal("a different key decrypted the data")
	}

	// Changing the passphrase of a used cipher must not keep using the cached key
	c.WithPassphrase(key2)
	if c.Decrypt(enc.String(), &bytes.Buffer{}); c.Error == nil {
		t.Fatal("cipher kept using the old key after WithPassphrase")
	}
}

func TestAesCipherInvalidKeyStillReportsError(t *testing.T) {
	c := security.NewAesCipher().WithPassphrase([]byte("short"))
	if c.Encrypt("x", &bytes.Buffer{}); c.Error == nil {
		t.Fatal("expected an error for an invalid key length")
	}
}

// One cipher used by several goroutines for successful calls.
func TestAesCipherConcurrentUse(t *testing.T) {
	c := security.NewAesCipher().WithAES256()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				var enc, dec bytes.Buffer
				c.Encrypt("payload", &enc)
				c.Decrypt(enc.String(), &dec)
				if dec.String() != "payload" {
					t.Errorf("got %q", dec.String())
					return
				}
			}
		}()
	}
	wg.Wait()
}
