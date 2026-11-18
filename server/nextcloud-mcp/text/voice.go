package text

import (
	"fmt"
	"strings"
)

// List joins items with "; ", showing at most max and appending " y N más".
func List(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:max], "; ") + fmt.Sprintf(" y %d más", len(items)-max)
}

// Clip cuts s to at most max bytes at the last newline or space and appends a
// truncation note.
func Clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexAny(cut, "\n "); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n(recortado; pide una parte más específica)"
}
