package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

var (
	ErrProfileBusy      = errors.New("profile is in use by another operation")
	ErrProfilePending   = errors.New("profile creation needs recovery")
	ErrNoPendingProfile = errors.New("no pending profile creation")
	ErrProfileConflict  = errors.New("profile files do not match the pending identity")
	ErrIdentityMismatch = errors.New("restored identity does not match the profile")
	ErrCreationAborted  = errors.New("unfinished creation had no saved key and was cancelled")
)

// ProfileData contains public metadata and ciphertext, never a plaintext secret.
type ProfileData struct {
	Profile       identity.Profile
	KeyID         string
	EncryptedSeed []byte
}

type PendingProfile struct {
	Data    ProfileData
	Upgrade bool
}

// ProfileFiles coordinates immutable files and a recovery journal. Mutations
// require Lock. Commit must be restartable and preserve conflicting files.
// Abort may clear the journal only if none of its identity files were published.
type ProfileFiles interface {
	Load() (ProfileData, error)
	Lock() (release func() error, err error)
	Pending() (PendingProfile, error)
	Stage(PendingProfile) error
	Commit(PendingProfile) error
	Finish() error
	Abort(PendingProfile) error
}

// EncryptionKeys returns caller-owned bytes. Missing keys must be reported as
// identity.ErrEncryptionKeyNotFound. Errors must not contain secret material.
type EncryptionKeys interface {
	EncryptionKeyReader
	Set(id string, key []byte) error
	Delete(id string) error
}

// LoadedProfile includes private keys only for profiles with HasIdentity().
// Legacy metadata can be displayed but must be upgraded before communication.
type LoadedProfile struct {
	Profile identity.Profile
	Keys    identity.KeyPair
}

type Profiles struct {
	files ProfileFiles
	keys  EncryptionKeys
}

func NewProfiles(files ProfileFiles, keys EncryptionKeys) *Profiles {
	return &Profiles{files: files, keys: keys}
}

func (p *Profiles) Load() (LoadedProfile, error) {
	data, err := p.files.Load()
	if err != nil {
		return LoadedProfile{}, err
	}
	return p.restore(data)
}

func (p *Profiles) restore(data ProfileData) (LoadedProfile, error) {
	if !data.Profile.HasIdentity() {
		return LoadedProfile{Profile: data.Profile}, nil
	}
	keys, err := restoreIdentity(data.KeyID, data.EncryptedSeed, p.keys)
	if err != nil {
		return LoadedProfile{}, err
	}
	if !bytes.Equal(keys.PublicKey(), data.Profile.PublicKey[:]) {
		return LoadedProfile{}, ErrIdentityMismatch
	}
	return LoadedProfile{Profile: data.Profile, Keys: keys}, nil
}

func (p *Profiles) Create(nickname string) (LoadedProfile, error) {
	if err := identity.ValidateNickname(nickname); err != nil {
		return LoadedProfile{}, err
	}
	return p.change(func() (LoadedProfile, error) {
		if err := p.requireNoPending(); err != nil {
			return LoadedProfile{}, err
		}
		_, err := p.files.Load()
		if err == nil {
			return LoadedProfile{}, identity.ErrProfileExists
		}
		if !errors.Is(err, identity.ErrProfileNotFound) {
			return LoadedProfile{}, err
		}
		return p.create(identity.Profile{Nickname: nickname, CreatedAt: time.Now().UTC()}, false)
	})
}

// Upgrade is explicit: loading legacy metadata never generates new keys.
func (p *Profiles) Upgrade() (LoadedProfile, error) {
	// A failed command for a missing profile must not create its directory.
	if _, err := p.files.Load(); err != nil {
		return LoadedProfile{}, err
	}
	return p.change(func() (LoadedProfile, error) {
		if err := p.requireNoPending(); err != nil {
			return LoadedProfile{}, err
		}
		old, err := p.files.Load()
		if err != nil {
			return LoadedProfile{}, err
		}
		if old.Profile.HasIdentity() {
			return LoadedProfile{}, identity.ErrProfileExists
		}
		return p.create(old.Profile, true)
	})
}

func (p *Profiles) create(profile identity.Profile, upgrade bool) (LoadedProfile, error) {
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		return LoadedProfile{}, err
	}
	seed, err := keys.Seed()
	if err != nil {
		return LoadedProfile{}, err
	}
	defer clear(seed)
	key := make([]byte, identity.SeedEncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		return LoadedProfile{}, err
	}
	defer clear(key)
	var idBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return LoadedProfile{}, err
	}
	id := hex.EncodeToString(idBytes[:])
	// Never intentionally overwrite an existing key, even if an ID collision occurs.
	existing, err := p.keys.Get(id)
	clear(existing)
	if err == nil {
		return LoadedProfile{}, ErrProfileConflict
	}
	if !errors.Is(err, identity.ErrEncryptionKeyNotFound) {
		return LoadedProfile{}, err
	}
	encrypted, err := identity.EncryptSeed(seed, key)
	if err != nil {
		return LoadedProfile{}, err
	}
	copy(profile.PublicKey[:], keys.PublicKey())
	pending := PendingProfile{Data: ProfileData{Profile: profile, KeyID: id, EncryptedSeed: encrypted}, Upgrade: upgrade}
	if err := p.files.Stage(pending); err != nil {
		return LoadedProfile{}, err
	}
	if err := p.keys.Set(id, key); err != nil {
		// Set may have written before reporting failure. Only our newly allocated
		// key is eligible for cleanup; no identity file has been published yet.
		if cleanupErr := p.keys.Delete(id); cleanupErr != nil {
			return LoadedProfile{}, errors.Join(ErrProfilePending, err, cleanupErr)
		}
		if cleanupErr := p.files.Abort(pending); cleanupErr != nil {
			return LoadedProfile{}, errors.Join(ErrProfilePending, err, cleanupErr)
		}
		return LoadedProfile{}, err
	}
	if err := p.publish(pending); err != nil {
		return LoadedProfile{}, err
	}
	return LoadedProfile{Profile: profile, Keys: keys}, nil
}

// Recover resumes the journal's original identity, never generating a new one.
func (p *Profiles) Recover() (LoadedProfile, error) {
	if _, err := p.files.Pending(); err != nil {
		return LoadedProfile{}, err
	}
	return p.change(func() (LoadedProfile, error) {
		pending, err := p.files.Pending()
		if err != nil {
			return LoadedProfile{}, err
		}
		loaded, err := p.restore(pending.Data)
		if err != nil {
			if errors.Is(err, identity.ErrEncryptionKeyNotFound) {
				// Cancellation is safe only before any identity file was published.
				if abortErr := p.files.Abort(pending); abortErr != nil {
					return LoadedProfile{}, errors.Join(ErrProfilePending, err, abortErr)
				}
				return LoadedProfile{}, ErrCreationAborted
			}
			return LoadedProfile{}, errors.Join(ErrProfilePending, err)
		}
		if err := p.publish(pending); err != nil {
			return LoadedProfile{}, err
		}
		return loaded, nil
	})
}

func (p *Profiles) publish(pending PendingProfile) error {
	if err := p.files.Commit(pending); err != nil {
		return errors.Join(ErrProfilePending, err)
	}
	if err := p.files.Finish(); err != nil {
		return errors.Join(ErrProfilePending, err)
	}
	return nil
}

func (p *Profiles) requireNoPending() error {
	_, err := p.files.Pending()
	if errors.Is(err, ErrNoPendingProfile) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrProfilePending
}

func (p *Profiles) change(fn func() (LoadedProfile, error)) (result LoadedProfile, err error) {
	release, err := p.files.Lock()
	if err != nil {
		return LoadedProfile{}, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			result = LoadedProfile{}
			err = errors.Join(err, fmt.Errorf("release profile lock: %w", releaseErr))
		}
	}()
	return fn()
}
