// Package profiledir coordinates profile metadata, encrypted seed and recovery.
package profiledir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/seedfile"
	"github.com/gofrs/flock"
)

const pendingName = "profile-pending.json"

type Store struct{ directory string }

func New(directory string) *Store { return &Store{directory: directory} }

func (s *Store) Lock() (func() error, error) {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return nil, err
	}
	if _, err := s.exists(".profile.lock"); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(s.directory, ".profile.lock"), flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock profile: %w", err)
	}
	if !locked {
		_ = lock.Close()
		return nil, app.ErrProfileBusy
	}
	// Never remove the lock file: replacing its inode would split the lock.
	return lock.Close, nil
}

func (s *Store) Load() (app.ProfileData, error) {
	if err := s.noPending(); err != nil {
		return app.ProfileData{}, err
	}
	data, err := s.loadCommitted()
	if err != nil {
		return app.ProfileData{}, err
	}
	// Detect a writer which started after the first check.
	if err := s.noPending(); err != nil {
		return app.ProfileData{}, err
	}
	return data, nil
}

func (s *Store) noPending() error {
	_, err := s.Pending()
	if errors.Is(err, app.ErrNoPendingProfile) {
		return nil
	}
	if err != nil {
		return err
	}
	return app.ErrProfilePending
}

func (s *Store) loadCommitted() (app.ProfileData, error) {
	hasProfile, err := s.exists("profile.json")
	if err != nil {
		return app.ProfileData{}, err
	}
	hasSeed, err := s.exists("identity-seed.json")
	if err != nil {
		return app.ProfileData{}, err
	}
	if !hasProfile {
		if hasSeed {
			return app.ProfileData{}, app.ErrProfileConflict
		}
		return app.ProfileData{}, identity.ErrProfileNotFound
	}
	profile, err := profilefile.New(s.directory).Load()
	if err != nil {
		return app.ProfileData{}, err
	}
	if !profile.HasIdentity() {
		if hasSeed {
			return app.ProfileData{}, app.ErrProfileConflict
		}
		return app.ProfileData{Profile: profile}, nil
	}
	if !hasSeed {
		return app.ProfileData{}, app.ErrProfileConflict
	}
	id, encrypted, err := seedfile.New(s.directory).Load()
	if err != nil {
		return app.ProfileData{}, err
	}
	return app.ProfileData{Profile: profile, KeyID: id, EncryptedSeed: encrypted}, nil
}

// exists also refuses links/special entries for the files this coordinator mutates.
// The directory itself may be reached through a symlink; the lock follows it too.
func (s *Store) exists(name string) (bool, error) {
	info, err := os.Lstat(filepath.Join(s.directory, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, identity.ErrInvalidProfile
	}
	return true, nil
}
