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
		return nil, apperror.Wrap(err)
	}

	err = ValidateCertificate(cert)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	var caPool *x509.CertPool
	if caFile != "" {
		caPool, err = LoadCACertPool(caFile)
		if err != nil {
			return nil, apperror.Wrap(err)
		}
	}

	return NewTLSConfig(cert, caPool, clientAuth), nil
}

// LoadCertificate loads a TLS certificate from the given cert and key file paths.
func LoadCertificate(certFile, keyFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, apperror.NewError("failed to load certificate").AddError(err)
	}
	return cert, nil
}

// LoadCACertPool loads a CA certificate pool from the given PEM file.
func LoadCACertPool(caFile string) (*x509.CertPool, error) {
	caCert, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, apperror.NewError("failed to read CA certificate").AddError(err)
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(caCert); !ok {
		return nil, apperror.NewError("failed to append CA certificate")
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
			return false, apperror.NewError("failed to parse certificate").AddError(err)
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
			return apperror.NewError("failed to parse certificate").AddError(err)
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
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("failed to generate private key").AddError(err)
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("failed to create certificate").AddError(err)
	}

	caPEM := new(bytes.Buffer)
	err = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("failed to encode certificate").AddError(err)
	}

	keyPEM := new(bytes.Buffer)
	err = pem.Encode(keyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("failed to encode private key").AddError(err)
	}

	cert, err := tls.X509KeyPair(caPEM.Bytes(), keyPEM.Bytes())
	if err != nil {
		return tls.Certificate{}, nil, apperror.NewError("failed to load X509 key pair").AddError(err)
	}

	caPool := x509.NewCertPool()
	if ok := caPool.AppendCertsFromPEM(caPEM.Bytes()); !ok {
		return tls.Certificate{}, nil, apperror.NewError("failed to append CA certificate to pool")
	}
	return cert, caPool, nil
}

// WriteCertificate writes the given certificate and key to the specified file paths.
func WriteCertificate(cert tls.Certificate, certPath, keyPath string) error {
	err := os.MkdirAll(filepath.Dir(certPath), 0700)
	if err != nil {
		return apperror.NewError("failed to create certificate directory").AddError(err)
	}

	err = os.MkdirAll(filepath.Dir(keyPath), 0700)
	if err != nil {
		return apperror.NewError("failed to create key directory").AddError(err)
	}

	certOut, err := os.Create(filepath.Clean(certPath))
	if err != nil {
		return apperror.NewError("failed to create certificate file").AddError(err)
	}
	defer certOut.Close()

	for _, c := range cert.Certificate {
		err = pem.Encode(certOut, &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: c,
		})
		if err != nil {
			return apperror.NewError("failed to encode certificate").AddError(err)
		}
	}

	keyOut, err := os.OpenFile(filepath.Clean(keyPath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return apperror.NewError("failed to create key file").AddError(err)
	}
	defer keyOut.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return apperror.NewError("failed to marshal private key").AddError(err)
	}

	err = pem.Encode(keyOut, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})
	if err != nil {
		return apperror.NewError("failed to encode private key").AddError(err)
	}

	return nil
}
