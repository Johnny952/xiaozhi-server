package text

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	if g := Normalize("Joyería  ÑANDÚ"); g != "joyeria nandu" {
		t.Fatalf("got %q", g)
	}
	if g := Normalize("  Hola, mundo! 2x "); g != "hola mundo 2x" {
		t.Fatalf("got %q", g)
	}
}

func TestSlug(t *testing.T) {
	if g := Slug("Mesa Libre"); g != "mesa-libre" {
		t.Fatalf("got %q", g)
	}
}

func TestFindExactBeatsContains(t *testing.T) {
	g := Find([]string{"Comprar alambre", "Comprar alambre de cobre"}, "comprar alambre")
	if !reflect.DeepEqual(g, []int{0}) {
		t.Fatalf("got %v", g)
	}
}

func TestFindAmbiguous(t *testing.T) {
	g := Find([]string{"Comprar alambre", "Alambre de cobre", "Pan"}, "alambre")
	if !reflect.DeepEqual(g, []int{0, 1}) {
		t.Fatalf("got %v", g)
	}
	if Find([]string{"Pan"}, "queso") != nil {
		t.Fatal("want nil")
	}
}

func TestCandidatesMax5(t *testing.T) {
	var ts []string
	for i := 0; i < 8; i++ {
		ts = append(ts, fmt.Sprintf("alambre %d", i))
	}
	ts = append(ts, "nada")
	g := Candidates(ts, "alambre", 5)
	if len(g) != 5 {
		t.Fatalf("got %v", g)
	}
	g = Candidates([]string{"b alambre cobre", "a alambre", "c alambre cobre"}, "alambre cobre", 5)
	if !reflect.DeepEqual(g, []string{"b alambre cobre", "c alambre cobre", "a alambre"}) {
		t.Fatalf("got %v", g)
	}
}

func TestListYNMas(t *testing.T) {
	var it []string
	for i := 1; i <= 13; i++ {
		it = append(it, fmt.Sprint("i", i))
	}
	g := List(it, 10)
	if !strings.HasSuffix(g, "y 3 más") || !strings.HasPrefix(g, "i1; i2") || strings.Contains(g, "i11") {
		t.Fatalf("got %q", g)
	}
	if List([]string{"a", "b"}, 10) != "a; b" {
		t.Fatal("short list")
	}
}

func TestClip(t *testing.T) {
	if Clip("corto", 10) != "corto" {
		t.Fatal("short")
	}
	g := Clip("hola mundo cruel", 12)
	if g != "hola mundo\n(recortado; pide una parte más específica)" {
		t.Fatalf("got %q", g)
	}
}

func TestHuman(t *testing.T) {
	loc := time.FixedZone("x", -3*3600)
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, loc)
	d := func(day, h int) time.Time { return time.Date(2026, 11, day, h, 0, 0, 0, loc) }
	cases := []struct {
		t    time.Time
		wt   bool
		want string
	}{
		{d(18, 15), true, "hoy 15:00"},
		{d(19, 15), true, "mañana 15:00"},
		{d(19, 15), false, "mañana"},
		{d(17, 9), false, "ayer"},
		{d(20, 15), true, "vie 20/11 15:00"},
		{d(20, 15), false, "vie 20/11"},
	}
	for _, c := range cases {
		if g := Human(c.t, now, c.wt); g != c.want {
			t.Errorf("got %q want %q", g, c.want)
		}
	}
	if MonthName(time.November) != "noviembre" {
		t.Fatal("month")
	}
}

func TestParseWhenForms(t *testing.T) {
	loc := time.FixedZone("x", -3*3600)
	for _, s := range []string{"2026-11-21T15:00", "2026-11-21 15:00", "2026-11-21T15:00:00", "2026-11-21 15:00:00"} {
		g, d, err := ParseWhen(s, loc)
		if err != nil || d || !g.Equal(time.Date(2026, 11, 21, 15, 0, 0, 0, loc)) {
			t.Errorf("%s: %v %v %v", s, g, d, err)
		}
	}
	g, d, err := ParseWhen("2026-11-21", loc)
	if err != nil || !d || !g.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, loc)) {
		t.Errorf("date: %v %v %v", g, d, err)
	}
	g, d, err = ParseWhen("2026-11-21T18:00:00Z", loc)
	if err != nil || d || g.Location() != loc || g.Hour() != 15 {
		t.Errorf("rfc: %v %v %v", g, d, err)
	}
	_, _, err = ParseWhen("mañana", loc)
	if err == nil || err.Error() != "Fecha no válida: mañana (usa AAAA-MM-DD o AAAA-MM-DDTHH:MM)" {
		t.Errorf("err: %v", err)
	}
}

func TestParsePriority(t *testing.T) {
	for in, want := range map[string]int{"Alta": 1, "3": 3, "": 5, "media": 5, "BAJA": 9, "0": 0} {
		if g, err := ParsePriority(in); err != nil || g != want {
			t.Errorf("%q: %d %v", in, g, err)
		}
	}
	if _, err := ParsePriority("urgente"); err == nil {
		t.Error("want error")
	}
	for p, w := range map[int]string{0: "", 1: "alta", 4: "alta", 5: "media", 6: "baja", 9: "baja"} {
		if PriorityWord(p) != w {
			t.Errorf("%d", p)
		}
	}
}

func TestFindEmptyQueryMatchesNothing(t *testing.T) {
	for _, q := range []string{"", "¿?", "  "} {
		if got := Find([]string{"a", "b"}, q); got != nil {
			t.Errorf("Find(%q) = %v", q, got)
		}
	}
}
