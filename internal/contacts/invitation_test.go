package contacts

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

var testTime = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// This fixture was independently signed with Python cryptography, using the
// public RFC 8032 test seed, hashlib HKDF/SHA3, and compact ordered JSON.
func TestIndependentInvitationV1Vector(t *testing.T) {
	data, err := os.ReadFile("testdata/invitation-v1.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Git may check out this text fixture with LF or CRLF. Remove its record
	// terminator before passing the unchanged token to the strict parser.
	token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\r\n"), "\n")
	invite, err := ParseInvitation(token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	keys, _ := identity.RestoreKeyPair(seed)
	address, err := keys.OnionAddress()
	if err != nil || invite.PublicKey != [32]byte(keys.PublicKey()) || invite.Onion != address || invite.Nickname != "Alice_01" {
		t.Fatal("independent wire/derivation vector changed")
	}
}

func testIdentity(t testing.TB) identity.KeyPair {
	t.Helper()
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func testInvitation(t testing.TB, keys identity.KeyPair) (string, Invitation) {
	t.Helper()
	token, err := CreateInvitation(keys, "Alice_01", testTime, DefaultLifetime)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := ParseInvitation(token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	return token, invite
}

func TestInvitationRoundTripAndRestoredIdentity(t *testing.T) {
	keys := testIdentity(t)
	token, invite := testInvitation(t, keys)
	seed, _ := keys.Seed()
	restored, _ := identity.RestoreKeyPair(seed)
	clear(seed)
	second, again := testInvitation(t, restored)
	address, _ := restored.OnionAddress()
	if invite.Onion != address || invite.PublicKey != [32]byte(keys.PublicKey()) || invite.Fingerprint() != (identity.Profile{PublicKey: invite.PublicKey}).Fingerprint() {
		t.Fatal("invitation lost profile identity")
	}
	if invite.Onion != again.Onion || invite.PublicKey != again.PublicKey || invite.ID == again.ID || token == second {
		t.Fatal("same profile must retain address/key but issue fresh invitation IDs")
	}
	if invite.Nickname != "Alice_01" || !invite.IssuedAt.Equal(testTime) || !invite.ExpiresAt.Equal(testTime.Add(DefaultLifetime)) {
		t.Fatalf("unexpected public metadata: %+v", invite)
	}
	if strings.ContainsAny(strings.TrimPrefix(token, InvitationPrefix), "\r\n +/=") || len(token) > MaxTokenSize {
		t.Fatal("noncanonical token")
	}
}

func TestInvitationTimeBoundaries(t *testing.T) {
	token, _ := testInvitation(t, testIdentity(t))
	for _, test := range []struct {
		now  time.Time
		want error
	}{
		{testTime.Add(-ClockSkew), nil},
		{testTime.Add(-ClockSkew - time.Nanosecond), ErrFutureInvitation},
		{testTime.Add(DefaultLifetime - time.Nanosecond), nil},
		{testTime.Add(DefaultLifetime), ErrExpiredInvitation},
		{time.Time{}, ErrInvalidInvitation},
	} {
		_, err := ParseInvitation(token, test.now)
		if !errors.Is(err, test.want) {
			t.Fatalf("at %s: %v, want %v", test.now, err, test.want)
		}
	}
	for _, lifetime := range []time.Duration{0, -time.Second, time.Millisecond, time.Second + time.Nanosecond, MaxLifetime + time.Second} {
		if _, err := CreateInvitation(testIdentity(t), "alice", testTime, lifetime); !errors.Is(err, ErrInvalidLifetime) {
			t.Fatalf("lifetime %s: %v", lifetime, err)
		}
	}
	if _, err := CreateInvitation(identity.KeyPair{}, "alice", testTime, DefaultLifetime); !errors.Is(err, identity.ErrInvalidKeyPair) {
		t.Fatal(err)
	}
	if _, err := CreateInvitation(testIdentity(t), "evil\x1b[2J", testTime, DefaultLifetime); err == nil {
		t.Fatal("accepted terminal controls")
	}
}

func decodeEnvelope(t testing.TB, token string) invitationEnvelope {
	t.Helper()
	data, err := decode(strings.TrimPrefix(token, InvitationPrefix))
	if err != nil {
		t.Fatal(err)
	}
	var envelope invitationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func encodeEnvelope(t testing.TB, envelope invitationEnvelope) string {
	t.Helper()
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return InvitationPrefix + encode(data)
}

func TestInvitationRejectsTamperingAndAmbiguousJSON(t *testing.T) {
	keys := testIdentity(t)
	token, _ := testInvitation(t, keys)
	for _, change := range []func(*invitationEnvelope){
		func(e *invitationEnvelope) { e.Payload.Nickname = "Mallory" },
		func(e *invitationEnvelope) { e.Payload.PublicKey = encode(testIdentity(t).PublicKey()) },
		func(e *invitationEnvelope) { e.Payload.Onion, _ = testIdentity(t).OnionAddress() },
		func(e *invitationEnvelope) { e.Payload.ExpiresAt++ },
		func(e *invitationEnvelope) { e.Payload.IssuedAt-- },
		func(e *invitationEnvelope) { e.Payload.ID = encode(make([]byte, 32)) },
		func(e *invitationEnvelope) { e.Signature = encode(make([]byte, 64)) },
	} {
		envelope := decodeEnvelope(t, token)
		change(&envelope)
		if _, err := ParseInvitation(encodeEnvelope(t, envelope), testTime); !errors.Is(err, ErrInvalidInvitation) {
			t.Fatalf("tampering accepted: %v", err)
		}
	}
	raw, _ := decode(strings.TrimPrefix(token, InvitationPrefix))
	for _, candidate := range []string{
		"", token + "=", token + "\n", " " + token,
		strings.Replace(token, "/v1/", "/v2/", 1), strings.Repeat("a", MaxTokenSize+1),
		InvitationPrefix + encode([]byte("null")),
		InvitationPrefix + encode(append(raw, '\n')),
		InvitationPrefix + encode([]byte(strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1))),
		InvitationPrefix + encode([]byte(strings.Replace(string(raw), `"version":1`, `"version":1,"extra":true`, 1))),
		InvitationPrefix + encode([]byte(strings.Replace(string(raw), `"nickname"`, `"Nickname"`, 1))),
	} {
		if _, err := ParseInvitation(candidate, testTime); !errors.Is(err, ErrInvalidInvitation) {
			t.Fatalf("malformed token accepted: %v", err)
		}
	}
}

func TestSignedInvalidClaimsAndOtherSigningDomainAreRejected(t *testing.T) {
	keys := testIdentity(t)
	token, _ := testInvitation(t, keys)
	seed, _ := keys.Seed()
	private := ed25519.NewKeyFromSeed(seed)
	defer clear(private)
	defer clear(seed)
	for _, change := range []func(*invitationPayload){
		func(p *invitationPayload) { p.Onion = "example.com" },
		func(p *invitationPayload) { p.Nickname = "alice\nforged" },
		func(p *invitationPayload) { p.ExpiresAt = p.IssuedAt },
		func(p *invitationPayload) { p.ExpiresAt = p.IssuedAt + int64(MaxLifetime/time.Second) + 1 },
		func(p *invitationPayload) { p.IssuedAt = -1 },
		func(p *invitationPayload) { p.Version = 2 },
		func(p *invitationPayload) { p.ID = encode([]byte{1}) },
	} {
		envelope := decodeEnvelope(t, token)
		change(&envelope.Payload)
		payload, _ := json.Marshal(envelope.Payload)
		envelope.Signature = encode(ed25519.Sign(private, append([]byte(signatureDomain), payload...)))
		if _, err := ParseInvitation(encodeEnvelope(t, envelope), testTime); !errors.Is(err, ErrInvalidInvitation) {
			t.Fatalf("signed invalid claim accepted: %v", err)
		}
	}
	envelope := decodeEnvelope(t, token)
	payload, _ := json.Marshal(envelope.Payload)
	envelope.Signature = encode(ed25519.Sign(private, payload))
	if _, err := ParseInvitation(encodeEnvelope(t, envelope), testTime); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatal("accepted signature without domain separation")
	}
}

func FuzzParseInvitation(f *testing.F) {
	token, _ := testInvitation(f, testIdentity(f))
	f.Add(token)
	f.Add(InvitationPrefix + "invalid")
	f.Fuzz(func(t *testing.T, token string) {
		invite, err := ParseInvitation(token, testTime)
		if err == nil && (invite.Fingerprint() == "" || len(invite.Onion) != 62 || !testTime.Before(invite.ExpiresAt)) {
			t.Fatal("accepted inconsistent invitation")
		}
		if err != nil && len(token) > 32 && strings.Contains(err.Error(), token) {
			t.Fatal("error echoed token")
		}
	})
}
