// Package contacts defines signed invitations and explicitly pinned contacts.
// It has no filesystem, UI, Tor process or network dependencies.
package contacts

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/onion"
)

const (
	InvitationPrefix = "shadowtalk://invite/v1/"
	MaxTokenSize     = 2048
	DefaultLifetime  = 24 * time.Hour
	MaxLifetime      = 7 * 24 * time.Hour
	ClockSkew        = 5 * time.Minute
	signatureDomain  = "ShadowTalk/invitation/v1\x00"
)

var (
	ErrInvalidInvitation = errors.New("invalid invitation")
	ErrExpiredInvitation = errors.New("invitation expired")
	ErrFutureInvitation  = errors.New("invitation issued in the future")
	ErrInvalidLifetime   = errors.New("invitation lifetime must be whole seconds between 1 second and 7 days")
)

// Invitation is verified public information, not proof of a person's identity.
// ID is a public random identifier, not a bearer secret or a one-use capability.
type Invitation struct {
	Nickname  string
	Onion     string
	PublicKey [32]byte
	ID        [32]byte
	IssuedAt  time.Time
	ExpiresAt time.Time
}

func (i Invitation) Fingerprint() string { return identity.PublicKeyFingerprint(i.PublicKey) }

// These structs define the exact v1 canonical JSON and signing field order.
// Changing fields or their order requires a new protocol version.
type invitationPayload struct {
	Version   int    `json:"version"`
	Nickname  string `json:"nickname"`
	Onion     string `json:"onion"`
	PublicKey string `json:"public_key"`
	ID        string `json:"id"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
}

type invitationEnvelope struct {
	Payload   invitationPayload `json:"payload"`
	Signature string            `json:"signature"`
}

func ValidateLifetime(lifetime time.Duration) error {
	if lifetime < time.Second || lifetime > MaxLifetime || lifetime%time.Second != 0 {
		return ErrInvalidLifetime
	}
	return nil
}

// CreateInvitation signs a short-lived public contact invitation. It neither
// publishes an onion service nor grants remote access. Caller chooses lifetime.
func CreateInvitation(keys identity.KeyPair, nickname string, now time.Time, lifetime time.Duration) (string, error) {
	if err := identity.ValidateNickname(nickname); err != nil {
		return "", err
	}
	if err := ValidateLifetime(lifetime); err != nil {
		return "", err
	}
	issued := now.UTC().Truncate(time.Second)
	if issued.Year() < 1970 || issued.Add(lifetime).Year() > 9999 {
		return "", ErrInvalidInvitation
	}
	signer, err := keys.Signer()
	if err != nil {
		return "", err
	}
	address, err := keys.OnionAddress()
	if err != nil {
		return "", err
	}
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", ErrInvalidInvitation
	}
	payload := invitationPayload{
		Version: 1, Nickname: nickname, Onion: address,
		PublicKey: encode(keys.PublicKey()), ID: encode(id[:]),
		IssuedAt: issued.Unix(), ExpiresAt: issued.Add(lifetime).Unix(),
	}
	message, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalidInvitation
	}
	signature, err := signer.Sign(rand.Reader, append([]byte(signatureDomain), message...), crypto.Hash(0))
	if err != nil {
		return "", ErrInvalidInvitation
	}
	data, err := json.Marshal(invitationEnvelope{payload, encode(signature)})
	if err != nil {
		return "", ErrInvalidInvitation
	}
	return InvitationPrefix + encode(data), nil
}

// ParseInvitation is bounded, strictly canonical and never includes token data
// in errors. Import must call it again, even if a preview was previously shown.
func ParseInvitation(token string, now time.Time) (Invitation, error) {
	if len(token) > MaxTokenSize || !strings.HasPrefix(token, InvitationPrefix) {
		return Invitation{}, ErrInvalidInvitation
	}
	data, err := decode(token[len(InvitationPrefix):])
	if err != nil {
		return Invitation{}, ErrInvalidInvitation
	}
	var envelope invitationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Invitation{}, ErrInvalidInvitation
	}
	canonical, err := json.Marshal(envelope)
	// Also rejects unknown/duplicate fields, alternate escaping and field order.
	if err != nil || !bytes.Equal(canonical, data) {
		return Invitation{}, ErrInvalidInvitation
	}
	p := envelope.Payload
	public, err := decode(p.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return Invitation{}, ErrInvalidInvitation
	}
	id, err := decode(p.ID)
	if err != nil || len(id) != 32 {
		return Invitation{}, ErrInvalidInvitation
	}
	signature, err := decode(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Invitation{}, ErrInvalidInvitation
	}
	if p.Version != 1 || identity.ValidateNickname(p.Nickname) != nil || onion.Validate(p.Onion) != nil ||
		p.IssuedAt < 0 || p.ExpiresAt > 253402300799 || p.ExpiresAt <= p.IssuedAt || p.ExpiresAt-p.IssuedAt > int64(MaxLifetime/time.Second) {
		return Invitation{}, ErrInvalidInvitation
	}
	invite := Invitation{
		Nickname: p.Nickname, Onion: p.Onion, PublicKey: [32]byte(public), ID: [32]byte(id),
		IssuedAt: time.Unix(p.IssuedAt, 0).UTC(), ExpiresAt: time.Unix(p.ExpiresAt, 0).UTC(),
	}
	if invite.PublicKey == [32]byte{} || invite.ID == [32]byte{} {
		return Invitation{}, ErrInvalidInvitation
	}
	message, err := json.Marshal(p)
	if err != nil || !ed25519.Verify(public, append([]byte(signatureDomain), message...), signature) {
		return Invitation{}, ErrInvalidInvitation
	}
	now = now.UTC()
	if now.IsZero() || now.Year() < 1970 || now.Year() > 9999 {
		return Invitation{}, ErrInvalidInvitation
	}
	if !now.Before(invite.ExpiresAt) {
		return Invitation{}, ErrExpiredInvitation
	}
	if invite.IssuedAt.After(now.Add(ClockSkew)) {
		return Invitation{}, ErrFutureInvitation
	}
	return invite, nil
}

func encode(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func decode(value string) ([]byte, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || encode(data) != value {
		return nil, ErrInvalidInvitation
	}
	return data, nil
}
