package security

import (
	"crypto/sha512"
	"encoding/base64"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

// Key derivation parameters.
const (
	deriveIterations = 500000
	deriveKeyBytes   = 64

	argon2Iterations = 4
	// argon2MemoryKiB is the Argon2 memory cost in KiB (64 MB).
	argon2MemoryKiB = 64 * 1024
	argon2Threads   = 4
	argon2KeyBytes  = 64
)

// DeriveKey derives a 64-byte cryptographic key from the given secret and salt using PBKDF2.
// Parameters:
//   - secret: the source material (e.g., password) to derive the key from.
//   - salt: a cryptographic salt to ensure uniqueness of the derived key.
//
// Returns:
//   - a 64-byte derived key as a byte slice.
//
// Security parameters:
//   - 500,000 iterations of PBKDF2 using SHA-512 as the hash function.
func DeriveKey(secret, salt string) []byte {
	return pbkdf2.Key([]byte(secret), []byte(salt), deriveIterations, deriveKeyBytes, sha512.New)
}

// DeriveBase64Key returns a base64-encoded version of the PBKDF2-derived key.
func DeriveBase64Key(secret, salt string) string {
	return base64.StdEncoding.EncodeToString(pbkdf2.Key([]byte(secret), []byte(salt), deriveIterations, deriveKeyBytes, sha512.New))
}

// DeriveArgon2Key derives a key from the given secret and salt using Argon2id.
//
//   - Uses Argon2id, the recommended variant of Argon2 for password hashing and key derivation.
//   - Parameters: 4 iterations, 64 MB memory (64*1024 KB), 4 threads, 64-byte output.
//   - Prefer Argon2 over PBKDF2 when higher resistance to GPU/ASIC attacks is required,
//     as Argon2 is designed to be memory-hard and more secure against such attacks,
//     but note that it requires significantly more memory than PBKDF2.
func DeriveArgon2Key(secret string, salt string) []byte {
	return argon2.IDKey([]byte(secret), []byte(salt), argon2Iterations, argon2MemoryKiB, argon2Threads, argon2KeyBytes)
}

// DeriveArgon2Base64Key returns a base64-encoded version of the Argon2-derived key.
func DeriveArgon2Base64Key(secret string, salt string) string {
	return base64.StdEncoding.EncodeToString(argon2.IDKey([]byte(secret), []byte(salt), argon2Iterations, argon2MemoryKiB, argon2Threads, argon2KeyBytes))
}
