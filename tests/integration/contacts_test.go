package integration

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
)

func TestConfirmedContactsSupplyMutualTLSPinsAfterRestart(t *testing.T) {
	now := time.Now()
	alice, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	aliceToken, err := contacts.CreateInvitation(alice, "alice", now, contacts.DefaultLifetime)
	if err != nil {
		t.Fatal(err)
	}
	bobToken, err := contacts.CreateInvitation(bob, "bob", now, contacts.DefaultLifetime)
	if err != nil {
		t.Fatal(err)
	}
	aliceDir, bobDir := t.TempDir(), t.TempDir()
	alicePublic, bobPublic := [32]byte(alice.PublicKey()), [32]byte(bob.PublicKey())
	// Expected fingerprints come independently from the profiles, as they would
	// over a trusted channel. Merely trusting the received token is insufficient.
	aliceBook := app.NewContacts(contactsfile.New(aliceDir, alicePublic))
	bobBook := app.NewContacts(contactsfile.New(bobDir, bobPublic))
	if _, err := aliceBook.Import(bobToken, "bob", identity.PublicKeyFingerprint(bobPublic), now); err != nil {
		t.Fatal(err)
	}
	if _, err := bobBook.Import(aliceToken, "alice", identity.PublicKeyFingerprint(alicePublic), now); err != nil {
		t.Fatal(err)
	}
	bobPin, err := app.NewContacts(contactsfile.New(aliceDir, alicePublic)).Get("bob")
	if err != nil {
		t.Fatal(err)
	}
	alicePin, err := app.NewContacts(contactsfile.New(bobDir, bobPublic)).Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	aliceEndpoint, err := securechannel.New(alice, securechannel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	bobEndpoint, err := securechannel.New(bob, securechannel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	rawClient, rawServer := net.Pipe()
	defer rawClient.Close()
	defer rawServer.Close()
	serverDone := make(chan error, 1)
	go func() {
		server, err := bobEndpoint.Server(ctx, rawServer, alicePin.PublicKey[:])
		if err != nil {
			serverDone <- err
			return
		}
		_ = server.SetDeadline(time.Now().Add(3 * time.Second))
		defer server.Close()
		message := make([]byte, len("confirmed contact"))
		if _, err = io.ReadFull(server, message); err == nil && string(message) != "confirmed contact" {
			err = errors.New("unexpected message")
		}
		if err == nil {
			_, err = server.Write([]byte("trusted reply"))
		}
		serverDone <- err
	}()
	client, err := aliceEndpoint.Client(ctx, rawClient, bobPin.PublicKey[:])
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("confirmed contact")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("trusted reply"))
	if _, err := io.ReadFull(client, reply); err != nil || string(reply) != "trusted reply" {
		t.Fatalf("reply: %q, %v", reply, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
