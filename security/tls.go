package security

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Generated certificate constants.
const (
	pemTypeCertificate = "CERTIFICATE"
	pemTypeRSAPrivate  = "RSA PRIVATE KEY"

	caKeyBits = 4096
	// caValidYears is the validity of generated CA certificates.
	caValidYears = 1
	certDirMode  = 0o700
)

// NewTLSConfig creates a *tls.Config from a certificate and optional CA pool.
func NewTLSConfig(cert tls.Certificate, caPool *x509.CertPool, clientAuth tls.ClientAuthType) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   clientAuth,
		MinVersion:   tls.VersionTLS12,
	}
}

// LoadCertAndConfig loads certificate, CA certs, and returns a configured *tls.Config.
func LoadCertAndConfig(certFile, keyFile, caFile string, clientAuth tls.ClientAuthType) (*tls.Config, error) {
	cert, err := LoadCertificate(certFile, keyFile)
	if err != nil {
		return nil, apperror.NewError("loading certificate failed").AddError(err)
	}

	err = ValidateCertificate(cert)
	if err != nil {
		return nil, apperror.NewError("validating certificate failed").AddError(err)
	}

	var caPool *x509.CertPool
	if caFile != "" {
		caPool, err = LoadCACertPool(caFile)
		if err != nil {
			return nil, apperror.NewError("loading CA certificate pool failed").AddError(err)
		}
	}

	return NewTLSConfig(cert, caPool, clientAuth), nil
}

// LoadCertificate loads a TLS certificate from the given cert and key file paths.
func LoadCertificate(certFile, keyFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, apperror.NewError("loading certificate failed").AddError(err)
	}
	return cert, nil
}

// LoadCACertPool loads a CA certificate pool from the given PEM file.
func LoadCACertPool(caFile string) (*x509.CertPool, error) {
	caCert, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, apperror.NewError("reading CA certificate failed").AddError(err)
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(caCert); !ok {
		return nil, apperror.NewError("appending CA certificate failed")
	}
	return pool, nil
}

// IsCertificateExpired returns true if the given certificate is expired.
func IsCertificateExpired(cert tls.Certificate) (bool, error) {
	if len(cert.Certificate) == 0 {
		return false, apperror.NewError("certificate chain is empty")
	}
	for _, c := range cert.Certificate {
		x509Cert, err := x509.ParseCertificate(c)
		if err != nil {
			return false, apperror.NewError("parsing certificate failed").AddError(err)
		}

		if time.Now().After(x509Cert.NotAfter) {
			return true, nil
		}
	}
	return false, nil
}

// ValidateCertificate checks if the certificate is valid (not expired and not before its valid date).
func ValidateCertificate(cert tls.Certificate) error {
	if len(cert.Certificate) == 0 {
		return apperror.NewError("certificate chain is empty")
	}
	for _, c := range cert.Certificate {
		x509Cert, err := x509.ParseCertificate(c)
		if err != nil {
			return apperror.NewError("parsing certificate failed").AddError(err)
		}

		now := time.Now()
		if now.Before(x509Cert.NotBefore) {
			return apperror.NewErrorf("certificate not valid before %s", x509Cert.NotBefore)
		}

		if now.After(x509Cert.NotAfter) {
			return apperror.NewErrorf("certificate expired on %s", x509Cert.NotAfter)
		}
	}
	return nil
}

// GenerateSelfSignedCertificate generates a self-signed certificate and private key.
// It returns the certificate, CA pool, and any error encountered.
// The certificate is valid for 1 year and can be used for both client and server authentication.
func GenerateSelfSignedCertificate(subject pkix.Name) (tls.Certificate, *x509.CertPool, error) {
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().Unix()),
		Subject:               subject,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(caValidYears, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	priv, err := rsa.GenerateKey(rand.Reader, caKeyBits)
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("generating private key failed").AddError(err)
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("creating certificate failed").AddError(err)
	}

	caPEM := new(bytes.Buffer)
	err = pem.Encode(caPEM, &pem.Block{
		Type:  pemTypeCertificate,
		Bytes: caBytes,
	})
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("encoding certificate failed").AddError(err)
	}

	keyPEM := new(bytes.Buffer)
	err = pem.Encode(keyPEM, &pem.Block{
		Type:  pemTypeRSAPrivate,
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("encoding private key failed").AddError(err)
	}

	cert, err := tls.X509KeyPair(caPEM.Bytes(), keyPEM.Bytes())
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("loading X509 key pair failed").AddError(err)
	}

	caPool := x509.NewCertPool()
	if ok := caPool.AppendCertsFromPEM(caPEM.Bytes()); !ok {
		return tls.Certificate{}, nil, apperror.NewError("appending CA certificate to pool failed")
	}
	return cert, caPool, nil
}

// WriteCertificate writes the given certificate and key to the specified file paths.
func WriteCertificate(cert tls.Certificate, certPath, keyPath string) error {
	err := os.MkdirAll(filepath.Dir(certPath), certDirMode)
	if err != nil {
		return apperror.NewError("creating certificate directory failed").AddError(err)
	}

	err = os.MkdirAll(filepath.Dir(keyPath), certDirMode)
	if err != nil {
		return apperror.NewError("creating key directory failed").AddError(err)
	}

	certOut, err := os.Create(filepath.Clean(certPath))
	if err != nil {
		return apperror.NewError("creating certificate file failed").AddError(err)
	}
	defer apperror.Catch(certOut.Close, "closing certificate file failed")

	for _, c := range cert.Certificate {
		err = pem.Encode(certOut, &pem.Block{
			Type:  pemTypeCertificate,
			Bytes: c,
		})
		if err != nil {
			return apperror.NewError("encoding certificate failed").AddError(err)
		}
	}

	keyOut, err := os.OpenFile(filepath.Clean(keyPath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, secretFileMode)
	if err != nil {
		return apperror.NewError("creating key file failed").AddError(err)
	}
	defer apperror.Catch(keyOut.Close, "closing key file failed")

	privBytes, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return apperror.NewError("marshaling private key failed").AddError(err)
	}

	err = pem.Encode(keyOut, &pem.Block{
		Type:  pemTypePrivateKey,
		Bytes: privBytes,
	})
	if err != nil {
		return apperror.NewError("encoding private key failed").AddError(err)
	}

	return nil
}
