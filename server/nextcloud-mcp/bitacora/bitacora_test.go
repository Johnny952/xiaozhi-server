package bitacora

import (
	"reflect"
	"testing"
	"time"
)

var day = time.Date(2026, time.November, 18, 0, 0, 0, 0, time.UTC)

func TestEntryAndHeaderExact(t *testing.T) {
	if got, want := Path("Casa Nueva", day), "/Bitacora/casa-nueva/2026/2026-11-18.md"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got, want := Header("Casa Nueva", day), "# Bitácora Casa Nueva — 18 de noviembre de 2026\n\n"; got != want {
		t.Errorf("Header = %q, want %q", got, want)
	}
	if got, want := EntryText("10:30", "Compré cemento", "Dos bolsas."), "## 10:30 — Compré cemento\n\nDos bolsas.\n\n---\n"; got != want {
		t.Errorf("EntryText = %q, want %q", got, want)
	}
	orig := Entry{Time: "10:30", Title: "Compré cemento"}
	want := "## 11:00 — Corrección: Compré cemento\n\n> Corrige: 10:30 — Compré cemento\n\nTres bolsas.\n\n---\n"
	if got := CorrectionText("11:00", orig, "Tres bolsas."); got != want {
		t.Errorf("CorrectionText = %q, want %q", got, want)
	}
}

func TestParseRoundTrip(t *testing.T) {
	c := Header("Casa", day) + EntryText("09:00", "Uno", "Texto uno.") + EntryText("10:00", "Dos", "Texto dos.")
	d := Parse(c)
	if !d.Structured || d.Context != "Casa" || !d.Date.Equal(day) || len(d.Entries) != 2 {
		t.Fatalf("unexpected %+v", d)
	}
	if d.Entries[1].Time != "10:00" || d.Entries[1].Title != "Dos" || d.Entries[1].Text != "Texto dos." {
		t.Errorf("entry = %+v", d.Entries[1])
	}
}

func TestParseAppliesLastCorrection(t *testing.T) {
	orig := Entry{Time: "10:30", Title: "Cemento"}
	c := Header("Casa", day) + EntryText("10:30", "Cemento", "Una bolsa.") +
		CorrectionText("11:00", orig, "Dos bolsas.") + CorrectionText("12:15", orig, "Tres bolsas.")
	d := Parse(c)
	if len(d.Entries) != 1 {
		t.Fatalf("entries = %+v", d.Entries)
	}
	e := d.Entries[0]
	if e.Text != "Tres bolsas." || e.CorrectedAt != "12:15" {
		t.Errorf("entry = %+v", e)
	}
}

func TestParseOrphanCorrectionKept(t *testing.T) {
	orig := Entry{Time: "08:00", Title: "Fantasma"}
	c := Header("Casa", day) + EntryText("10:30", "Cemento", "Una bolsa.") + CorrectionText("11:00", orig, "Texto.")
	d := Parse(c)
	if len(d.Entries) != 2 {
		t.Fatalf("entries = %+v", d.Entries)
	}
	if d.Entries[1].Time != "11:00" || d.Entries[1].Title != "Corrección: Fantasma" {
		t.Errorf("orphan = %+v", d.Entries[1])
	}
}

func TestParseHandEditedDashes(t *testing.T) {
	c := "# Bitácora Casa — 18 de noviembre de 2026\n\n## 09:00 - Con guion\n\nA.\n\n---\n\n## 10:00 – Con en dash\n\nB.\n"
	d := Parse(c)
	if len(d.Entries) != 2 || d.Entries[0].Title != "Con guion" || d.Entries[1].Title != "Con en dash" || d.Entries[1].Text != "B." {
		t.Errorf("entries = %+v", d.Entries)
	}
}

func TestParseUnstructured(t *testing.T) {
	c := "Nota libre sin encabezados.\nOtra línea."
	d := Parse(c)
	if d.Structured || d.Raw != c || len(d.Entries) != 0 {
		t.Errorf("day = %+v", d)
	}
}

func TestFindEntryByTimeAndTitle(t *testing.T) {
	d := Day{Entries: []Entry{
		{Time: "09:00", Title: "Comprar alambre"},
		{Time: "10:30", Title: "Cemento"},
		{Time: "10:30", Title: "Pintura"},
	}}
	if got := FindEntry(d, "09:00"); !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("by time = %v", got)
	}
	if got := FindEntry(d, "10:30"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("ambiguous = %v", got)
	}
	if got := FindEntry(d, "alambre"); !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("by title = %v", got)
	}
	if got := FindEntry(d, "  "); got != nil {
		t.Errorf("blank = %v", got)
	}
}
