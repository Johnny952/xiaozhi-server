// Package bitacora defines the daily-log markdown format and a tolerant parser.
package bitacora

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"nextcloud-mcp/text"
)

// Entry is one log entry; Text already reflects the last correction, if any.
type Entry struct {
	Time        string
	Title       string
	Text        string
	CorrectedAt string
}

// Day is a parsed daily file. If Structured is false, only Raw is meaningful.
type Day struct {
	Date       time.Time
	Context    string
	Entries    []Entry
	Raw        string
	Structured bool
}

const correctionPrefix = "Corrección:"

var (
	entryRe   = regexp.MustCompile(`^##\s+(\d{1,2}:\d{2})\s+[—–-]\s*(.*?)\s*$`)
	headerRe  = regexp.MustCompile(`^#\s+Bitácora\s+(.*?)\s+[—–-]\s+(\d{1,2})\s+de\s+(\S+)\s+de\s+(\d{4})\s*$`)
	corrigeRe = regexp.MustCompile(`^>\s*Corrige:\s*(\d{1,2}:\d{2})\s*(?:[—–-]\s*(.*?))?\s*$`)
	timeRe    = regexp.MustCompile(`^\d{1,2}:\d{2}$`)
)

// Path returns the file path for a context and day.
func Path(ctx string, day time.Time) string {
	return fmt.Sprintf("/Bitacora/%s/%d/%s.md", text.Slug(ctx), day.Year(), day.Format("2006-01-02"))
}

// Header returns the file header; ctx is used as spoken, not slugged.
func Header(ctx string, day time.Time) string {
	return fmt.Sprintf("# Bitácora %s — %02d de %s de %d\n\n", ctx, day.Day(), text.MonthName(day.Month()), day.Year())
}

// EntryText returns the block to append for a new entry.
func EntryText(hhmm, titulo, texto string) string {
	return fmt.Sprintf("## %s — %s\n\n%s\n\n---\n", hhmm, titulo, texto)
}

// CorrectionText returns the block to append correcting orig.
func CorrectionText(hhmm string, orig Entry, texto string) string {
	return fmt.Sprintf("## %s — %s %s\n\n> Corrige: %s — %s\n\n%s\n\n---\n",
		hhmm, correctionPrefix, orig.Title, orig.Time, orig.Title, texto)
}

// Parse reads a daily file tolerantly (hand-edited dashes, extra blank lines).
func Parse(content string) Day {
	d := Day{Raw: content}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	type block struct {
		time, title string
		body        []string
	}
	var blocks []*block
	var cur *block
	for _, ln := range lines {
		if m := entryRe.FindStringSubmatch(ln); m != nil {
			cur = &block{time: m[1], title: m[2]}
			blocks = append(blocks, cur)
			continue
		}
		if cur != nil {
			cur.body = append(cur.body, ln)
		} else if m := headerRe.FindStringSubmatch(ln); m != nil {
			d.Context = m[1]
			for mo := time.January; mo <= time.December; mo++ {
				if text.MonthName(mo) == strings.ToLower(m[3]) {
					var day int
					fmt.Sscanf(m[2], "%d", &day)
					var year int
					fmt.Sscanf(m[4], "%d", &year)
					d.Date = time.Date(year, mo, day, 0, 0, 0, 0, time.UTC)
				}
			}
		}
	}
	if len(blocks) == 0 {
		return d
	}
	d.Structured = true

	for _, b := range blocks {
		body := b.body
		// Drop trailing blank lines and the closing "---" separator.
		trim := func() {
			for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
				body = body[:len(body)-1]
			}
		}
		trim()
		if len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "---" {
			body = body[:len(body)-1]
		}
		trim()

		if rest, ok := strings.CutPrefix(b.title, correctionPrefix); ok {
			var origTime, origTitle string
			var kept []string
			found := false
			for _, ln := range body {
				if m := corrigeRe.FindStringSubmatch(ln); m != nil && !found {
					found, origTime, origTitle = true, m[1], m[2]
					continue
				}
				kept = append(kept, ln)
			}
			if found {
				if origTitle == "" {
					origTitle = strings.TrimSpace(rest)
				}
				if i := matchOriginal(d.Entries, origTime, origTitle); i >= 0 {
					d.Entries[i].Text = strings.TrimSpace(strings.Join(kept, "\n"))
					d.Entries[i].CorrectedAt = b.time
					continue
				}
			}
		}
		d.Entries = append(d.Entries, Entry{
			Time: b.time, Title: b.title,
			Text: strings.TrimSpace(strings.Join(body, "\n")),
		})
	}
	return d
}

// matchOriginal finds the original entry by time+title, else by time alone
// when that is unambiguous. Corrections never target other corrections.
func matchOriginal(entries []Entry, tm, title string) int {
	for i, e := range entries {
		if e.Time == tm && text.Normalize(e.Title) == text.Normalize(title) {
			return i
		}
	}
	idx := -1
	for i, e := range entries {
		if e.Time == tm {
			if idx >= 0 {
				return -1
			}
			idx = i
		}
	}
	return idx
}

// FindEntry returns candidate indices for ref: by time if ref is HH:MM,
// otherwise by title. A blank ref matches nothing.
func FindEntry(d Day, ref string) []int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if timeRe.MatchString(ref) {
		var out []int
		for i, e := range d.Entries {
			if e.Time == ref || (len(ref) == 4 && e.Time == "0"+ref) {
				out = append(out, i)
			}
		}
		return out
	}
	titles := make([]string, len(d.Entries))
	for i, e := range d.Entries {
		titles[i] = e.Title
	}
	return text.Find(titles, ref)
}
