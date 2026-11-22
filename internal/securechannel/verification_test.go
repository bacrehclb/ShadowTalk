package securechannel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

func fixtureCertificate(t *testing.T, keys identity.KeyPair, change func(*x509.Certificate)) tls.Certificate {
	t.Helper()
	signer, err := keys.Signer()
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test peer"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if change != nil {
		change(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: signer}
}

func rawTLSConfig(certificate tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate}, NextProtos: []string{Protocol},
		// This is a test adversary. The production endpoint always checks pins.
		InsecureSkipVerify: true, ClientAuth: tls.RequireAnyClientCert,
	}
}

func againstRawTLS(t *testing.T, endpoint *Endpoint, expected ed25519.PublicKey, config *tls.Config, client bool, confirmation byte) result {
	t.Helper()
	a, b := testPipe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		defer b.Close()
		peer := tls.Client(b, config)
		if client {
			peer = tls.Server(b, config)
		}
		if err := peer.HandshakeContext(ctx); err != nil {
			return
		}
		if client {
			_, _ = peer.Write([]byte{confirmation})
		} else {
			_, _ = io.ReadFull(peer, make([]byte, 1))
		}
	}()
	var connection *Conn
	var err error
	if client {
		connection, err = endpoint.Client(ctx, a, expected)
	} else {
		connection, err = endpoint.Server(ctx, a, expected)
	}
	<-peerDone
	return result{connection, err}
}

func TestInvalidCertificatesRejectedOnBothSides(t *testing.T) {
	for _, client := range []bool{true, false} {
		role := "server rejects client"
		if client {
			role = "client rejects server"
		}
		t.Run(role, func(t *testing.T) {
			_, endpoint := testIdentity(t)
			peerKeys, _ := testIdentity(t)
			cases := []struct {
				name             string
				change           func(*x509.Certificate)
				corruptSignature bool
			}{
				{"expired", func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) }, false},
				{"not yet valid", func(c *x509.Certificate) { c.NotBefore = time.Now().Add(time.Minute) }, false},
				{"CA certificate", func(c *x509.Certificate) { c.IsCA = true }, false},
				{"no signing usage", func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageKeyEncipherment }, false},
				{"wrong extended usage", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning} }, false},
				{"unknown critical extension", func(c *x509.Certificate) {
					c.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 2, 3, 4}, Critical: true, Value: []byte{5, 0}}}
				}, false},
				{"oversized", func(c *x509.Certificate) {
					c.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 2, 3, 4}, Value: make([]byte, maxCertificateSize)}}
				}, false},
				{"invalid self-signature", nil, true},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					certificate := fixtureCertificate(t, peerKeys, test.change)
					if test.corruptSignature {
						certificate.Certificate[0][len(certificate.Certificate[0])-1] ^= 1
					}
					got := againstRawTLS(t, endpoint, peerKeys.PublicKey(), rawTLSConfig(certificate), client, serverReady)
					if got.connection != nil || !errors.Is(got.err, ErrInvalidCertificate) {
						t.Fatalf("invalid certificate accepted or wrong error: %v", got.err)
					}
				})
			}
		})
	}
}

func TestProtocolAndCertificatePossessionAreMandatory(t *testing.T) {
	_, endpoint := testIdentity(t)
	peerKeys, _ := testIdentity(t)
	otherKeys, _ := testIdentity(t)
	for _, client := range []bool{true, false} {
		for _, mode := range []string{"missing ALPN", "different ALPN", "TLS 1.2", "certificate without private key", "extra certificate"} {
			role := "accept/"
			if client {
				role = "connect/"
			}
			t.Run(role+mode, func(t *testing.T) {
				certificate := fixtureCertificate(t, peerKeys, nil)
				config := rawTLSConfig(certificate)
				switch mode {
				case "missing ALPN":
					config.NextProtos = nil
				case "different ALPN":
					config.NextProtos = []string{"not-shadowtalk"}
				case "TLS 1.2":
					config.MinVersion, config.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
				case "certificate without private key":
					config.Certificates[0].PrivateKey, _ = otherKeys.Signer()
				case "extra certificate":
					config.Certificates[0].Certificate = append(config.Certificates[0].Certificate, certificate.Certificate[0])
				}
				got := againstRawTLS(t, endpoint, peerKeys.PublicKey(), config, client, serverReady)
				if got.connection != nil || !errors.Is(got.err, ErrHandshake) {
					t.Fatalf("invalid peer accepted: %v", got.err)
				}
			})
		}
	}
	t.Run("no client certificate", func(t *testing.T) {
		config := rawTLSConfig(tls.Certificate{})
		config.Certificates = nil
		got := againstRawTLS(t, endpoint, peerKeys.PublicKey(), config, false, serverReady)
		if got.connection != nil || !errors.Is(got.err, ErrHandshake) {
			t.Fatal("anonymous client accepted")
		}
	})
	t.Run("invalid readiness marker", func(t *testing.T) {
		got := againstRawTLS(t, endpoint, peerKeys.PublicKey(), rawTLSConfig(fixtureCertificate(t, peerKeys, nil)), true, 255)
		if got.connection != nil || !errors.Is(got.err, ErrProtocol) {
			t.Fatalf("invalid readiness accepted: %v", got.err)
		}
	})
}

func TestWaitingForPeerConfirmationIsCancellableAndBounded(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			_, endpoint := testIdentity(t)
			peerKeys, _ := testIdentity(t)
			a, b := testPipe(t)
			config := rawTLSConfig(fixtureCertificate(t, peerKeys, nil))
			peer := tls.Server(b, config)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				endpoint.timeout = 200 * time.Millisecond
			}
			finished := make(chan result, 1)
			go func() {
				connection, err := endpoint.Client(ctx, a, peerKeys.PublicKey())
				finished <- result{connection, err}
			}()
			// TLS succeeds, but this peer never sends the encrypted ready byte.
			if err := peer.HandshakeContext(ctx); err != nil {
				t.Fatal(err)
			}
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
				timer := time.AfterFunc(20*time.Millisecond, cancel)
				defer timer.Stop()
			}
			select {
			case got := <-finished:
				if got.connection != nil || !errors.Is(got.err, want) || !a.isClosed() {
					t.Fatalf("unconfirmed channel returned: %v", got.err)
				}
			case <-time.After(time.Second):
				t.Fatal("confirmation wait ignored cancellation/timeout")
			}
		})
	}
}
