package app

import (
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
)

// ContactStore is bound to one profile identity. Update holds an interprocess
// lock through load, callback and publication. Callback failure leaves saved
// contact data unchanged (the repository may still create its lock file).
type ContactStore interface {
	Read() (*contacts.Book, error)
	Update(func(*contacts.Book) error) error
}

type Contacts struct{ store ContactStore }

func NewContacts(store ContactStore) *Contacts { return &Contacts{store: store} }

func (c *Contacts) Import(token, alias, fingerprint string, now time.Time) (contacts.Contact, error) {
	confirmed, err := contacts.ConfirmInvitation(token, alias, fingerprint, now)
	if err != nil {
		return contacts.Contact{}, err
	}
	var saved contacts.Contact
	err = c.store.Update(func(book *contacts.Book) error {
		var err error
		saved, err = book.Add(confirmed)
		return err
	})
	if err != nil {
		return contacts.Contact{}, err
	}
	return saved, nil
}

func (c *Contacts) List() ([]contacts.Contact, error) {
	book, err := c.store.Read()
	if err != nil {
		return nil, err
	}
	return book.List(), nil
}

func (c *Contacts) Get(alias string) (contacts.Contact, error) {
	book, err := c.store.Read()
	if err != nil {
		return contacts.Contact{}, err
	}
	return book.Get(alias)
}

func (c *Contacts) Remove(alias, fingerprint string) error {
	return c.store.Update(func(book *contacts.Book) error { return book.Remove(alias, fingerprint) })
}
