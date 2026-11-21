package identity

import (
	"errors"
	"testing"
	"time"
)

func TestProfileRejectsInvalidCreationTime(t *testing.T) {
	for _, createdAt := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		profile := Profile{Nickname: "ivan", CreatedAt: createdAt}
		if err := profile.Validate(); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("got %v, want ErrInvalidProfile", err)
		}
	}
}
