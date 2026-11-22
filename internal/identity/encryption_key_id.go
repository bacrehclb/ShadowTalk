package identity

import "errors"

var ErrInvalidEncryptionKeyID = errors.New("key ID must contain 1 to 128 ASCII letters, digits, underscores or hyphens")

// ValidateEncryptionKeyID validates the non-secret reference shared by the
// encrypted seed file and the system key store. It does not generate an ID.
func ValidateEncryptionKeyID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return ErrInvalidEncryptionKeyID
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			continue
		}
		return ErrInvalidEncryptionKeyID
	}
	return nil
}
