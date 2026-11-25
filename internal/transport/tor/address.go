package tor

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"

	"github.com/bacrehclb/ShadowTalk/internal/onion"
	"github.com/cretz/bine/control"
)

const virtualPort = "443"

// ValidateAddress accepts only canonical onion v3 hostnames with a valid
// checksum. URLs, IP addresses, ports and DNS names are never dial targets.
func ValidateAddress(address string) error {
	return onion.Validate(address)
}

func onionAddress(publicKey ed25519.PublicKey) string {
	return onion.FromPublicKey([32]byte(publicKey))
}

// Tor expects an expanded scalar and prefix, not Go's seed || public key.
// Only the wire representation is implemented here; signing stays in Tor.
type serviceKey [64]byte

func newServiceKey(seed []byte) serviceKey {
	key := serviceKey(sha512.Sum512(seed))
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
	return key
}

func (*serviceKey) Type() control.KeyType { return control.KeyTypeED25519V3 }
func (k *serviceKey) Blob() string        { return base64.StdEncoding.EncodeToString(k[:]) }

type onionAddr string

func (onionAddr) Network() string  { return "tor" }
func (a onionAddr) String() string { return string(a) + ":" + virtualPort }
