// Package text holds Spanish text helpers: normalization, title matching,
// voice formatting, dates and priority. It imports only the stdlib.
package text

import (
	"strings"
	"unicode"
)

var accentFold = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c",
)

// Normalize lowercases, strips accents and keeps only letters, digits and
// single spaces.
func Normalize(s string) string {
	s = accentFold.Replace(strings.ToLower(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		} else {
			space = true
		}
	}
	return b.String()
}

// Slug is Normalize with spaces turned into dashes.
func Slug(s string) string {
	return strings.ReplaceAll(Normalize(s), " ", "-")
}
