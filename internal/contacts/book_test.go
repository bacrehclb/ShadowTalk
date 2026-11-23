package contacts

import (
	"errors"
	"testing"
	"time"
)

func TestExplicitConfirmationAndImmutablePins(t *testing.T) {
	owner, peer := testIdentity(t), testIdentity(t)
	token, invite := testInvitation(t, peer)
	for _, test := range []struct {
		pin  string
		want error
	}{{"", ErrConfirmationRequired}, {"SHA256:short", ErrFingerprintMismatch}, {invite.Fingerprint()[7:], ErrFingerprintMismatch}} {
		if _, err := ConfirmInvitation(token, "alice", test.pin, testTime); !errors.Is(err, test.want) {
			t.Fatalf("confirmation: %v", err)
		}
	}
	contact, err := ConfirmInvitation(token, "alice", invite.Fingerprint(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	book, err := RestoreBook([32]byte(owner.PublicKey()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.Add(contact); err != nil {
		t.Fatal(err)
	}
	repeat := contact
	repeat.AddedAt = testTime.Add(time.Hour)
	saved, err := book.Add(repeat)
	if err != nil || saved != contact {
		t.Fatal("reimport changed a stored contact")
	}
	if got := book.List(); len(got) != 1 {
		t.Fatal("duplicate entry")
	} else {
		got[0].Alias = "changed"
	}
	if _, err := book.Get("alice"); err != nil {
		t.Fatal("List returned shared memory")
	}
	changed := contact
	changed.PublicKey = [32]byte(testIdentity(t).PublicKey())
	if _, err := book.Add(changed); !errors.Is(err, ErrKeyChanged) {
		t.Fatalf("silent key replacement: %v", err)
	}
	changed = contact
	changed.Onion, _ = testIdentity(t).OnionAddress()
	if _, err := book.Add(changed); !errors.Is(err, ErrContactChanged) {
		t.Fatal(err)
	}
	changed = contact
	changed.Alias = "other"
	if _, err := book.Add(changed); !errors.Is(err, ErrDuplicateContact) {
		t.Fatal(err)
	}
	selfToken, self := testInvitation(t, owner)
	selfContact, _ := ConfirmInvitation(selfToken, "myself", self.Fingerprint(), testTime)
	if _, err := book.Add(selfContact); !errors.Is(err, ErrSelfContact) {
		t.Fatal(err)
	}
	if err := book.Remove("alice", ""); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatal(err)
	}
	if err := book.Remove("alice", self.Fingerprint()); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatal(err)
	}
	if err := book.Remove("alice", invite.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Get("alice"); !errors.Is(err, ErrContactNotFound) {
		t.Fatal(err)
	}
}

func TestExpiredPreviewCannotBeConfirmedLater(t *testing.T) {
	token, invite := testInvitation(t, testIdentity(t))
	if _, err := ConfirmInvitation(token, "alice", invite.Fingerprint(), invite.ExpiresAt); !errors.Is(err, ErrExpiredInvitation) {
		t.Fatal(err)
	}
}

func TestRestoreRejectsDuplicateOrInvalidPins(t *testing.T) {
	owner := [32]byte(testIdentity(t).PublicKey())
	token, invite := testInvitation(t, testIdentity(t))
	contact, _ := ConfirmInvitation(token, "alice", invite.Fingerprint(), testTime)
	if _, err := new(Book).Add(contact); !errors.Is(err, ErrInvalidContact) {
		t.Fatal("zero book accepted contact")
	}
	if _, err := RestoreBook(owner, []Contact{contact, contact}); !errors.Is(err, ErrDuplicateContact) {
		t.Fatal(err)
	}
	if _, err := RestoreBook([32]byte{}, nil); !errors.Is(err, ErrInvalidContact) {
		t.Fatal(err)
	}
	if _, err := RestoreBook(owner, make([]Contact, MaxContacts+1)); !errors.Is(err, ErrContactLimit) {
		t.Fatal(err)
	}
	contact.Onion = "127.0.0.1"
	if _, err := RestoreBook(owner, []Contact{contact}); !errors.Is(err, ErrInvalidContact) {
		t.Fatal(err)
	}
}

func TestContactTimestampRemainsValidAfterUTCConversion(t *testing.T) {
	token, invite := testInvitation(t, testIdentity(t))
	contact, _ := ConfirmInvitation(token, "alice", invite.Fingerprint(), testTime)
	contact.AddedAt = time.Date(1970, 1, 1, 0, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	if !errors.Is(contact.Validate(), ErrInvalidContact) {
		t.Fatal("accepted timestamp outside supported UTC range")
	}
}
