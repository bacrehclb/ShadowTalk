package identity

import (
	"errors"
	"testing"
	"time"
)

func TestProfileFingerprintFormat(t *testing.T) {
	var profile Profile
	if profile.HasIdentity() || profile.Fingerprint() != "" {
		t.Fatal("legacy metadata has a fingerprint")
	}
	for i := range profile.PublicKey {
		profile.PublicKey[i] = byte(i)
	}
	if got := profile.Fingerprint(); got != "SHA256:630dcd2966c4336691125448bbb25b4ff412a49c732db2c8abc1b8581bd710dd" {
		t.Fatalf("unexpected fingerprint: %s", got)
	}
}

func TestProfileRejectsInvalidCreationTime(t *testing.T) {
	for _, createdAt := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		profile := Profile{Nickname: "ivan", CreatedAt: createdAt}
		if err := profile.Validate(); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("got %v, want ErrInvalidProfile", err)
		}
	}
}
