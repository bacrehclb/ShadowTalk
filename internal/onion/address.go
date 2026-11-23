// Package onion encodes and validates onion v3 names without starting Tor.
package onion

import (
	"bytes"
	"crypto/sha3"
	"encoding/base32"
	"errors"
	"strings"
)

var ErrInvalidAddress = errors.New("invalid onion v3 address")

// Validate accepts a canonical lowercase hostname, including its checksum.
// URLs, ports, IP addresses and ordinary DNS names are rejected.
func Validate(address string) error {
	if len(address) != 62 || !strings.HasSuffix(address, ".onion") || address != strings.ToLower(address) {
		return ErrInvalidAddress
	}
	decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(address[:56]))
	if err != nil || len(decoded) != 35 || decoded[34] != 3 {
		return ErrInvalidAddress
	}
	sum := checksum(decoded[:32])
	if !bytes.Equal(decoded[32:34], sum[:2]) {
		return ErrInvalidAddress
	}
	return nil
}

func checksum(publicKey []byte) [32]byte {
	input := append([]byte(".onion checksum"), publicKey...)
	return sha3.Sum256(append(input, 3))
}

// FromPublicKey takes the service's public key, not the profile's signing key.
func FromPublicKey(publicKey [32]byte) string {
	sum := checksum(publicKey[:])
	data := append(bytes.Clone(publicKey[:]), sum[:2]...)
	return strings.ToLower(base32.StdEncoding.EncodeToString(append(data, 3))) + ".onion"
}
