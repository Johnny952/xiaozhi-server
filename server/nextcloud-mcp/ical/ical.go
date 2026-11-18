// Package ical builds and parses the small subset of iCalendar (VTODO and
// VEVENT) that the Nextcloud tools need. It is pure: no I/O, stdlib only.
package ical

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	utcLayout   = "20060102T150405Z"
	dateLayout  = "20060102"
	localLayout = "20060102T150405"
	prodID      = "-//xiaozhi-nextcloud//EN"
)

// ToUTC formats t as an iCal UTC timestamp.
func ToUTC(t time.Time) string { return t.UTC().Format(utcLayout) }

// ToDate formats the calendar date of t (in t's own zone).
func ToDate(t time.Time) string { return t.Format(dateLayout) }

// ---- low-level text handling ----

func unfold(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n ", "")
	s = strings.ReplaceAll(s, "\n\t", "")
	return strings.Split(s, "\n")
}

func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\\n")
}

// fold wraps a content line at 75 octets, never splitting a UTF-8 rune.
func fold(line string) string {
	if len(line) <= 75 {
		return line
	}
	var b strings.Builder
	limit := 75
	n := 0
	for _, r := range line {
		sz := utf8.RuneLen(r)
		if n+sz > limit {
			b.WriteString("\r\n ")
			n, limit = 1, 75 // the leading space counts
		}
		b.WriteRune(r)
		n += sz
	}
	return b.String()
}

// splitLine splits "NAME;P=V:value" into name, params and value.
func splitLine(line string) (name string, params map[string]string, value string, ok bool) {
	colon := indexOutsideQuotes(line, ':')
	if colon < 0 {
		return "", nil, "", false
	}
	head, value := line[:colon], line[colon+1:]
	parts := splitOutsideQuotes(head, ';')
	name = strings.ToUpper(parts[0])
	params = map[string]string{}
	for _, p := range parts[1:] {
		if k, v, found := strings.Cut(p, "="); found {
			params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return name, params, value, true
}

func indexOutsideQuotes(s string, c byte) int {
	in := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			in = !in
		case s[i] == c && !in:
			return i
		}
	}
	return -1
}

func splitOutsideQuotes(s string, c byte) []string {
	var out []string
	for {
		i := indexOutsideQuotes(s, c)
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i])
		s = s[i+1:]
	}
}

// Field returns the first occurrence of property name (VTIMEZONE contents are
// ignored), unfolded and unescaped, with its parameters (keys upper-cased).
func Field(ics, name string) (string, map[string]string, bool) {
	name = strings.ToUpper(name)
	inTZ := false
	for _, line := range unfold(ics) {
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "BEGIN:VTIMEZONE":
			inTZ = true
			continue
		case "END:VTIMEZONE":
			inTZ = false
			continue
		}
		if inTZ {
			continue
		}
		n, params, v, ok := splitLine(line)
		if ok && n == name {
			return unescape(strings.TrimSpace(v)), params, true
		}
	}
	return "", nil, false
}

// SetVTodoField replaces property name inside the VTODO (dropping its folded
// continuation lines) or appends it before END:VTODO. value is escaped.
func SetVTodoField(ics, name, value string) string {
	newLine := fold(strings.ToUpper(name) + ":" + escape(value))
	raw := strings.ReplaceAll(ics, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	var out []string
	inTodo, done, skipping := false, false, false
	for _, l := range lines {
		if skipping {
			if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
				continue
			}
			skipping = false
		}
		up := strings.ToUpper(strings.TrimSpace(l))
		switch {
		case up == "BEGIN:VTODO":
			inTodo = true
		case up == "END:VTODO":
			if inTodo && !done {
				out = append(out, newLine)
				done = true
			}
			inTodo = false
		case inTodo && !done:
			if n, _, _, ok := splitLine(l); ok && n == strings.ToUpper(name) {
				out = append(out, newLine)
				done, skipping = true, true
				continue
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\r\n")
}

// ---- time parsing ----

// parseTime reads a DATE, UTC, TZID or floating value. TZID that cannot be
// loaded falls back to loc. The bool reports whether the value was a DATE.
func parseTime(value string, params map[string]string, loc *time.Location) (time.Time, bool, bool) {
	value = strings.TrimSpace(value)
	if params["VALUE"] == "DATE" || len(value) == 8 {
		t, err := time.ParseInLocation(dateLayout, value, loc)
		return t, true, err == nil
	}
	if strings.HasSuffix(value, "Z") {
		t, err := time.Parse(utcLayout, value)
		return t.In(loc), false, err == nil
	}
	zone := loc
	if tz := params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			zone = l
		}
	}
	t, err := time.ParseInLocation(localLayout, value, zone)
	return t.In(loc), false, err == nil
}

func propLine(name, value string) string { return fold(name + ":" + escape(value)) }

// ---- VTODO ----

type Todo struct {
	UID, Summary, Description, Status, ParentUID string
	Due                                          *time.Time
	DueIsDate                                    bool
	Priority                                     int
	Completed                                    *time.Time
}

// MakeVTodo renders a complete VCALENDAR holding one VTODO.
func MakeVTodo(t Todo, now time.Time) string {
	status := t.Status
	if status == "" {
		status = "NEEDS-ACTION"
	}
	l := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:" + prodID, "BEGIN:VTODO",
		"UID:" + t.UID, "DTSTAMP:" + ToUTC(now), "CREATED:" + ToUTC(now),
		propLine("SUMMARY", t.Summary),
	}
	if t.Description != "" {
		l = append(l, propLine("DESCRIPTION", t.Description))
	}
	if t.Due != nil {
		if t.DueIsDate {
			l = append(l, "DUE;VALUE=DATE:"+ToDate(*t.Due))
		} else {
			l = append(l, "DUE:"+ToUTC(*t.Due))
		}
	}
	if t.Priority > 0 {
		l = append(l, "PRIORITY:"+strconv.Itoa(t.Priority))
	}
	if t.ParentUID != "" {
		l = append(l, "RELATED-TO;RELTYPE=PARENT:"+t.ParentUID)
	}
	l = append(l, "STATUS:"+status)
	if t.Completed != nil {
		l = append(l, "COMPLETED:"+ToUTC(*t.Completed))
	}
	l = append(l, "END:VTODO", "END:VCALENDAR")
	return strings.Join(l, "\r\n") + "\r\n"
}

// ParseVTodo reads the first VTODO of ics. Times are returned in loc.
func ParseVTodo(ics string, loc *time.Location) Todo {
	var t Todo
	t.UID, _, _ = Field(ics, "UID")
	t.Summary, _, _ = Field(ics, "SUMMARY")
	t.Description, _, _ = Field(ics, "DESCRIPTION")
	t.Status, _, _ = Field(ics, "STATUS")
	t.ParentUID, _, _ = Field(ics, "RELATED-TO")
	if p, _, ok := Field(ics, "PRIORITY"); ok {
		t.Priority, _ = strconv.Atoi(p)
	}
	if v, params, ok := Field(ics, "DUE"); ok {
		if d, isDate, good := parseTime(v, params, loc); good {
			t.Due, t.DueIsDate = &d, isDate
		}
	}
	if v, params, ok := Field(ics, "COMPLETED"); ok {
		if d, _, good := parseTime(v, params, loc); good {
			t.Completed = &d
		}
	}
	return t
}

// ---- VEVENT ----

type Event struct {
	UID, Summary, Location, Description, RRule string
	Start, End                                 time.Time
	AllDay                                     bool
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// MakeVEvent renders a VCALENDAR with one VEVENT. With a known VTIMEZONE the
// times carry TZID; otherwise they are written in UTC. All-day events use
// VALUE=DATE and DTEND is the (exclusive) day after the last day.
func MakeVEvent(e Event, loc *time.Location, now time.Time) string {
	tz, hasTZ := VTimezone(loc)
	l := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:" + prodID}
	if hasTZ {
		l = append(l, tz)
	}
	l = append(l, "BEGIN:VEVENT", "UID:"+e.UID, "DTSTAMP:"+ToUTC(now), propLine("SUMMARY", e.Summary))
	switch {
	case e.AllDay:
		start := dateOnly(e.Start)
		end := dateOnly(e.End)
		if next := start.AddDate(0, 0, 1); end.Before(next) {
			end = next
		}
		l = append(l, "DTSTART;VALUE=DATE:"+ToDate(start), "DTEND;VALUE=DATE:"+ToDate(end))
	case hasTZ:
		l = append(l,
			"DTSTART;TZID="+loc.String()+":"+e.Start.In(loc).Format(localLayout),
			"DTEND;TZID="+loc.String()+":"+e.End.In(loc).Format(localLayout))
	default:
		l = append(l, "DTSTART:"+ToUTC(e.Start), "DTEND:"+ToUTC(e.End))
	}
	if e.Location != "" {
		l = append(l, propLine("LOCATION", e.Location))
	}
	if e.Description != "" {
		l = append(l, propLine("DESCRIPTION", e.Description))
	}
	if e.RRule != "" {
		l = append(l, fold("RRULE:"+e.RRule))
	}
	l = append(l, "END:VEVENT", "END:VCALENDAR")
	return strings.Join(l, "\r\n") + "\r\n"
}

// ParseVEvents returns every VEVENT in ics (an expanded recurrence carries
// several). Events without a DTSTART are skipped.
func ParseVEvents(ics string, loc *time.Location) []Event {
	var events []Event
	var block []string
	in := false
	for _, line := range unfold(ics) {
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "BEGIN:VEVENT":
			in, block = true, nil
			continue
		case "END:VEVENT":
			if in {
				if e, ok := parseVEvent(strings.Join(block, "\r\n"), loc); ok {
					events = append(events, e)
				}
			}
			in = false
			continue
		}
		if in {
			block = append(block, line)
		}
	}
	return events
}

func parseVEvent(body string, loc *time.Location) (Event, bool) {
	var e Event
	v, params, ok := Field(body, "DTSTART")
	if !ok {
		return e, false
	}
	start, allDay, good := parseTime(v, params, loc)
	if !good {
		return e, false
	}
	e.Start, e.AllDay = start, allDay
	if v, params, ok := Field(body, "DTEND"); ok {
		if end, _, good := parseTime(v, params, loc); good {
			e.End = end
		}
	}
	if e.End.IsZero() {
		e.End = e.Start
		if allDay {
			e.End = e.Start.AddDate(0, 0, 1)
		}
	}
	e.UID, _, _ = Field(body, "UID")
	e.Summary, _, _ = Field(body, "SUMMARY")
	e.Location, _, _ = Field(body, "LOCATION")
	e.Description, _, _ = Field(body, "DESCRIPTION")
	e.RRule, _, _ = rawField(body, "RRULE")
	return e, true
}

// rawField is Field without unescaping: RRULE is structured, not text.
func rawField(ics, name string) (string, map[string]string, bool) {
	for _, line := range unfold(ics) {
		if n, p, v, ok := splitLine(line); ok && n == name {
			return strings.TrimSpace(v), p, true
		}
	}
	return "", nil, false
}
