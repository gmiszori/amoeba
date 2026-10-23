package hub

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameLength is the longest allowed username, in characters.
const MaxNameLength = 32

var ErrInvalidName = errors.New("usernames are 1–32 characters: letters, digits, spaces and _ - .")

// NormalizeName trims the name, collapses runs of whitespace into single
// spaces and checks it against the allowed character set.
func NormalizeName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if n := utf8.RuneCountInString(name); n == 0 || n > MaxNameLength {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" _-.", r) {
			return "", ErrInvalidName
		}
	}
	return name, nil
}
