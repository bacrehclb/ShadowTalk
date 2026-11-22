package tor

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

func TestOnionAddressAndExpandedKeyVector(t *testing.T) {
	// RFC 8032 test 1 seed/public key. The Tor address was computed separately
	// with Python hashlib.sha3_256 and base64.b32encode.
	seed, _ := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	key := ed25519.NewKeyFromSeed(seed)
	const expected = "25njqamcweflpvkl73j4szahhihoc4xt3ktcgjnpaingr5yhkenl5sid.onion"
	address := onionAddress(key.Public().(ed25519.PublicKey))
	if address != expected || ValidateAddress(address) != nil {
		t.Fatalf("onion address: %s", address)
	}
	expanded := newServiceKey(seed)
	const expectedExpanded = "307c83864f2833cb427a2ef1c00a013cfdff2768d980c0a3a520f006904de94f9b4f0afe280b746a778684e75442502057b7473a03f08f96f5a38e9287e01f8f"
	if hex.EncodeToString(expanded[:]) != expectedExpanded {
		t.Fatal("expanded Tor key differs from the independent vector")
	}
}

func TestValidateAddressRejectsNonOnionDestinations(t *testing.T) {
	valid := onionAddress(ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey))
	badChecksum := []byte(valid)
	badChecksum[54] = 'a'
	if string(badChecksum) == valid {
		badChecksum[54] = 'b'
	}
	for _, address := range []string{
		"", "localhost", "127.0.0.1", "[::1]", "example.com", "abcdefghijklmnop.onion",
		strings.ToUpper(valid), valid + ":443", "https://" + valid, " " + valid,
		valid + "\n", strings.Repeat("a", 56) + ".onion", string(badChecksum),
	} {
		if ValidateAddress(address) == nil {
			t.Errorf("accepted %q", address)
		}
	}
}

func FuzzValidateAddress(f *testing.F) {
	f.Add(onionAddress(ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)))
	f.Add("127.0.0.1:9050")
	f.Fuzz(func(t *testing.T, address string) {
		if ValidateAddress(address) == nil && (len(address) != 62 || strings.ContainsAny(address, "\r\n /:")) {
			t.Fatal("invalid destination accepted")
		}
	})
}
