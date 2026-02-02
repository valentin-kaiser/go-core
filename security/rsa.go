package security

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"os"

	"github.com/valentin-kaiser/go-core/apperror"
	"golang.org/x/crypto/pbkdf2"
)

// RSACipher provides RSA encryption, decryption, signing, and verification operations.
// It supports key sizes from 2048 to 4096 bits and uses SHA-256 for hashing.
type RSACipher struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	Error      error
}

// NewRSACipher creates a new RSACipher instance.
func NewRSACipher() *RSACipher {
	return &RSACipher{}
}

// GenerateKeyPair generates a new RSA key pair with the specified bit size.
// Recommended sizes: 2048, 3072, or 4096 bits. Larger keys are more secure but slower.
func (r *RSACipher) GenerateKeyPair(bits int) *RSACipher {
	if r.Error != nil {
		return r
	}

	if bits < 2048 {
		r.Error = apperror.NewError("RSA key size must be at least 2048 bits")
		return r
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		r.Error = apperror.NewError("failed to generate RSA key pair").AddError(err)
		return r
	}

	r.privateKey = privateKey
	r.publicKey = &privateKey.PublicKey
	return r
}

// LoadPrivateKey loads a private key from PEM-encoded bytes.
// Supports both PKCS#1 and PKCS#8 formats, with optional password protection for PKCS#8.
func (r *RSACipher) LoadPrivateKey(pemData []byte, passphrase []byte) *RSACipher {
	if r.Error != nil {
		return r
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		r.Error = apperror.NewError("failed to decode PEM block containing private key")
		return r
	}

	var privateKey *rsa.PrivateKey
	var err error

	if block.Type == "ENCRYPTED PRIVATE KEY" && len(passphrase) > 0 {
		// Decrypt PKCS#8 encrypted private key
		decryptedBytes, err := decryptPKCS8PrivateKey(block.Bytes, passphrase)
		if err != nil {
			r.Error = apperror.NewError("failed to decrypt PKCS#8 private key").AddError(err)
			return r
		}

		keyInterface, err := x509.ParsePKCS8PrivateKey(decryptedBytes)
		if err != nil {
			r.Error = apperror.NewError("failed to parse decrypted PKCS#8 private key").AddError(err)
			return r
		}

		var ok bool
		privateKey, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			r.Error = apperror.NewError("decrypted key is not an RSA private key")
			return r
		}
	} else if block.Type == "PRIVATE KEY" {
		// Unencrypted PKCS#8
		keyInterface, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			r.Error = apperror.NewError("failed to parse PKCS#8 private key").AddError(err)
			return r
		}

		var ok bool
		privateKey, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			r.Error = apperror.NewError("key is not an RSA private key")
			return r
		}
	} else {
		// Try PKCS#1 format
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			r.Error = apperror.NewError("failed to parse PKCS#1 private key").AddError(err)
			return r
		}
	}

	r.privateKey = privateKey
	r.publicKey = &privateKey.PublicKey
	return r
}

// LoadPrivateKeyFromFile loads a private key from a PEM file.
func (r *RSACipher) LoadPrivateKeyFromFile(path string, passphrase []byte) *RSACipher {
	if r.Error != nil {
		return r
	}

	data, err := os.ReadFile(path)
	if err != nil {
		r.Error = apperror.NewError("failed to read private key file").AddError(err)
		return r
	}

	return r.LoadPrivateKey(data, passphrase)
}

// LoadPublicKey loads a public key from PEM-encoded bytes.
func (r *RSACipher) LoadPublicKey(pemData []byte) *RSACipher {
	if r.Error != nil {
		return r
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		r.Error = apperror.NewError("failed to decode PEM block containing public key")
		return r
	}

	publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		// Try ParsePKIXPublicKey as fallback
		pubKeyInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			r.Error = apperror.NewError("failed to parse public key").AddError(err)
			return r
		}
		var ok bool
		publicKey, ok = pubKeyInterface.(*rsa.PublicKey)
		if !ok {
			r.Error = apperror.NewError("not an RSA public key")
			return r
		}
	}

	r.publicKey = publicKey
	return r
}

// LoadPublicKeyFromFile loads a public key from a PEM file.
func (r *RSACipher) LoadPublicKeyFromFile(path string) *RSACipher {
	if r.Error != nil {
		return r
	}

	data, err := os.ReadFile(path)
	if err != nil {
		r.Error = apperror.NewError("failed to read public key file").AddError(err)
		return r
	}

	return r.LoadPublicKey(data)
}

// ExportPrivateKey exports the private key as PEM-encoded bytes.
// If passphrase is provided, the key will be encrypted using PKCS#8 with AES-256-CBC and PBKDF2.
func (r *RSACipher) ExportPrivateKey(passphrase []byte) ([]byte, error) {
	if r.Error != nil {
		return nil, r.Error
	}

	if r.privateKey == nil {
		return nil, apperror.NewError("no private key loaded")
	}

	var block *pem.Block
	if len(passphrase) > 0 {
		// Marshal to PKCS#8 first
		pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(r.privateKey)
		if err != nil {
			return nil, apperror.NewError("failed to marshal private key").AddError(err)
		}

		// Encrypt using AES-256-CBC with PBKDF2
		encryptedBytes, err := encryptPKCS8PrivateKey(pkcs8Bytes, passphrase)
		if err != nil {
			return nil, apperror.NewError("failed to encrypt private key").AddError(err)
		}

		block = &pem.Block{
			Type:  "ENCRYPTED PRIVATE KEY",
			Bytes: encryptedBytes,
		}
	} else {
		// Export as unencrypted PKCS#8
		privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(r.privateKey)
		if err != nil {
			return nil, apperror.NewError("failed to marshal private key").AddError(err)
		}
		block = &pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: privateKeyBytes,
		}
	}

	return pem.EncodeToMemory(block), nil
}

// ExportPublicKey exports the public key as PEM-encoded bytes.
func (r *RSACipher) ExportPublicKey() ([]byte, error) {
	if r.Error != nil {
		return nil, r.Error
	}

	if r.publicKey == nil {
		return nil, apperror.NewError("no public key loaded")
	}

	publicKeyBytes := x509.MarshalPKCS1PublicKey(r.publicKey)
	block := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: publicKeyBytes,
	}

	return pem.EncodeToMemory(block), nil
}

// SavePrivateKey saves the private key to a file.
func (r *RSACipher) SavePrivateKey(path string, passphrase []byte) *RSACipher {
	if r.Error != nil {
		return r
	}

	pemData, err := r.ExportPrivateKey(passphrase)
	if err != nil {
		r.Error = err
		return r
	}

	err = os.WriteFile(path, pemData, 0600)
	if err != nil {
		r.Error = apperror.NewError("failed to write private key file").AddError(err)
		return r
	}

	return r
}

// SavePublicKey saves the public key to a file.
func (r *RSACipher) SavePublicKey(path string) *RSACipher {
	if r.Error != nil {
		return r
	}

	pemData, err := r.ExportPublicKey()
	if err != nil {
		r.Error = err
		return r
	}

	err = os.WriteFile(path, pemData, 0644)
	if err != nil {
		r.Error = apperror.NewError("failed to write public key file").AddError(err)
		return r
	}

	return r
}

// Sign signs the data using the private key with SHA-256 hashing.
// Returns the signature bytes.
func (r *RSACipher) Sign(data []byte) ([]byte, error) {
	if r.Error != nil {
		return nil, r.Error
	}

	if r.privateKey == nil {
		return nil, apperror.NewError("no private key loaded")
	}

	hashed := sha256.Sum256(data)
	signature, err := rsa.SignPKCS1v15(rand.Reader, r.privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return nil, apperror.NewError("failed to sign data").AddError(err)
	}

	return signature, nil
}

// Verify verifies the signature of data using the public key with SHA-256 hashing.
// Returns true if the signature is valid, false otherwise.
func (r *RSACipher) Verify(data []byte, signature []byte) (bool, error) {
	if r.Error != nil {
		return false, r.Error
	}

	if r.publicKey == nil {
		return false, apperror.NewError("no public key loaded")
	}

	hashed := sha256.Sum256(data)
	err := rsa.VerifyPKCS1v15(r.publicKey, crypto.SHA256, hashed[:], signature)
	if err != nil {
		// Verification failed - not an error, just invalid signature
		return false, nil
	}

	return true, nil
}

// Encrypt encrypts data using the public key with OAEP padding.
// This is suitable for encrypting small amounts of data (typically symmetric keys).
func (r *RSACipher) Encrypt(plaintext []byte) ([]byte, error) {
	if r.Error != nil {
		return nil, r.Error
	}

	if r.publicKey == nil {
		return nil, apperror.NewError("no public key loaded")
	}

	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, r.publicKey, plaintext, nil)
	if err != nil {
		return nil, apperror.NewError("failed to encrypt data").AddError(err)
	}

	return ciphertext, nil
}

// Decrypt decrypts data using the private key with OAEP padding.
func (r *RSACipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if r.Error != nil {
		return nil, r.Error
	}

	if r.privateKey == nil {
		return nil, apperror.NewError("no private key loaded")
	}

	plaintext, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, r.privateKey, ciphertext, nil)
	if err != nil {
		return nil, apperror.NewError("failed to decrypt data").AddError(err)
	}

	return plaintext, nil
}

// GetPublicKey returns the public key.
func (r *RSACipher) GetPublicKey() *rsa.PublicKey {
	return r.publicKey
}

// GetPrivateKey returns the private key.
func (r *RSACipher) GetPrivateKey() *rsa.PrivateKey {
	return r.privateKey
}

// GetKeySize returns the key size in bits.
func (r *RSACipher) GetKeySize() int {
	if r.publicKey != nil {
		return r.publicKey.N.BitLen()
	}
	if r.privateKey != nil {
		return r.privateKey.N.BitLen()
	}
	return 0
}

// encryptPKCS8PrivateKey encrypts a PKCS#8 private key using AES-256-CBC with PBKDF2.
// This implements secure password-based encryption without using deprecated x509.EncryptPEMBlock.
func encryptPKCS8PrivateKey(data []byte, passphrase []byte) ([]byte, error) {
	// Generate salt for PBKDF2
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, apperror.Wrap(err)
	}

	// Derive key using PBKDF2 with SHA-256 and 100,000 iterations (NIST recommendation)
	key := pbkdf2.Key(passphrase, salt, 100000, 32, sha256.New)

	// Generate IV for AES-CBC
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, apperror.Wrap(err)
	}

	// Encrypt using AES-256-CBC
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	// Pad the data to block size
	padding := aes.BlockSize - len(data)%aes.BlockSize
	paddedData := make([]byte, len(data)+padding)
	copy(paddedData, data)
	for i := len(data); i < len(paddedData); i++ {
		paddedData[i] = byte(padding)
	}

	ciphertext := make([]byte, len(paddedData))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, paddedData)

	// Encode as ASN.1 PKCS#8 EncryptedPrivateKeyInfo structure
	// This is a simplified version - full PKCS#8 would include algorithm parameters
	encryptedKey := struct {
		Salt       []byte
		IV         []byte
		Iterations int
		Ciphertext []byte
	}{
		Salt:       salt,
		IV:         iv,
		Iterations: 100000,
		Ciphertext: ciphertext,
	}

	return asn1.Marshal(encryptedKey)
}

// decryptPKCS8PrivateKey decrypts an encrypted PKCS#8 private key.
func decryptPKCS8PrivateKey(data []byte, passphrase []byte) ([]byte, error) {
	// Decode ASN.1 structure
	var encryptedKey struct {
		Salt       []byte
		IV         []byte
		Iterations int
		Ciphertext []byte
	}

	_, err := asn1.Unmarshal(data, &encryptedKey)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	// Derive key using PBKDF2
	key := pbkdf2.Key(passphrase, encryptedKey.Salt, encryptedKey.Iterations, 32, sha256.New)

	// Decrypt using AES-256-CBC
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	if len(encryptedKey.Ciphertext)%aes.BlockSize != 0 {
		return nil, apperror.NewError("ciphertext is not a multiple of block size")
	}

	plaintext := make([]byte, len(encryptedKey.Ciphertext))
	mode := cipher.NewCBCDecrypter(block, encryptedKey.IV)
	mode.CryptBlocks(plaintext, encryptedKey.Ciphertext)

	// Remove padding
	padding := int(plaintext[len(plaintext)-1])
	if padding > aes.BlockSize || padding == 0 {
		return nil, apperror.NewError("invalid padding")
	}

	return plaintext[:len(plaintext)-padding], nil
}
