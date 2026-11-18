package ical

import "time"

// VTimezone returns the fixed VTIMEZONE block for America/Santiago. Any other
// zone has no block (false); callers then write times in UTC.
func VTimezone(loc *time.Location) (string, bool) {
	if loc == nil || loc.String() != "America/Santiago" {
		return "", false
	}
	lines := []string{
		"BEGIN:VTIMEZONE",
		"TZID:America/Santiago",
		"BEGIN:STANDARD",
		"DTSTART:19700405T000000",
		"TZOFFSETFROM:-0300",
		"TZOFFSETTO:-0400",
		"TZNAME:-04",
		"RRULE:FREQ=YEARLY;BYMONTH=4;BYDAY=1SU",
		"END:STANDARD",
		"BEGIN:DAYLIGHT",
		"DTSTART:19700906T000000",
		"TZOFFSETFROM:-0400",
		"TZOFFSETTO:-0300",
		"TZNAME:-03",
		"RRULE:FREQ=YEARLY;BYMONTH=9;BYDAY=1SU",
		"END:DAYLIGHT",
		"END:VTIMEZONE",
	}
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\r\n"
		}
		out += l
	}
	return out, true
}
