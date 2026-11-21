package identity

import "errors"

const (
	MinNicknameLength = 3
	MaxNicknameLength = 24
)

var ErrInvalidNickname = errors.New("invalid nickname")

// ValidateNickname accepts ASCII letters, digits, underscores and hyphens.
// It does not trim or change the case of the user's input.
func ValidateNickname(nickname string) error {
	if len(nickname) < MinNicknameLength || len(nickname) > MaxNicknameLength {
		return ErrInvalidNickname
	}
	if !isLetterOrDigit(nickname[0]) || !isLetterOrDigit(nickname[len(nickname)-1]) {
		return ErrInvalidNickname
	}
	for i := 0; i < len(nickname); i++ {
		c := nickname[i]
		if !isLetterOrDigit(c) && c != '_' && c != '-' {
			return ErrInvalidNickname
		}
	}
	return nil
}

func isLetterOrDigit(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
