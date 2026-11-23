package contacts

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/onion"
)

const MaxContacts = 1000

var (
	ErrInvalidContact       = errors.New("invalid contact")
	ErrConfirmationRequired = errors.New("full fingerprint confirmation is required")
	ErrFingerprintMismatch  = errors.New("fingerprint does not match")
	ErrKeyChanged           = errors.New("contact key changed")
	ErrContactChanged       = errors.New("contact details changed")
	ErrDuplicateContact     = errors.New("contact key or onion address already saved under another alias")
	ErrSelfContact          = errors.New("cannot add own identity as a contact")
	ErrContactNotFound      = errors.New("contact not found")
	ErrContactLimit         = errors.New("contact limit reached")
)

type Contact struct {
	Alias     string
	Nickname  string
	Onion     string
	PublicKey [32]byte
	AddedAt   time.Time
}

func (c Contact) Fingerprint() string { return identity.PublicKeyFingerprint(c.PublicKey) }

func (c Contact) Validate() error {
	year := c.AddedAt.UTC().Year()
	if identity.ValidateNickname(c.Alias) != nil || identity.ValidateNickname(c.Nickname) != nil ||
		onion.Validate(c.Onion) != nil || c.PublicKey == [32]byte{} || year < 1970 || year > 9999 {
		return ErrInvalidContact
	}
	return nil
}

// ConfirmInvitation verifies the token again at import time and requires the
// exact full fingerprint obtained through a trusted channel. It does not save.
func ConfirmInvitation(token, alias, expectedFingerprint string, now time.Time) (Contact, error) {
	if expectedFingerprint == "" {
		return Contact{}, ErrConfirmationRequired
	}
	invite, err := ParseInvitation(token, now)
	if err != nil {
		return Contact{}, err
	}
	if expectedFingerprint != invite.Fingerprint() {
		return Contact{}, ErrFingerprintMismatch
	}
	c := Contact{Alias: alias, Nickname: invite.Nickname, Onion: invite.Onion, PublicKey: invite.PublicKey, AddedAt: now.UTC()}
	if err := c.Validate(); err != nil {
		return Contact{}, err
	}
	return c, nil
}

// Book owns an in-memory snapshot. Persistent mutation must be serialized by
// the repository. Returned contacts contain values, never shared mutable slices.
type Book struct {
	owner   [32]byte
	entries []Contact
}

func (b *Book) Owner() [32]byte { return b.owner }

func RestoreBook(owner [32]byte, entries []Contact) (*Book, error) {
	if owner == [32]byte{} {
		return nil, ErrInvalidContact
	}
	if len(entries) > MaxContacts {
		return nil, ErrContactLimit
	}
	b := &Book{owner: owner}
	for _, entry := range entries {
		before := len(b.entries)
		if _, err := b.Add(entry); err != nil {
			return nil, err
		}
		if len(b.entries) == before {
			return nil, ErrDuplicateContact
		}
	}
	return b, nil
}

func (b *Book) List() []Contact {
	entries := slices.Clone(b.entries)
	slices.SortFunc(entries, func(a, b Contact) int { return strings.Compare(a.Alias, b.Alias) })
	return entries
}

func (b *Book) Get(alias string) (Contact, error) {
	if identity.ValidateNickname(alias) != nil {
		return Contact{}, ErrInvalidContact
	}
	for _, entry := range b.entries {
		if entry.Alias == alias {
			return entry, nil
		}
	}
	return Contact{}, ErrContactNotFound
}

// Add never changes an existing pin. Reimporting identical details is idempotent
// and preserves the original AddedAt. Alias matching is case-sensitive.
func (b *Book) Add(contact Contact) (Contact, error) {
	if b.owner == [32]byte{} {
		return Contact{}, ErrInvalidContact
	}
	if err := contact.Validate(); err != nil {
		return Contact{}, err
	}
	if contact.PublicKey == b.owner {
		return Contact{}, ErrSelfContact
	}
	for _, saved := range b.entries {
		if saved.Alias == contact.Alias {
			if saved.PublicKey != contact.PublicKey {
				return Contact{}, ErrKeyChanged
			}
			if saved.Onion != contact.Onion || saved.Nickname != contact.Nickname {
				return Contact{}, ErrContactChanged
			}
			return saved, nil
		}
		if saved.PublicKey == contact.PublicKey || saved.Onion == contact.Onion {
			return Contact{}, ErrDuplicateContact
		}
	}
	if len(b.entries) >= MaxContacts {
		return Contact{}, ErrContactLimit
	}
	contact.AddedAt = contact.AddedAt.UTC()
	b.entries = append(b.entries, contact)
	return contact, nil
}

func (b *Book) Remove(alias, expectedFingerprint string) error {
	if expectedFingerprint == "" {
		return ErrConfirmationRequired
	}
	contact, err := b.Get(alias)
	if err != nil {
		return err
	}
	if contact.Fingerprint() != expectedFingerprint {
		return ErrFingerprintMismatch
	}
	for i, entry := range b.entries {
		if entry.Alias == alias {
			b.entries = slices.Delete(b.entries, i, i+1)
			return nil
		}
	}
	return ErrContactNotFound
}
