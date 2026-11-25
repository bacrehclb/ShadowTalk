package securechannel

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"
)

const maxCertificateSize = 4096

func newCertificate(signer crypto.Signer, now time.Time) (tls.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ShadowTalk"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: signer, Leaf: leaf}, nil
}

func verifyPeer(state tls.ConnectionState, expected map[[ed25519.PublicKeySize]byte]struct{}, usage x509.ExtKeyUsage, now time.Time) error {
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != Protocol || state.DidResume {
		return ErrProtocol
	}
	if len(state.PeerCertificates) != 1 {
		return fmt.Errorf("%w: expected one self-signed leaf", ErrInvalidCertificate)
	}
	certificate := state.PeerCertificates[0]
	if len(certificate.Raw) > maxCertificateSize {
		return fmt.Errorf("%w: certificate is too large", ErrInvalidCertificate)
	}
	publicKey, ok := certificate.PublicKey.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize || certificate.SignatureAlgorithm != x509.PureEd25519 {
		return fmt.Errorf("%w: Ed25519 is required", ErrInvalidCertificate)
	}
	if _, trusted := expected[[ed25519.PublicKeySize]byte(publicKey)]; !trusted {
		return ErrPeerKeyMismatch
	}
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return fmt.Errorf("%w: certificate is outside its validity interval", ErrInvalidCertificate)
	}
	if certificate.IsCA || certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 ||
		len(certificate.UnhandledCriticalExtensions) != 0 || !bytes.Equal(certificate.RawIssuer, certificate.RawSubject) {
		return fmt.Errorf("%w: unsupported certificate constraints", ErrInvalidCertificate)
	}
	allowed := false
	for _, declared := range certificate.ExtKeyUsage {
		if declared == usage {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("%w: incorrect authentication usage", ErrInvalidCertificate)
	}
	// CheckSignatureFrom requires a CA certificate; these certificates are
	// self-signed leaves. Verify the signature directly with their pinned key.
	if err := certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature); err != nil {
		return fmt.Errorf("%w: invalid self-signature", ErrInvalidCertificate)
	}
	return nil
}
