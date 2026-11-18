package ical

import (
	"strings"
	"testing"
	"time"
)

func santiago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skip("tzdata no disponible:", err)
	}
	return loc
}

func TestFieldUnfoldsAndParams(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\n" +
		"SUMMARY:Reunion con el\r\n equipo de\r\n\t ventas\\, hoy\\nok\r\n" +
		"DTSTART;TZID=America/Santiago:20261121T150000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	v, _, ok := Field(ics, "SUMMARY")
	if !ok || v != "Reunion con elequipo de ventas, hoy\nok" {
		t.Fatalf("summary = %q ok=%v", v, ok)
	}
	v, p, ok := Field(ics, "DTSTART")
	if !ok || v != "20261121T150000" || p["TZID"] != "America/Santiago" {
		t.Fatalf("dtstart = %q %v %v", v, p, ok)
	}
	if _, _, ok := Field(ics, "NOPE"); ok {
		t.Fatal("NOPE should be absent")
	}
}

func TestSetVTodoFieldReplaceAndAppend(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VTODO\r\nUID:1\r\nSTATUS:NEEDS-ACTION\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	out := SetVTodoField(ics, "STATUS", "COMPLETED")
	if strings.Contains(out, "NEEDS-ACTION") || !strings.Contains(out, "STATUS:COMPLETED\r\n") {
		t.Fatalf("replace failed: %q", out)
	}
	out = SetVTodoField(out, "DESCRIPTION", "a, b; c\nd")
	if !strings.Contains(out, "DESCRIPTION:a\\, b\\; c\\nd\r\nEND:VTODO") {
		t.Fatalf("append failed: %q", out)
	}
	long := strings.Repeat("ñ", 100)
	out = SetVTodoField(out, "SUMMARY", long)
	for _, l := range strings.Split(out, "\r\n") {
		if len(l) > 75 {
			t.Fatalf("line over 75 octets (%d): %q", len(l), l)
		}
	}
	if v, _, _ := Field(out, "SUMMARY"); v != long {
		t.Fatalf("summary roundtrip = %q", v)
	}
	// replacing a folded property removes its continuation lines
	out = SetVTodoField(out, "SUMMARY", "corto")
	if strings.Contains(out, "ñ") {
		t.Fatalf("continuation left behind: %q", out)
	}
}

func TestMakeVTodoRoundTrip(t *testing.T) {
	loc := santiago(t)
	due := time.Date(2026, 11, 25, 0, 0, 0, 0, loc)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	in := Todo{UID: "1@xiaozhi-nextcloud", Summary: "Comprar, alambre", Description: "x;y", ParentUID: "0@xiaozhi-nextcloud",
		Due: &due, DueIsDate: true, Priority: 1}
	ics := MakeVTodo(in, now)
	for _, want := range []string{"DTSTAMP:20261118T120000Z", "CREATED:20261118T120000Z",
		"RELATED-TO;RELTYPE=PARENT:0@xiaozhi-nextcloud", "DUE;VALUE=DATE:20261125"} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q in %q", want, ics)
		}
	}
	out := ParseVTodo(ics, loc)
	if out.UID != in.UID || out.Summary != in.Summary || out.Description != in.Description ||
		out.ParentUID != in.ParentUID || out.Priority != 1 || !out.DueIsDate ||
		out.Due == nil || !out.Due.Equal(due) || out.Status != "NEEDS-ACTION" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}

func TestMakeVEventTZIDAndVTimezone(t *testing.T) {
	loc := santiago(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	e := Event{UID: "2@xiaozhi-nextcloud", Summary: "Dentista", Location: "Av. Siempre Viva",
		Start: time.Date(2026, 11, 21, 15, 0, 0, 0, loc), End: time.Date(2026, 11, 21, 16, 0, 0, 0, loc),
		RRule: "FREQ=WEEKLY;BYDAY=SA"}
	ics := MakeVEvent(e, loc, now)
	for _, want := range []string{"BEGIN:VTIMEZONE", "TZID:America/Santiago",
		"DTSTART;TZID=America/Santiago:20261121T150000", "RRULE:FREQ=WEEKLY;BYDAY=SA", "BYMONTH=4;BYDAY=1SU", "BYMONTH=9;BYDAY=1SU"} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q in %q", want, ics)
		}
	}
	evs := ParseVEvents(ics, loc)
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	g := evs[0]
	if !g.Start.Equal(e.Start) || !g.End.Equal(e.End) || g.Start.Hour() != 15 || g.Summary != "Dentista" ||
		g.Location != e.Location || g.RRule != e.RRule || g.AllDay {
		t.Fatalf("roundtrip mismatch: %+v", g)
	}
}

func TestMakeVEventUnknownZoneUsesUTC(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	e := Event{UID: "3", Summary: "x", Start: time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 21, 16, 0, 0, 0, time.UTC)}
	ics := MakeVEvent(e, time.UTC, now)
	if strings.Contains(ics, "VTIMEZONE") || strings.Contains(ics, "TZID") || !strings.Contains(ics, "DTSTART:20261121T150000Z") {
		t.Fatalf("unexpected: %q", ics)
	}
	if _, ok := VTimezone(time.UTC); ok {
		t.Fatal("UTC should have no VTIMEZONE")
	}
}

func TestAllDayEndNextDay(t *testing.T) {
	loc := santiago(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	e := Event{UID: "4", Summary: "Feriado", AllDay: true, Start: time.Date(2026, 12, 8, 0, 0, 0, 0, loc)}
	ics := MakeVEvent(e, loc, now)
	if !strings.Contains(ics, "DTSTART;VALUE=DATE:20261208") || !strings.Contains(ics, "DTEND;VALUE=DATE:20261209") {
		t.Fatalf("unexpected: %q", ics)
	}
	evs := ParseVEvents(ics, loc)
	if len(evs) != 1 || !evs[0].AllDay || evs[0].Start.Day() != 8 || evs[0].End.Day() != 9 {
		t.Fatalf("roundtrip: %+v", evs)
	}
}

func TestParseVEventsFloatingUTCTZID(t *testing.T) {
	loc := santiago(t)
	ics := "BEGIN:VCALENDAR\r\n" +
		"BEGIN:VEVENT\r\nUID:a\r\nSUMMARY:floating\r\nDTSTART:20261121T150000\r\nDTEND:20261121T160000\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:b\r\nSUMMARY:utc\r\nDTSTART:20261121T180000Z\r\nDTEND:20261121T190000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:c\r\nSUMMARY:tzid\r\nDTSTART;TZID=America/Santiago:20261121T150000\r\nDTEND;TZID=Nowhere/Land:20261121T160000\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	evs := ParseVEvents(ics, loc)
	if len(evs) != 3 {
		t.Fatalf("got %d", len(evs))
	}
	want := time.Date(2026, 11, 21, 15, 0, 0, 0, loc)
	for i, e := range evs[:3] {
		if i == 1 {
			if !e.Start.Equal(time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)) {
				t.Errorf("utc start = %v", e.Start)
			}
			continue
		}
		if !e.Start.Equal(want) {
			t.Errorf("event %d start = %v want %v", i, e.Start, want)
		}
	}
	// unknown TZID falls back to loc
	if !evs[2].End.Equal(time.Date(2026, 11, 21, 16, 0, 0, 0, loc)) {
		t.Errorf("fallback end = %v", evs[2].End)
	}
}

func TestParseExpandedRecurrence(t *testing.T) {
	loc := santiago(t)
	ics := "BEGIN:VCALENDAR\r\n" +
		"BEGIN:VEVENT\r\nUID:r\r\nRECURRENCE-ID:20261121T150000Z\r\nSUMMARY:rep\r\nDTSTART:20261121T150000Z\r\nDTEND:20261121T160000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:r\r\nRECURRENCE-ID:20261128T150000Z\r\nSUMMARY:rep\r\nDTSTART:20261128T150000Z\r\nDTEND:20261128T160000Z\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	evs := ParseVEvents(ics, loc)
	if len(evs) != 2 || evs[1].Start.Day() != 28 {
		t.Fatalf("got %+v", evs)
	}
}
