package hub

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameLength is the longest allowed username, in characters.
const MaxNameLength = 16

var (
	ErrInvalidName   = errors.New("Usernames are 1–16 characters: letters, digits, spaces and _ - .")
	ErrOffensiveName = errors.New("That username isn't allowed. Please pick another one.")
)

// NormalizeName trims the name, collapses runs of whitespace into single
// spaces and checks it against the allowed character set and the bad-word list.
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
	if isOffensive(name) {
		return "", ErrOffensiveName
	}
	return name, nil
}
