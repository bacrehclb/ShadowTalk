package identity

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateEncryptionKeyID(t *testing.T) {
	for _, id := range []string{"a", "ABC_xyz-012", strings.Repeat("a", 128)} {
		if err := ValidateEncryptionKeyID(id); err != nil {
			t.Fatalf("valid key ID rejected: %v", err)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 129), "a b", "a\nb", "a\x00b", "иван", "a:b", "../a"} {
		if err := ValidateEncryptionKeyID(id); !errors.Is(err, ErrInvalidEncryptionKeyID) {
			t.Fatalf("got %v, want ErrInvalidEncryptionKeyID", err)
		}
	}
}
