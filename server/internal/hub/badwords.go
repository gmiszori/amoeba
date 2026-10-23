package hub

import (
	_ "embed"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed badwords.txt
var badwordsFile string

type wholeWord struct {
	collapsed string
	length    int // in letters before collapsing, so "ass" doesn't block "As"
}

var (
	// anywhereWords may not appear anywhere in a name; wholeWords may not be a
	// word of it. Both hold normalised, collapsed forms.
	anywhereWords []string
	wholeWords    []wholeWord
)

func init() {
	for line := range strings.Lines(badwordsFile) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if w, ok := strings.CutPrefix(line, "="); ok {
			w = normalize(w, 'i')
			wholeWords = append(wholeWords, wholeWord{collapse(w), utf8.RuneCountInString(w)})
		} else {
			anywhereWords = append(anywhereWords, collapse(normalize(line, 'i')))
		}
	}
}

// isOffensive reports whether a (valid, normalised) username contains a
// blocked word. It's a best-effort filter: it catches the usual tricks, but a
// determined person will always find a spelling that gets through.
func isOffensive(name string) bool {
	// "1" stands in for both i and l (sh1t, s1ut), so try both readings.
	for _, one := range []rune{'i', 'l'} {
		words := strings.FieldsFunc(normalize(name, one), func(r rune) bool { return !unicode.IsLetter(r) })
		squashed := collapse(strings.Join(words, ""))
		for _, bad := range anywhereWords {
			if strings.Contains(squashed, bad) {
				return true
			}
		}
		for _, w := range words {
			cw, n := collapse(w), utf8.RuneCountInString(w)
			for _, bad := range wholeWords {
				if cw == bad.collapsed && n >= bad.length {
					return true
				}
			}
		}
	}
	return false
}

var foldAccents = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'å': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'ő': 'o', 'õ': 'o', 'ø': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ű': 'u',
	'ç': 'c', 'ñ': 'n', 'ý': 'y', 'ÿ': 'y', 'ß': 's',
}

var leet = map[rune]rune{'0': 'o', '3': 'e', '4': 'a', '5': 's', '7': 't', '8': 'b', '9': 'g'}

// normalize lowercases s, strips accents and undoes leetspeak, reading "1"
// as one.
func normalize(s string, one rune) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) {
			continue // combining accent written separately
		}
		if f, ok := foldAccents[r]; ok {
			r = f
		} else if f, ok := leet[r]; ok {
			r = f
		} else if r == '1' {
			r = one
		}
		b.WriteRune(r)
	}
	return b.String()
}

// collapse squeezes runs of the same letter into one: "fuuuck" → "fuck".
func collapse(s string) string {
	var b strings.Builder
	var prev rune = -1
	for _, r := range s {
		if r != prev {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}
