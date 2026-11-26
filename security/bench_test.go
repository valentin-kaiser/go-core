package security_test

import (
	"bytes"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/security"
)

const (
	benchSecret  = "secret"
	benchSalt    = "salt"
	benchDataLen = 1 << 20
)

func BenchmarkAesCipherDecrypt(b *testing.B) {
	cipher := security.NewAesCipher().WithAES256()
	var enc bytes.Buffer
	cipher.Encrypt(benchPlaintext, &enc)
	if cipher.Error != nil {
		b.Fatalf("encrypting benchmark data failed: %v", cipher.Error)
	}
	text := enc.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out bytes.Buffer
		cipher.Decrypt(text, &out)
		if cipher.Error != nil {
			b.Fatalf("decrypting benchmark data failed: %v", cipher.Error)
		}
	}
}

func BenchmarkAesCipherEncryptParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		cipher := security.NewAesCipher().WithAES256()
		var out bytes.Buffer
		for pb.Next() {
			out.Reset()
			cipher.Encrypt(benchPlaintext, &out)
			if cipher.Error != nil {
				b.Errorf("encrypting benchmark data failed: %v", cipher.Error)
				return
			}
		}
	})
}

func BenchmarkDeriveKey(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := security.DeriveKey(benchSecret, benchSalt)
		if len(key) == 0 {
			b.Fatal("DeriveKey returned an empty key")
		}
	}
}

func BenchmarkDeriveArgon2Key(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := security.DeriveArgon2Key(benchSecret, benchSalt)
		if len(key) == 0 {
			b.Fatal("DeriveArgon2Key returned an empty key")
		}
	}
}

func BenchmarkSHA256Large(b *testing.B) {
	data := make([]byte, benchDataLen)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sum := security.SHA256(data)
		if sum == "" {
			b.Fatal("SHA256 returned an empty hash")
		}
	}
}
