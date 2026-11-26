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

	"github.com/valentin-kaiser/go-core/v2/apperror"
	"golang.org/x/crypto/pbkdf2"
)

// RSA and PKCS#8 encryption constants.
const (
	// rsaMinKeyBits is the smallest accepted RSA key size.
	rsaMinKeyBits = 2048

	pemTypeEncryptedPrivateKey = "ENCRYPTED PRIVATE KEY"
	pemTypePrivateKey          = "PRIVATE KEY"
	pemTypeRSAPublicKey        = "RSA PUBLIC KEY"

	publicKeyFileMode = 0o644

	pbkdf2SaltBytes  = 16
	pbkdf2Iterations = 100000
	// pbkdf2KeyBytes is the AES-256 key length derived by PBKDF2.
	pbkdf2KeyBytes = 32
)

// OIDs for PKCS#5 PBES2 and related algorithms
var (
	oidPBES2      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}     // PBES2
	oidPBKDF2     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}     // PBKDF2
	oidHMACSHA256 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}         // HMAC-SHA256
	oidAES256CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42} // AES-256-CBC
)

// PKCS#5 v2.0 (PBES2) ASN.1 structures for standard-compliant PKCS#8 encryption
type pbes2Params struct {
	KeyDerivationFunc algorithmIdentifier
	EncryptionScheme  algorithmIdentifier
}

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue
}

type pbkdf2Params struct {
	Salt           []byte
	IterationCount int
	KeyLength      int                 `asn1:"optional"`
	PRF            algorithmIdentifier `asn1:"optional"`
}

type encryptedPrivateKeyInfo struct {
	EncryptionAlgorithm algorithmIdentifier
	EncryptedData       []byte
}

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

	if bits < rsaMinKeyBits {
		r.Error = apperror.NewError("RSA key size must be at least 2048 bits")
		return r
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		r.Error = apperror.NewError("generating RSA key pair failed").AddError(err)
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
		r.Error = apperror.NewError("decoding PEM block containing private key failed")
		return r
	}

	var privateKey *rsa.PrivateKey
	var err error

	switch block.Type {
	case pemTypeEncryptedPrivateKey:
		if len(passphrase) == 0 {
			r.Error = apperror.NewError("encrypted private key requires a passphrase")
			return r
		}
		decryptedBytes, err := decryptPKCS8PrivateKey(block.Bytes, passphrase)
		if err != nil {
			r.Error = apperror.NewError("decrypting PKCS#8 private key failed").AddError(err)
			return r
		}
		keyInterface, err := x509.ParsePKCS8PrivateKey(decryptedBytes)
		if err != nil {
			r.Error = apperror.NewError("parsing decrypted PKCS#8 private key failed").AddError(err)
			return r
		}
		var ok bool
		privateKey, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			r.Error = apperror.NewError("decrypted key is not an RSA private key")
			return r
		}
	case pemTypePrivateKey:
		keyInterface, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			r.Error = apperror.NewError("parsing PKCS#8 private key failed").AddError(err)
			return r
		}
		var ok bool
		privateKey, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			r.Error = apperror.NewError("key is not an RSA private key")
			return r
		}
	default:
		// Try PKCS#1 format
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			r.Error = apperror.NewError("parsing PKCS#1 private key failed").AddError(err)
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

	data, err := os.ReadFile(path) //nolint:gosec // path is supplied by the caller by design
	if err != nil {
		r.Error = apperror.NewError("reading private key file failed").AddError(err)
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
		r.Error = apperror.NewError("decoding PEM block containing public key failed")
		return r
	}

	publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		// Try ParsePKIXPublicKey as fallback
		pubKeyInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			r.Error = apperror.NewError("parsing public key failed").AddError(err)
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

	data, err := os.ReadFile(path) //nolint:gosec // path is supplied by the caller by design
	if err != nil {
		r.Error = apperror.NewError("reading public key file failed").AddError(err)
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
		pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(r.privateKey)
		if err != nil {
			return nil, apperror.NewError("marshaling private key failed").AddError(err)
		}
		encryptedBytes, err := encryptPKCS8PrivateKey(pkcs8Bytes, passphrase)
		if err != nil {
			return nil, apperror.NewError("encrypting private key failed").AddError(err)
		}
		block = &pem.Block{
			Type:  pemTypeEncryptedPrivateKey,
			Bytes: encryptedBytes,
		}
	} else {
		privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(r.privateKey)
		if err != nil {
			return nil, apperror.NewError("marshaling private key failed").AddError(err)
		}
		block = &pem.Block{
			Type:  pemTypePrivateKey,
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
		Type:  pemTypeRSAPublicKey,
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

	err = os.WriteFile(path, pemData, secretFileMode)
	if err != nil {
		r.Error = apperror.NewError("writing private key file failed").AddError(err)
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

	err = os.WriteFile(path, pemData, publicKeyFileMode) //nolint:gosec // public keys are meant to be world-readable
	if err != nil {
		r.Error = apperror.NewError("writing public key file failed").AddError(err)
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
		return nil, apperror.NewError("signing data failed").AddError(err)
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
		return nil, apperror.NewError("encrypting data failed").AddError(err)
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
		return nil, apperror.NewError("decrypting data failed").AddError(err)
	}

	return plaintext, nil
}

// PublicKey returns the public key.
func (r *RSACipher) PublicKey() *rsa.PublicKey {
	return r.publicKey
}

// PrivateKey returns the private key.
func (r *RSACipher) PrivateKey() *rsa.PrivateKey {
	return r.privateKey
}

// KeySize returns the key size in bits.
func (r *RSACipher) KeySize() int {
	if r.publicKey != nil {
		return r.publicKey.N.BitLen()
	}
	if r.privateKey != nil {
		return r.privateKey.N.BitLen()
	}
	return 0
}

// encryptPKCS8PrivateKey encrypts a PKCS#8 private key using standard PBES2 (PKCS#5 v2.0).
// This produces encrypted keys compatible with OpenSSL and other standard tools.
// Uses PBKDF2-HMAC-SHA256 with 100,000 iterations and AES-256-CBC for encryption.
func encryptPKCS8PrivateKey(data []byte, passphrase []byte) ([]byte, error) {
	salt := make([]byte, pbkdf2SaltBytes)
	_, err := rand.Read(salt)
	if err != nil {
		return nil, apperror.NewError("generating salt failed").AddError(err)
	}

	iv := make([]byte, aes.BlockSize)
	_, err = rand.Read(iv)
	if err != nil {
		return nil, apperror.NewError("generating initialization vector failed").AddError(err)
	}

	// Derive key using PBKDF2 with SHA-256 and 100,000 iterations (NIST recommendation)
	key := pbkdf2.Key(passphrase, salt, pbkdf2Iterations, pbkdf2KeyBytes, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, apperror.NewError("creating AES cipher failed").AddError(err)
	}

	// Pad the data to block size (PKCS#7 padding)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	paddedData := make([]byte, len(data)+padding)
	copy(paddedData, data)
	for i := len(data); i < len(paddedData); i++ {
		paddedData[i] = byte(padding)
	}

	ciphertext := make([]byte, len(paddedData))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, paddedData)

	pbkdf2ParamsBytes, err := asn1.Marshal(pbkdf2Params{
		Salt:           salt,
		IterationCount: pbkdf2Iterations,
		KeyLength:      pbkdf2KeyBytes,
		PRF: algorithmIdentifier{
			Algorithm: oidHMACSHA256,
		},
	})
	if err != nil {
		return nil, apperror.NewError("encoding PBKDF2 parameters failed").AddError(err)
	}

	aesParamsBytes, err := asn1.Marshal(iv)
	if err != nil {
		return nil, apperror.NewError("encoding AES-256-CBC parameters failed").AddError(err)
	}

	pbes2ParamsBytes, err := asn1.Marshal(pbes2Params{
		KeyDerivationFunc: algorithmIdentifier{
			Algorithm:  oidPBKDF2,
			Parameters: asn1.RawValue{FullBytes: pbkdf2ParamsBytes},
		},
		EncryptionScheme: algorithmIdentifier{
			Algorithm:  oidAES256CBC,
			Parameters: asn1.RawValue{FullBytes: aesParamsBytes},
		},
	})
	if err != nil {
		return nil, apperror.NewError("encoding PBES2 parameters failed").AddError(err)
	}

	encryptedPKI := encryptedPrivateKeyInfo{
		EncryptionAlgorithm: algorithmIdentifier{
			Algorithm:  oidPBES2,
			Parameters: asn1.RawValue{FullBytes: pbes2ParamsBytes},
		},
		EncryptedData: ciphertext,
	}

	return asn1.Marshal(encryptedPKI)
}

// decryptPKCS8PrivateKey decrypts a standard PBES2-encrypted PKCS#8 private key.
// Compatible with keys encrypted by OpenSSL and other standard tools.
func decryptPKCS8PrivateKey(data []byte, passphrase []byte) ([]byte, error) {
	var encryptedPKI encryptedPrivateKeyInfo
	_, err := asn1.Unmarshal(data, &encryptedPKI)
	if err != nil {
		return nil, apperror.NewError("parsing encrypted private key info failed").AddError(err)
	}

	if !encryptedPKI.EncryptionAlgorithm.Algorithm.Equal(oidPBES2) {
		return nil, apperror.NewError("unsupported encryption algorithm, expected PBES2")
	}

	var pbes2Params pbes2Params
	_, err = asn1.Unmarshal(encryptedPKI.EncryptionAlgorithm.Parameters.FullBytes, &pbes2Params)
	if err != nil {
		return nil, apperror.NewError("parsing PBES2 parameters failed").AddError(err)
	}

	if !pbes2Params.KeyDerivationFunc.Algorithm.Equal(oidPBKDF2) {
		return nil, apperror.NewError("unsupported key derivation function, expected PBKDF2")
	}

	var pbkdf2Params pbkdf2Params
	_, err = asn1.Unmarshal(pbes2Params.KeyDerivationFunc.Parameters.FullBytes, &pbkdf2Params)
	if err != nil {
		return nil, apperror.NewError("parsing PBKDF2 parameters failed").AddError(err)
	}

	if !pbes2Params.EncryptionScheme.Algorithm.Equal(oidAES256CBC) {
		return nil, apperror.NewError("unsupported encryption scheme, expected AES-256-CBC")
	}

	var iv []byte
	_, err = asn1.Unmarshal(pbes2Params.EncryptionScheme.Parameters.FullBytes, &iv)
	if err != nil {
		return nil, apperror.NewError("parsing AES-256-CBC parameters failed").AddError(err)
	}

	keyLen := pbkdf2Params.KeyLength
	if keyLen == 0 {
		keyLen = pbkdf2KeyBytes // Default for AES-256
	}
	key := pbkdf2.Key(passphrase, pbkdf2Params.Salt, pbkdf2Params.IterationCount, keyLen, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, apperror.NewError("creating AES cipher failed").AddError(err)
	}

	if len(encryptedPKI.EncryptedData)%aes.BlockSize != 0 {
		return nil, apperror.NewError("ciphertext is not a multiple of block size")
	}

	plaintext := make([]byte, len(encryptedPKI.EncryptedData))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plaintext, encryptedPKI.EncryptedData)

	// Remove PKCS#7 padding
	padding := int(plaintext[len(plaintext)-1])
	if padding > aes.BlockSize || padding == 0 {
		return nil, apperror.NewError("invalid padding")
	}

	for i := len(plaintext) - padding; i < len(plaintext); i++ {
		if plaintext[i] != byte(padding) {
			return nil, apperror.NewError("invalid padding")
		}
	}

	return plaintext[:len(plaintext)-padding], nil
}
