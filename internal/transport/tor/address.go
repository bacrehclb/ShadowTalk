package tor

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"strings"

	"github.com/cretz/bine/control"
)

const virtualPort = "443"

// ValidateAddress accepts only canonical onion v3 hostnames with a valid
// checksum. URLs, IP addresses, ports and DNS names are never dial targets.
func ValidateAddress(address string) error {
	if len(address) != 62 || !strings.HasSuffix(address, ".onion") || address != strings.ToLower(address) {
		return ErrInvalidAddress
	}
	decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(address[:56]))
	if err != nil || len(decoded) != 35 || decoded[34] != 3 {
		return ErrInvalidAddress
	}
	checksum := onionChecksum(decoded[:32])
	if !bytes.Equal(decoded[32:34], checksum[:2]) {
		return ErrInvalidAddress
	}
	return nil
}

func onionChecksum(publicKey []byte) [32]byte {
	input := append([]byte(".onion checksum"), publicKey...)
	return sha3.Sum256(append(input, 3))
}

func onionAddress(publicKey ed25519.PublicKey) string {
	checksum := onionChecksum(publicKey)
	data := append(bytes.Clone(publicKey), checksum[:2]...)
	data = append(data, 3)
	return strings.ToLower(base32.StdEncoding.EncodeToString(data)) + ".onion"
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
