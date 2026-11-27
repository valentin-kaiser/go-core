package security

import (
	"io"
	"os"
	"path/filepath"

	"github.com/ProtonMail/gopenpgp/v3/constants"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/ProtonMail/gopenpgp/v3/profile"
	"github.com/valentin-kaiser/go-core/apperror"
)

// PGPCipher is a struct that provides methods for encrypting and decrypting data using PGP.
// It uses the gopenpgp library for PGP operations and supports both public key and passphrase-based encryption.
type PGPCipher struct {
	handle     *crypto.PGPHandle
	passphrase []byte
	privateKey string
	publicKey  string
	Error      error
}

// NewPGPCipher creates a new PGPCipher instance with the default PGP profile (RFC4880).
func NewPGPCipher(p *profile.Custom) *PGPCipher {
	if p == nil {
		p = profile.RFC4880()
	}
	return &PGPCipher{handle: crypto.PGPWithProfile(p)}
}

// Encrypt encrypts the given plaintext using the provided public key or passphrase.
func (p *PGPCipher) Encrypt(plaintext []byte, out io.Writer) *PGPCipher {
	switch {
	case len(p.publicKey) > 0:
		return p.EncryptWithPublicKey(plaintext, out)
	case len(p.passphrase) > 0:
		return p.EncryptWithPassword(plaintext, out)
	default:
		p.Error = apperror.NewError("no public key or passphrase provided")
		return p
	}
}

// Decrypt decrypts the given ciphertext using the provided private key or passphrase.
func (p *PGPCipher) Decrypt(ciphertext []byte, out io.Writer) *PGPCipher {
	switch {
	case len(p.privateKey) > 0:
		return p.DecryptWithPrivateKey(ciphertext, out)
	case len(p.passphrase) > 0:
		return p.DecryptWithPassword(ciphertext, out)
	default:
		p.Error = apperror.NewError("no private key or passphrase provided")
		return p
	}
}

// EncryptWithPassword encrypts the given plaintext using the provided passphrase.
// The passphrase must be set using the WithPassphrase method before calling this function.
func (p *PGPCipher) EncryptWithPassword(plaintext []byte, out io.Writer) *PGPCipher {
	if p.Error != nil {
		return p
	}

	if len(plaintext) == 0 {
		p.Error = apperror.NewError("no plaintext provided")
		return p
	}

	if len(p.passphrase) == 0 {
		p.Error = apperror.NewError("no passphrase provided")
		return p
	}

	if out == nil {
		p.Error = apperror.NewError("no output writer provided")
		return p
	}

	enc, err := p.handle.Encryption().Password(p.passphrase).New()
	if err != nil {
		p.Error = apperror.NewError("creating encryption handle failed").AddError(err)
		return p
	}

	message, err := enc.Encrypt(plaintext)
	if err != nil {
		p.Error = apperror.NewError("encrypting message failed").AddError(err)
		return p
	}

	armored, err := message.ArmorBytes()
	if err != nil {
		p.Error = apperror.NewError("armoring message failed").AddError(err)
		return p
	}

	_, err = out.Write(armored)
	if err != nil {
		p.Error = apperror.NewError("writing encrypted message failed").AddError(err)
		return p
	}

	return p
}

// EncryptWithPublicKey encrypts the given plaintext using the provided public key.
// The public key must be set using the WithPublicKey method before calling this function.
func (p *PGPCipher) EncryptWithPublicKey(plaintext []byte, out io.Writer) *PGPCipher {
	if p.Error != nil {
		return p
	}

	if len(plaintext) == 0 {
		p.Error = apperror.NewError("no plaintext provided")
		return p
	}

	if out == nil {
		p.Error = apperror.NewError("no output writer provided")
		return p
	}

	if len(p.publicKey) == 0 {
		p.Error = apperror.NewError("no public key provided")
		return p
	}

	pKey, err := crypto.NewKeyFromArmored(p.publicKey)
	if err != nil {
		p.Error = apperror.NewError("creating public key failed").AddError(err)
		return p
	}

	enc, err := p.handle.Encryption().Recipient(pKey).New()
	if err != nil {
		p.Error = apperror.NewError("creating encryption handle failed").AddError(err)
		return p
	}

	message, err := enc.Encrypt(plaintext)
	if err != nil {
		p.Error = apperror.NewError("encrypting message failed").AddError(err)
		return p
	}

	armored, err := message.ArmorBytes()
	if err != nil {
		p.Error = apperror.NewError("armoring message failed").AddError(err)
		return p
	}

	_, err = out.Write(armored)
	if err != nil {
		p.Error = apperror.NewError("writing encrypted message failed").AddError(err)
		return p
	}

	return p
}

// DecryptWithPassword decrypts the given ciphertext using the provided passphrase.
// The passphrase must be set using the WithPassphrase method before calling this function.
func (p *PGPCipher) DecryptWithPassword(ciphertext []byte, out io.Writer) *PGPCipher {
	if p.Error != nil {
		return p
	}

	if len(ciphertext) == 0 {
		p.Error = apperror.NewError("no ciphertext provided")
		return p
	}

	if len(p.passphrase) == 0 {
		p.Error = apperror.NewError("no passphrase provided")
		return p
	}

	if out == nil {
		p.Error = apperror.NewError("no output writer provided")
		return p
	}

	dec, err := p.handle.Decryption().Password(p.passphrase).New()
	if err != nil {
		p.Error = apperror.NewError("creating decryption handle failed").AddError(err)
		return p
	}

	message, err := dec.Decrypt(ciphertext, crypto.Armor)
	if err != nil {
		p.Error = apperror.NewError("decrypting message failed").AddError(err)
		return p
	}

	_, err = out.Write(message.Bytes())
	if err != nil {
		p.Error = apperror.NewError("writing decrypted message failed").AddError(err)
		return p
	}

	return p
}

// DecryptWithPrivateKey decrypts the given ciphertext using the provided private key.
// The private key must be set using the WithPrivateKey method before calling this function.
// When the private key is encrypted, the passphrase must also be set using the WithPassphrase method.
func (p *PGPCipher) DecryptWithPrivateKey(ciphertext []byte, out io.Writer) *PGPCipher {
	if p.Error != nil {
		return p
	}

	if len(ciphertext) == 0 {
		p.Error = apperror.NewError("no ciphertext provided")
		return p
	}

	if len(p.privateKey) == 0 {
		p.Error = apperror.NewError("no private key provided")
		return p
	}

	if out == nil {
		p.Error = apperror.NewError("no output writer provided")
		return p
	}

	pKey, err := crypto.NewPrivateKeyFromArmored(p.privateKey, p.passphrase)
	if err != nil {
		p.Error = apperror.NewError("creating private key failed").AddError(err)
		return p
	}

	dec, err := p.handle.Decryption().DecryptionKey(pKey).New()
	if err != nil {
		p.Error = apperror.NewError("creating decryption handle failed").AddError(err)
		return p
	}

	message, err := dec.Decrypt(ciphertext, crypto.Armor)
	if err != nil {
		p.Error = apperror.NewError("decrypting message failed").AddError(err)
		return p
	}

	_, err = out.Write(message.Bytes())
	if err != nil {
		p.Error = apperror.NewError("writing decrypted message failed").AddError(err)
		return p
	}

	return p
}

// WithPassphrase sets the passphrase for the PGPCipher instance.
func (p *PGPCipher) WithPassphrase(passphrase []byte) *PGPCipher {
	p.passphrase = passphrase
	return p
}

// WithPublicKey sets the public key for the PGPCipher instance.
func (p *PGPCipher) WithPublicKey(key string) *PGPCipher {
	p.publicKey = key
	return p
}

// WithPublicKeyFromFile sets the public key for the PGPCipher instance from a file.
func (p *PGPCipher) WithPublicKeyFromFile(filePath string) *PGPCipher {
	if len(filePath) > 0 && p.Error == nil {
		key, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			p.Error = apperror.NewError("reading public key file failed").AddError(err)
			return p
		}
		p.publicKey = string(key)
	}
	return p
}

// WithPrivateKey sets the private key for the PGPCipher instance.
func (p *PGPCipher) WithPrivateKey(key string) *PGPCipher {
	p.privateKey = key
	return p
}

// WithPrivateKeyFromFile sets the private key for the PGPCipher instance from a file.
func (p *PGPCipher) WithPrivateKeyFromFile(filePath string) *PGPCipher {
	if len(filePath) > 0 && p.Error == nil {
		key, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			p.Error = apperror.NewError("reading private key file failed").AddError(err)
			return p
		}
		p.privateKey = string(key)
	}
	return p
}

// GetPublicKey returns the public key set for the PGPCipher instance or an error if no public key is set.
func (p *PGPCipher) GetPublicKey() (string, error) {
	if len(p.publicKey) == 0 {
		return "", apperror.NewError("no public key set")
	}

	return p.publicKey, nil
}

// GetPrivateKey returns the private key set for the PGPCipher instance.
func (p *PGPCipher) GetPrivateKey() (string, error) {
	if len(p.privateKey) == 0 {
		return "", apperror.NewError("no private key set")
	}

	return p.privateKey, nil
}

// GenerateKeyPair generates a new key pair with the given name and email.
// To encrypt the private key, a passphrase must be set using the WithPassphrase method.
func (p *PGPCipher) GenerateKeyPair(name, email string) error {
	if len(name) == 0 {
		return apperror.NewError("no name provided")
	}

	if len(email) == 0 {
		return apperror.NewError("no email provided")
	}

	keyGen := p.handle.KeyGeneration().AddUserId(name, email).New()

	privateKey, err := keyGen.GenerateKeyWithSecurity(constants.HighSecurity)
	if err != nil {
		return apperror.NewError("generating key pair failed").AddError(err)
	}

	armored, err := privateKey.Armor()
	if err != nil {
		return apperror.NewError("armoring private key failed").AddError(err)
	}

	if len(p.passphrase) > 0 {
		locked, err := p.handle.LockKey(privateKey, p.passphrase)
		if err != nil {
			return apperror.NewError("locking private key failed").AddError(err)
		}
		armored, err = locked.Armor()
		if err != nil {
			return apperror.NewError("armoring private key failed").AddError(err)
		}
	}
	p.privateKey = armored

	armoredPub, err := privateKey.GetArmoredPublicKey()
	if err != nil {
		return apperror.NewError("getting public key failed").AddError(err)
	}
	p.publicKey = armoredPub

	return nil
}
