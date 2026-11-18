package text

import (
	"fmt"
	"strings"
	"time"
)

var dayAbbr = [...]string{"dom", "lun", "mar", "mie", "jue", "vie", "sab"}

var monthNames = [...]string{"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// MonthName returns the Spanish month name.
func MonthName(m time.Month) string {
	if m < 1 || m > 12 {
		return ""
	}
	return monthNames[m-1]
}

// Human formats t relative to now, comparing calendar dates in now's location.
func Human(t, now time.Time, withTime bool) string {
	loc := now.Location()
	t = t.In(loc)
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	days := int(time.Date(ty, tm, td, 12, 0, 0, 0, time.UTC).Sub(time.Date(ny, nm, nd, 12, 0, 0, 0, time.UTC)).Hours() / 24)
	var s string
	switch days {
	case 0:
		s = "hoy"
	case 1:
		s = "mañana"
	case -1:
		s = "ayer"
	default:
		s = fmt.Sprintf("%s %02d/%02d", dayAbbr[t.Weekday()], td, int(tm))
	}
	if withTime {
		s += t.Format(" 15:04")
	}
	return s
}

// ParseWhen parses ISO local date or datetime (seconds optional) in loc, or
// RFC3339 with offset converted to loc.
func ParseWhen(s string, loc *time.Location) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, true, nil
	}
	for _, l := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, false, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc), false, nil
	}
	return time.Time{}, false, fmt.Errorf("Fecha no válida: %s (usa AAAA-MM-DD o AAAA-MM-DDTHH:MM)", s)
}

// ParsePriority maps alta/media/baja or 0-9 to an iCal priority; "" is 5.
func ParsePriority(s string) (int, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "":
		return 5, nil
	case "alta":
		return 1, nil
	case "media":
		return 5, nil
	case "baja":
		return 9, nil
	default:
		if len(v) == 1 && v[0] >= '0' && v[0] <= '9' {
			return int(v[0] - '0'), nil
		}
	}
	return 0, fmt.Errorf("Prioridad no válida: %s (usa alta, media, baja o 0-9)", s)
}

// PriorityWord names an iCal priority.
func PriorityWord(p int) string {
	switch {
	case p >= 1 && p <= 4:
		return "alta"
	case p == 5:
		return "media"
	case p >= 6 && p <= 9:
		return "baja"
	}
	return ""
}
