package identity

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrProfileNotFound = errors.New("profile not found")
	ErrProfileExists   = errors.New("profile already exists")
	ErrInvalidProfile  = errors.New("invalid profile")
)

// Profile currently contains local metadata only, without cryptographic keys.
// Its representation is independent of the storage format and the interface.
type Profile struct {
	Nickname  string
	CreatedAt time.Time
}

func (p Profile) Validate() error {
	if err := ValidateNickname(p.Nickname); err != nil {
		return err
	}
	if p.CreatedAt.IsZero() || p.CreatedAt.Year() < 1 || p.CreatedAt.Year() > 9999 {
		return fmt.Errorf("%w: invalid creation time", ErrInvalidProfile)
	}
	return nil
}
