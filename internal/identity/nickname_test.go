package identity

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateNickname(t *testing.T) {
	tests := []struct {
		name     string
		nickname string
		valid    bool
	}{
		{"minimum length", "abc", true},
		{"maximum length", strings.Repeat("a", 24), true},
		{"case preserved", "NightOwl", true},
		{"digits", "123", true},
		{"allowed punctuation", "night_01-owl", true},
		{"repeated punctuation", "a__--1", true},
		{"empty", "", false},
		{"too short", "ab", false},
		{"too long", strings.Repeat("a", 25), false},
		{"leading underscore", "_ivan", false},
		{"trailing hyphen", "ivan-", false},
		{"leading space", " ivan", false},
		{"trailing space", "ivan ", false},
		{"space inside", "night owl", false},
		{"dot", "ivan.one", false},
		{"cyrillic", "иван", false},
		{"unicode lookalike", "ivаn", false},
		{"newline", "iv\nan", false},
		{"terminal control", "a\x1b[31mb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNickname(tt.nickname)
			if tt.valid && err != nil {
				t.Fatalf("valid nickname rejected: %v", err)
			}
			if !tt.valid && !errors.Is(err, ErrInvalidNickname) {
				t.Fatalf("got %v, want ErrInvalidNickname", err)
			}
		})
	}
}
