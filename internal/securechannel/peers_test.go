package securechannel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestServerForPeersSelectsTheAuthenticatedKey(t *testing.T) {
	clientKeys, client := testIdentity(t)
	serverKeys, server := testIdentity(t)
	otherKeys, _ := testIdentity(t)
	for _, trusted := range []bool{true, false} {
		name := "unknown"
		if trusted {
			name = "confirmed"
		}
		t.Run(name, func(t *testing.T) {
			a, b := testPipe(t)
			pins := []ed25519.PublicKey{otherKeys.PublicKey()}
			if trusted {
				pins = append(pins, clientKeys.PublicKey())
			}
			done := make(chan result, 1)
			go func() {
				conn, err := server.ServerForPeers(t.Context(), b, pins)
				done <- result{conn, err}
			}()
			conn, err := client.Client(t.Context(), a, serverKeys.PublicKey())
			accepted := <-done
			if !trusted {
				if conn != nil || err == nil || accepted.connection != nil || !errors.Is(accepted.err, ErrPeerKeyMismatch) {
					t.Fatalf("unknown peer accepted: client=%v server=%v", err, accepted.err)
				}
				return
			}
			if err != nil || accepted.err != nil {
				t.Fatalf("connect: %v, %v", err, accepted.err)
			}
			pins[1][0] ^= 0xff
			if !bytes.Equal(accepted.connection.PeerPublicKey(), clientKeys.PublicKey()) {
				t.Fatal("server returned a different or mutable peer identity")
			}
			// Closing the raw pipes avoids waiting for a TLS close-notify reader.
			_ = a.Close()
			_ = b.Close()
		})
	}
}

func TestServerForPeersRejectsInvalidSetsBeforeIO(t *testing.T) {
	keys, server := testIdentity(t)
	for _, pins := range [][]ed25519.PublicKey{
		nil, {}, {nil}, {make([]byte, 32)}, {keys.PublicKey(), make([]byte, 31)},
	} {
		a, _ := testPipe(t)
		conn, err := server.ServerForPeers(context.Background(), a, pins)
		if conn != nil || !errors.Is(err, ErrInvalidPeerKey) || !a.isClosed() {
			t.Fatalf("invalid pin set: %v, %v", conn, err)
		}
	}
}
