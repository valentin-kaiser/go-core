package security_test

import (
	"bytes"
	"testing"

	"github.com/valentin-kaiser/go-core/security"
)

func BenchmarkAesCipherDecrypt(b *testing.B) {
	cipher := security.NewAesCipher().WithAES256()
	var enc bytes.Buffer
	cipher.Encrypt("benchmark test data for encryption", &enc)
	text := enc.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out bytes.Buffer
		cipher.Decrypt(text, &out)
	}
}

func BenchmarkAesCipherEncryptParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		cipher := security.NewAesCipher().WithAES256()
		var out bytes.Buffer
		for pb.Next() {
			out.Reset()
			cipher.Encrypt("benchmark test data for encryption", &out)
		}
	})
}

func BenchmarkDeriveKey(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = security.DeriveKey("secret", "salt")
	}
}

func BenchmarkDeriveArgon2Key(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = security.DeriveArgon2Key("secret", "salt")
	}
}

func BenchmarkSHA256Large(b *testing.B) {
	data := make([]byte, 1<<20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = security.SHA256(data)
	}
}
