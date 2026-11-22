package profiledir

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type pendingRecord struct {
	Version       int       `json:"version"`
	Upgrade       bool      `json:"upgrade"`
	Nickname      string    `json:"nickname"`
	CreatedAt     time.Time `json:"created_at"`
	PublicKey     string    `json:"public_key"`
	KeyID         string    `json:"key_id"`
	EncryptedSeed string    `json:"encrypted_seed"`
}

func validate(p app.PendingProfile) error {
	if err := p.Data.Profile.Validate(); err != nil {
		return err
	}
	if !p.Data.Profile.HasIdentity() || len(p.Data.EncryptedSeed) != 60 {
		return identity.ErrInvalidProfile
	}
	return identity.ValidateEncryptionKeyID(p.Data.KeyID)
}

func (s *Store) Stage(p app.PendingProfile) error {
	if err := validate(p); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pendingRecord{
		Version: 1, Upgrade: p.Upgrade, Nickname: p.Data.Profile.Nickname,
		CreatedAt: p.Data.Profile.CreatedAt.UTC(), PublicKey: hex.EncodeToString(p.Data.Profile.PublicKey[:]),
		KeyID: p.Data.KeyID, EncryptedSeed: base64.StdEncoding.EncodeToString(p.Data.EncryptedSeed),
	}, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.directory, ".profile-pending-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary.Name(), filepath.Join(s.directory, pendingName)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return app.ErrProfilePending
		}
		return err
	}
	return nil
}

func (s *Store) Pending() (app.PendingProfile, error) {
	present, err := s.exists(pendingName)
	if err != nil {
		return app.PendingProfile{}, err
	}
	if !present {
		return app.PendingProfile{}, app.ErrNoPendingProfile
	}
	file, err := os.Open(filepath.Join(s.directory, pendingName))
	if err != nil {
		return app.PendingProfile{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return app.PendingProfile{}, err
	}
	if len(data) > 4096 {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	var saved *pendingRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	if saved == nil || saved.Version != 1 {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	publicKey, err := hex.DecodeString(saved.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	if len(saved.EncryptedSeed) != 80 {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	encrypted, err := base64.StdEncoding.Strict().DecodeString(saved.EncryptedSeed)
	if err != nil {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	p := app.PendingProfile{Upgrade: saved.Upgrade, Data: app.ProfileData{
		Profile: identity.Profile{Nickname: saved.Nickname, CreatedAt: saved.CreatedAt.UTC()},
		KeyID:   saved.KeyID, EncryptedSeed: encrypted,
	}}
	copy(p.Data.Profile.PublicKey[:], publicKey)
	if err := validate(p); err != nil {
		return app.PendingProfile{}, identity.ErrInvalidProfile
	}
	return p, nil
}
