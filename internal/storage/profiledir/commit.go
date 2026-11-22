package profiledir

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/seedfile"
)

func sameProfile(a, b identity.Profile) bool {
	return a.Nickname == b.Nickname && a.CreatedAt.Equal(b.CreatedAt) && a.PublicKey == b.PublicKey
}

func sameData(a, b app.ProfileData) bool {
	return sameProfile(a.Profile, b.Profile) && a.KeyID == b.KeyID && bytes.Equal(a.EncryptedSeed, b.EncryptedSeed)
}

func (s *Store) checkPending(p app.PendingProfile) error {
	saved, err := s.Pending()
	if err != nil {
		return err
	}
	if saved.Upgrade != p.Upgrade || !sameData(saved.Data, p.Data) {
		return app.ErrProfileConflict
	}
	return nil
}

func (s *Store) Commit(p app.PendingProfile) error {
	if err := s.checkPending(p); err != nil {
		return err
	}
	hasSeed, err := s.exists("identity-seed.json")
	if err != nil {
		return err
	}
	if hasSeed {
		id, encrypted, err := seedfile.New(s.directory).Load()
		if err != nil {
			return err
		}
		if id != p.Data.KeyID || !bytes.Equal(encrypted, p.Data.EncryptedSeed) {
			return app.ErrProfileConflict
		}
	} else if err := seedfile.New(s.directory).Create(p.Data.KeyID, p.Data.EncryptedSeed); err != nil {
		return err
	}

	hasProfile, err := s.exists("profile.json")
	if err != nil {
		return err
	}
	profiles := profilefile.New(s.directory)
	if !hasProfile {
		return profiles.Create(p.Data.Profile)
	}
	old, err := profiles.Load()
	if err != nil {
		return err
	}
	if sameProfile(old, p.Data.Profile) {
		return nil
	}
	if p.Upgrade && !old.HasIdentity() && old.Nickname == p.Data.Profile.Nickname && old.CreatedAt.Equal(p.Data.Profile.CreatedAt) {
		return profiles.Upgrade(p.Data.Profile)
	}
	return app.ErrProfileConflict
}

func (s *Store) Finish() error {
	p, err := s.Pending()
	if err != nil {
		return err
	}
	saved, err := s.loadCommitted()
	if err != nil {
		return err
	}
	if !sameData(saved, p.Data) {
		return app.ErrProfileConflict
	}
	return os.Remove(filepath.Join(s.directory, pendingName))
}

func (s *Store) Abort(p app.PendingProfile) error {
	if err := s.checkPending(p); err != nil {
		return err
	}
	hasSeed, err := s.exists("identity-seed.json")
	if err != nil {
		return err
	}
	if hasSeed {
		return app.ErrProfileConflict
	}
	hasProfile, err := s.exists("profile.json")
	if err != nil {
		return err
	}
	if p.Upgrade {
		if !hasProfile {
			return app.ErrProfileConflict
		}
		old, err := profilefile.New(s.directory).Load()
		if err != nil {
			return err
		}
		if old.HasIdentity() || old.Nickname != p.Data.Profile.Nickname || !old.CreatedAt.Equal(p.Data.Profile.CreatedAt) {
			return app.ErrProfileConflict
		}
	} else if hasProfile {
		return app.ErrProfileConflict
	}
	return os.Remove(filepath.Join(s.directory, pendingName))
}
