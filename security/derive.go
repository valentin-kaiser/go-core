package security

import (
	"crypto/sha512"
	"encoding/base64"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

func DeriveKey(secret, salt string) []byte {
	return pbkdf2.Key([]byte(secret), []byte(salt), 500000, 64, sha512.New)
}

func DeriveBase64Key(secret, salt string) string {
	return base64.StdEncoding.EncodeToString(pbkdf2.Key([]byte(secret), []byte(salt), 500000, 64, sha512.New))
}

func DeriveArgon2Key(secret string, salt string) []byte {
	return argon2.IDKey([]byte(secret), []byte(salt), 4, 64*1024, 4, 64)
}

func DeriveArgon2Base64Key(secret string, salt string) string {
	return base64.StdEncoding.EncodeToString(argon2.IDKey([]byte(secret), []byte(salt), 4, 64*1024, 4, 64))
}
