package security_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509/pkix"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/apperror"
	"github.com/valentin-kaiser/go-core/v2/security"
)

const (
	keyLength             = 32
	md5HexLength          = 32
	sha256HexLength       = 64
	shortPassphraseLength = 16
	shortPassphrase       = "short"
	longInputRepeat       = 1000
	largeInputRepeat      = 10000
	privateFileMode       = 0600
	privateDirMode        = 0750
	missingCertFile       = "nonexistent.crt"
	missingKeyFile        = "nonexistent.key"
	missingCAFile         = "nonexistent.ca"
)

func TestGetRandomBytes(t *testing.T) {
	testCases := []uint{1, 16, 32, 64, 128}

	for _, size := range testCases {
		bytes, err := security.RandomBytes(size)
		if err != nil {
			t.Errorf("RandomBytes(%d) returned error: %v", size, err)
			continue
		}

		if len(bytes) != int(size) {
			t.Errorf("RandomBytes(%d) returned %d bytes, expected %d", size, len(bytes), size)
		}
	}
}

func TestGetRandomBytesZero(t *testing.T) {
	bytes, err := security.RandomBytes(0)
	if err != nil {
		t.Errorf("RandomBytes(0) returned error: %v", err)
	}
	if len(bytes) != 0 {
		t.Errorf("RandomBytes(0) returned %d bytes, expected 0", len(bytes))
	}
}

func TestSHA256(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{input: "hello", expected: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{input: "Hello, World!", expected: "dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f"},
		{input: "The quick brown fox jumps over the lazy dog", expected: "d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592"},
	}

	for _, tc := range testCases {
		result := security.SHA256([]byte(tc.input))
		if result != tc.expected {
			t.Errorf("SHA256(%q) = %s, expected %s", tc.input, result, tc.expected)
		}
	}
}

func TestXX(t *testing.T) {
	testCases := []struct {
		input string
	}{
		{input: ""},
		{input: "hello"},
		{input: "Hello, World!"},
		{input: "The quick brown fox jumps over the lazy dog"},
	}

	for _, tc := range testCases {
		result := security.XX([]byte(tc.input))
		// We can't predict the exact hash, but it should be consistent
		result2 := security.XX([]byte(tc.input))
		if result != result2 {
			t.Errorf("XX(%q) not consistent: %d != %d", tc.input, result, result2)
		}
	}
}

func TestMd5(t *testing.T) {
	testCases := []struct {
		input    any
		expected string
	}{
		{input: "", expected: "d41d8cd98f00b204e9800998ecf8427e"},
		{input: "hello", expected: "5d41402abc4b2a76b9719d911017c592"},
		{input: "Hello, World!", expected: "65a8e27d8879283831b664bd8b7f0ad4"},
		{input: 42, expected: "a1d0c6e83f027327d8461063f4ac58a6"},
		{input: true, expected: "b326b5062b2f0e69046810717534cb09"},
	}

	for _, tc := range testCases {
		result := security.Md5(tc.input)
		if result != tc.expected {
			t.Errorf("Md5(%v) = %s, expected %s", tc.input, result, tc.expected)
		}
	}
}

func TestStringToKey32Bytes(t *testing.T) {
	testCases := []string{
		"",
		"password",
		"secret",
		"very long passphrase with many characters",
	}

	for _, tc := range testCases {
		key := security.StringToKey32Bytes(tc)
		if len(key) != keyLength {
			t.Errorf("StringToKey32Bytes(%q) returned %d bytes, expected 32", tc, len(key))
		}

		// Test consistency
		key2 := security.StringToKey32Bytes(tc)
		if !bytes.Equal(key, key2) {
			t.Errorf("StringToKey32Bytes(%q) not consistent", tc)
		}
	}
}

func TestStringToKey32(t *testing.T) {
	testCases := []string{
		"",
		"password",
		"secret",
		"very long passphrase with many characters",
	}

	for _, tc := range testCases {
		key := security.StringToKey32(tc)
		if len(key) != keyLength {
			t.Errorf("StringToKey32(%q) returned %d characters, expected 32", tc, len(key))
		}

		// Test consistency
		key2 := security.StringToKey32(tc)
		if key != key2 {
			t.Errorf("StringToKey32(%q) not consistent", tc)
		}

		// Test that it's hex encoded
		for _, c := range key {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("StringToKey32(%q) contains non-hex character: %c", tc, c)
				break
			}
		}
	}
}

func TestGetRandomBytesBase64(t *testing.T) {
	testCases := []uint{1, 16, 32, 64}

	for _, size := range testCases {
		encoded, err := security.RandomBytesBase64(size)
		if err != nil {
			t.Errorf("RandomBytesBase64(%d) returned error: %v", size, err)
			continue
		}

		// Base64 encoding should produce a string
		if len(encoded) == 0 && size > 0 {
			t.Errorf("RandomBytesBase64(%d) returned empty string", size)
		}

		// Test that it's valid base64
		if size > 0 {
			for _, c := range encoded {
				if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') &&
					(c < '0' || c > '9') && c != '+' && c != '/' && c != '=' {
					t.Errorf("RandomBytesBase64(%d) contains invalid base64 character: %c", size, c)
					break
				}
			}
		}
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	testCases := []int{1, 8, shortPassphraseLength, keyLength, 64}

	for _, length := range testCases {
		password, err := security.GenerateRandomPassword(length)
		if err != nil {
			t.Errorf("GenerateRandomPassword(%d) returned error: %v", length, err)
			continue
		}

		if len(password) != length {
			t.Errorf("GenerateRandomPassword(%d) returned password of length %d, expected %d", length, len(password), length)
		}

		// Test that password contains only valid characters
		validChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-#!+/"
		for _, c := range password {
			if !strings.ContainsRune(validChars, c) {
				t.Errorf("GenerateRandomPassword(%d) contains invalid character: %c", length, c)
				break
			}
		}
	}
}

func TestGenerateRandomPasswordZero(t *testing.T) {
	password, err := security.GenerateRandomPassword(0)
	if err != nil {
		t.Errorf("GenerateRandomPassword(0) returned error: %v", err)
	}
	if len(password) != 0 {
		t.Errorf("GenerateRandomPassword(0) returned password of length %d, expected 0", len(password))
	}
}

func TestReadOrSavePassphrase(t *testing.T) {
	// Create a temporary directory for test
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test_passphrase.txt")

	// Test creating new passphrase
	passphrase1, err := security.ReadOrSavePassphrase(testFile, keyLength)
	if err != nil {
		t.Errorf("ReadOrSavePassphrase() returned error: %v", err)
	}

	if len(passphrase1) != keyLength {
		t.Errorf("ReadOrSavePassphrase() returned passphrase of length %d, expected 32", len(passphrase1))
	}

	// Test reading existing passphrase
	passphrase2, err := security.ReadOrSavePassphrase(testFile, keyLength)
	if err != nil {
		t.Errorf("ReadOrSavePassphrase() returned error when reading existing file: %v", err)
	}

	if !bytes.Equal(passphrase1, passphrase2) {
		t.Error("ReadOrSavePassphrase() returned different passphrase when reading existing file")
	}

	// Test that file exists and has correct content
	content, err := os.ReadFile(filepath.Clean(testFile))
	if err != nil {
		t.Errorf("Failed to read test file: %v", err)
	}

	if !bytes.Equal(content[:keyLength], passphrase1) {
		t.Error("File content doesn't match returned passphrase")
	}
}

func TestReadOrSavePassphraseTooShort(t *testing.T) {
	// Create a temporary file with short content
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "short_passphrase.txt")

	err := os.WriteFile(testFile, []byte(shortPassphrase), privateFileMode)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test reading passphrase that's too short
	_, err = security.ReadOrSavePassphrase(testFile, keyLength)
	if err == nil {
		t.Error("ReadOrSavePassphrase() should return error for too short passphrase")
	}
}

func TestReadOrSavePassphraseFilePermissions(t *testing.T) {
	// Create a temporary directory for test
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "perm_test.txt")

	// Create passphrase file
	_, err := security.ReadOrSavePassphrase(testFile, shortPassphraseLength)
	if err != nil {
		t.Errorf("ReadOrSavePassphrase() returned error: %v", err)
	}

	// Check file permissions
	info, err := os.Stat(testFile)
	if err != nil {
		t.Errorf("Failed to stat test file: %v", err)
	}

	if runtime.GOOS != "windows" {
		mode := info.Mode()
		if mode.Perm() != privateFileMode {
			t.Errorf("File permissions are %o, expected 0600", mode.Perm())
		}
	}
}

func TestStringToKey32BytesEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "single character", input: "a"},
		{name: "long string", input: strings.Repeat("test", longInputRepeat)},
		{name: "unicode string", input: "测试中文字符串🚀"},
		{name: "special characters", input: "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := security.StringToKey32Bytes(tt.input)
			if len(result) != keyLength {
				t.Errorf("StringToKey32Bytes() returned %d bytes, expected 32", len(result))
			}

			// Test consistency
			result2 := security.StringToKey32Bytes(tt.input)
			if !bytes.Equal(result, result2) {
				t.Error("StringToKey32Bytes() should return consistent results")
			}
		})
	}
}

func TestStringToKey32EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "single character", input: "a"},
		{name: "long string", input: strings.Repeat("test", longInputRepeat)},
		{name: "unicode string", input: "测试中文字符串🚀"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := security.StringToKey32(tt.input)
			if len(result) != keyLength {
				t.Errorf("StringToKey32() returned %d characters, expected 32", len(result))
			}

			// Test consistency
			result2 := security.StringToKey32(tt.input)
			if result != result2 {
				t.Error("StringToKey32() should return consistent results")
			}
		})
	}
}

func TestSHA256EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{name: "empty bytes", input: []byte{}},
		{name: "single byte", input: []byte{0}},
		{name: "null bytes", input: []byte{0, 0, 0, 0}},
		{name: "large input", input: bytes.Repeat([]byte("test"), largeInputRepeat)},
		{name: "binary data", input: []byte{0xFF, 0xFE, 0xFD, 0xFC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := security.SHA256(tt.input)
			if len(result) != sha256HexLength { // SHA256 hex string is 64 characters
				t.Errorf("SHA256() returned %d characters, expected 64", len(result))
			}

			// Test consistency
			result2 := security.SHA256(tt.input)
			if result != result2 {
				t.Error("SHA256() should return consistent results")
			}
		})
	}
}

func TestXXHashEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{name: "empty bytes", input: []byte{}},
		{name: "single byte", input: []byte{0}},
		{name: "large input", input: bytes.Repeat([]byte("test"), largeInputRepeat)},
		{name: "binary data", input: []byte{0xFF, 0xFE, 0xFD, 0xFC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := security.XX(tt.input)

			// Test consistency
			result2 := security.XX(tt.input)
			if result != result2 {
				t.Error("XX() should return consistent results")
			}
		})
	}
}

func TestMd5EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{name: "nil", input: nil},
		{name: "empty string", input: ""},
		{name: "number", input: 42},
		{name: "float", input: 3.14159},
		{name: "bool", input: true},
		{name: "slice", input: []int{1, 2, 3}},
		{name: "struct", input: struct{ Name string }{Name: "test"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := security.Md5(tt.input)
			if len(result) != md5HexLength { // MD5 hex string is 32 characters
				t.Errorf("Md5() returned %d characters, expected 32", len(result))
			}

			// Test consistency
			result2 := security.Md5(tt.input)
			if result != result2 {
				t.Error("Md5() should return consistent results")
			}
		})
	}
}

func TestGenerateRandomPasswordEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		length int
		valid  bool
	}{
		{name: "zero length", length: 0, valid: true},
		{name: "small length", length: 1, valid: true},
		{name: "normal length", length: 16, valid: true},
		{name: "large length", length: 1000, valid: true},
		{name: "negative length", length: -1, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := security.GenerateRandomPassword(tt.length)

			if tt.valid {
				if err != nil {
					t.Errorf("GenerateRandomPassword() should succeed for length %d: %v", tt.length, err)
				}
				if len(result) != tt.length {
					t.Errorf("GenerateRandomPassword() returned %d characters, expected %d", len(result), tt.length)
				}
			}
		})
	}
}

func TestReadOrSavePassphraseFileOperations(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		filename string
		length   int
		setup    func(string) error
		wantErr  bool
	}{
		{
			name:     "new file",
			filename: "new_passphrase.txt",
			length:   keyLength,
			setup:    nil,
			wantErr:  false,
		},
		{
			name:     "existing file with sufficient length",
			filename: "existing_passphrase.txt",
			length:   shortPassphraseLength,
			setup: func(path string) error {
				return os.WriteFile(path, []byte("this_is_a_long_enough_passphrase"), privateFileMode)
			},
			wantErr: false,
		},
		{
			name:     "existing file with insufficient length",
			filename: "short_passphrase.txt",
			length:   keyLength,
			setup: func(path string) error {
				return os.WriteFile(path, []byte(shortPassphrase), privateFileMode)
			},
			wantErr: true,
		},
	}

	// Add permission test only for non-Windows systems
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name     string
			filename string
			length   int
			setup    func(string) error
			wantErr  bool
		}{
			name:     "permission denied directory",
			filename: "readonly/passphrase.txt",
			length:   shortPassphraseLength,
			setup: func(path string) error {
				dir := filepath.Dir(path)
				err := os.MkdirAll(dir, privateDirMode)
				if err != nil {
					return apperror.NewError("creating directory failed").AddError(err)
				}
				// Remove all permissions on Unix-like systems
				return os.Chmod(dir, 0000)
			},
			wantErr: true,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tempDir, tt.filename)

			if tt.setup != nil {
				err := tt.setup(filePath)
				if err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			result, err := security.ReadOrSavePassphrase(filePath, tt.length)

			if tt.wantErr {
				if err == nil {
					t.Error("ReadOrSavePassphrase() should have failed")
				}
			} else {
				if err != nil {
					t.Errorf("ReadOrSavePassphrase() failed: %v", err)
				}
				if len(result) != tt.length {
					t.Errorf("ReadOrSavePassphrase() returned %d bytes, expected %d", len(result), tt.length)
				}
			}

			// Cleanup permission denied test
			if strings.Contains(tt.filename, "readonly") {
				dir := filepath.Dir(filePath)
				// Restore permissions before cleanup
				err := os.Chmod(dir, 0600)
				if err != nil {
					t.Logf("Failed to restore permissions: %v", err)
				}
			}
		})
	}

	// Separate Windows-specific permission test using a different approach
	if runtime.GOOS == "windows" {
		t.Run("windows permission test - invalid path", func(t *testing.T) {
			// On Windows, test with an invalid path that should cause an error
			invalidPath := filepath.Join("Z:\\nonexistent\\path\\that\\should\\not\\exist", "passphrase.txt")
			_, err := security.ReadOrSavePassphrase(invalidPath, 16)
			if err == nil {
				t.Error("ReadOrSavePassphrase() should fail with invalid path on Windows")
			}
		})
	}
}

func TestPGPCipherWithoutKeysOrPassphrase(t *testing.T) {
	cipher := security.NewPGPCipher(nil)
	var buf bytes.Buffer

	// Test encryption without keys or passphrase
	cipher.Encrypt([]byte("test message"), &buf)
	if cipher.Error == nil {
		t.Error("Encrypt() should fail without keys or passphrase")
	}

	// Reset error and test decryption
	cipher.Error = nil
	buf.Reset()
	cipher.Decrypt([]byte("encrypted data"), &buf)
	if cipher.Error == nil {
		t.Error("Decrypt() should fail without keys or passphrase")
	}
}

// Test TLS functions
func TestNewTLSConfig(t *testing.T) {
	// Generate a self-signed certificate for testing
	subject := pkix.Name{
		Organization:  []string{"Test"},
		Country:       []string{"US"},
		Province:      []string{""},
		Locality:      []string{"Test City"},
		StreetAddress: []string{""},
		PostalCode:    []string{""},
	}

	cert, caPool, err := security.GenerateSelfSignedCertificate(subject)
	if err != nil {
		t.Fatalf("Failed to generate test certificate: %v", err)
	}

	config := security.NewTLSConfig(cert, caPool, tls.RequireAndVerifyClientCert)

	if config == nil {
		t.Error("NewTLSConfig() should not return nil")
		return
	}

	if config.MinVersion != tls.VersionTLS12 {
		t.Errorf("Expected MinVersion to be TLS 1.2, got %d", config.MinVersion)
	}

	if len(config.Certificates) != 1 {
		t.Errorf("Expected 1 certificate, got %d", len(config.Certificates))
	}

	if config.ClientCAs != caPool {
		t.Error("ClientCAs should be set to the provided CA pool")
	}

	if config.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Error("ClientAuth should be set correctly")
	}
}

func TestGenerateSelfSignedCertificate(t *testing.T) {
	subject := pkix.Name{
		Organization:  []string{"Test Org"},
		Country:       []string{"US"},
		Province:      []string{"CA"},
		Locality:      []string{"San Francisco"},
		StreetAddress: []string{"123 Test St"},
		PostalCode:    []string{"12345"},
	}

	cert, caPool, err := security.GenerateSelfSignedCertificate(subject)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCertificate() failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Error("Generated certificate should not be empty")
	}

	if caPool == nil {
		t.Error("CA pool should not be nil")
	}

	// Validate the generated certificate
	err = security.ValidateCertificate(cert)
	if err != nil {
		t.Errorf("Generated certificate should be valid: %v", err)
	}

	// Check if certificate is not expired
	expired, err := security.IsCertificateExpired(cert)
	if err != nil {
		t.Errorf("Error checking certificate expiration: %v", err)
	}
	if expired {
		t.Error("Newly generated certificate should not be expired")
	}
}

func TestValidateCertificate(t *testing.T) {
	// Test with empty certificate
	emptyCert := tls.Certificate{}
	err := security.ValidateCertificate(emptyCert)
	if err == nil {
		t.Error("ValidateCertificate() should fail with empty certificate")
	}

	// Test with valid certificate
	subject := pkix.Name{Organization: []string{"Test"}}
	cert, _, err := security.GenerateSelfSignedCertificate(subject)
	if err != nil {
		t.Fatalf("Failed to generate test certificate: %v", err)
	}

	err = security.ValidateCertificate(cert)
	if err != nil {
		t.Errorf("ValidateCertificate() should pass with valid certificate: %v", err)
	}
}

func TestIsCertificateExpired(t *testing.T) {
	// Test with empty certificate
	emptyCert := tls.Certificate{}
	_, err := security.IsCertificateExpired(emptyCert)
	if err == nil {
		t.Error("IsCertificateExpired() should fail with empty certificate")
	}

	// Test with valid certificate
	subject := pkix.Name{Organization: []string{"Test"}}
	cert, _, err := security.GenerateSelfSignedCertificate(subject)
	if err != nil {
		t.Fatalf("Failed to generate test certificate: %v", err)
	}

	expired, err := security.IsCertificateExpired(cert)
	if err != nil {
		t.Errorf("IsCertificateExpired() failed: %v", err)
	}
	if expired {
		t.Error("Newly generated certificate should not be expired")
	}
}

func TestLoadCertAndConfig(t *testing.T) {
	// This test will fail because we don't have actual cert files
	// but it tests the error handling path
	_, err := security.LoadCertAndConfig(missingCertFile, missingKeyFile, missingCAFile, tls.NoClientCert)
	if err == nil {
		t.Error("LoadCertAndConfig() should fail with nonexistent files")
	}
}

func TestLoadCertificate(t *testing.T) {
	// Test with nonexistent files
	_, err := security.LoadCertificate(missingCertFile, missingKeyFile)
	if err == nil {
		t.Error("LoadCertificate() should fail with nonexistent files")
	}
}

func TestLoadCACertPool(t *testing.T) {
	// Test with nonexistent file
	_, err := security.LoadCACertPool(missingCAFile)
	if err == nil {
		t.Error("LoadCACertPool() should fail with nonexistent file")
	}

	// Test with invalid CA file
	tempDir := t.TempDir()
	invalidCAFile := filepath.Join(tempDir, "invalid.ca")
	err = os.WriteFile(invalidCAFile, []byte("invalid certificate data"), privateFileMode)
	if err != nil {
		t.Fatalf("Failed to create invalid CA file: %v", err)
	}

	_, err = security.LoadCACertPool(invalidCAFile)
	if err == nil {
		t.Error("LoadCACertPool() should fail with invalid certificate data")
	}
}

// Benchmark tests for security functions
func BenchmarkSHA256(b *testing.B) {
	data := []byte("benchmark test data for SHA256 hashing function")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		security.SHA256(data)
	}
}

func BenchmarkXX(b *testing.B) {
	data := []byte("benchmark test data for xxHash function")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		security.XX(data)
	}
}

func BenchmarkMd5(b *testing.B) {
	data := "benchmark test data for MD5 hashing function"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		security.Md5(data)
	}
}

func BenchmarkGetRandomBytes(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := security.RandomBytes(keyLength)
		if err != nil {
			b.Fatalf("Failed to get random bytes: %v", err)
		}
	}
}

func BenchmarkGenerateRandomPassword(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := security.GenerateRandomPassword(shortPassphraseLength)
		if err != nil {
			b.Fatalf("Failed to generate password: %v", err)
		}
	}
}

func BenchmarkStringToKey32(b *testing.B) {
	input := "benchmark test string for key derivation"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		security.StringToKey32(input)
	}
}
