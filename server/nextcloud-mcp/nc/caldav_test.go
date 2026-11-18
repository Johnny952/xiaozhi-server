package nc_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"nextcloud-mcp/nc"
)

func TestCalendarsSkipsSystemAndClassifies(t *testing.T) {
	c, f := setup(t)
	f.AddCalendar("inbox", "Inbox", "VEVENT")
	f.AddCalendar("outbox", "Outbox", "VEVENT")
	f.AddCollection("trashbin", "Papelera", "other")
	f.AddCalendar("contact_birthdays", "Cumpleaños", "VEVENT")
	f.AddCalendar("personal", "Personal", "VEVENT", "VTODO")
	f.AddCalendar("tareas", "Tareas", "VTODO")
	f.AddCollection("contacts", "Contactos", "addressbook")

	cals, err := c.Calendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 2 {
		t.Fatalf("got %d calendars: %+v", len(cals), cals)
	}
	want := map[string]nc.Calendar{
		"personal": {ID: "personal", Name: "Personal", Events: true, Todos: true},
		"tareas":   {ID: "tareas", Name: "Tareas", Events: false, Todos: true},
	}
	for _, cal := range cals {
		if want[cal.ID] != cal {
			t.Fatalf("calendar %+v, want %+v", cal, want[cal.ID])
		}
	}
}

func TestMkCalendarSlugID(t *testing.T) {
	c, f := setup(t)
	cal, err := c.MkCalendarTodos(context.Background(), "Mesa Libre")
	if err != nil {
		t.Fatal(err)
	}
	if cal.ID != "mesa-libre" || cal.Name != "Mesa Libre" || !cal.Todos || cal.Events {
		t.Fatalf("cal = %+v", cal)
	}
	fc := f.Cal("mesa-libre")
	if fc == nil || len(fc.Comps) != 1 || fc.Comps[0] != "VTODO" || fc.Name != "Mesa Libre" {
		t.Fatalf("fake cal = %+v", fc)
	}
}

const todoICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:t1\r\nSUMMARY:Comprar\r\nDUE:20260310T120000Z\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
const eventICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:e1\r\nSUMMARY:Reunion\r\nDTSTART:20260310T150000Z\r\nDTEND:20260310T160000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestQueryBuildsTimeRangeAndExpand(t *testing.T) {
	c, f := setup(t)
	f.AddCalendar("personal", "Personal", "VEVENT", "VTODO")
	ctx := context.Background()
	if _, err := c.PutObject(ctx, "personal", "e1", eventICS, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject(ctx, "personal", "t1", todoICS, ""); err != nil {
		t.Fatal(err)
	}
	santiago := time.FixedZone("X", -3*3600)
	from := time.Date(2026, 3, 10, 9, 0, 0, 0, santiago) // 12:00Z
	to := time.Date(2026, 3, 10, 13, 0, 0, 0, santiago)  // 16:00Z
	objs, err := c.Query(ctx, "personal", "VEVENT", &from, &to, true)
	if err != nil {
		t.Fatal(err)
	}
	body := f.LastReportBody()
	if !strings.Contains(body, `time-range start="20260310T120000Z" end="20260310T160000Z"`) {
		t.Fatalf("body lacks UTC time-range: %s", body)
	}
	if !strings.Contains(body, "<C:expand") {
		t.Fatalf("body lacks expand: %s", body)
	}
	if len(objs) != 1 || !strings.Contains(objs[0].Data, "UID:e1") || objs[0].ETag == "" || objs[0].Href == "" {
		t.Fatalf("objs = %+v", objs)
	}
	// Sin rango: ni time-range ni expand; filtra por componente.
	objs, err = c.Query(ctx, "personal", "VTODO", nil, nil, false)
	if err != nil || len(objs) != 1 || !strings.Contains(objs[0].Data, "UID:t1") {
		t.Fatalf("objs = %+v err %v", objs, err)
	}
	if b := f.LastReportBody(); strings.Contains(b, "time-range") || strings.Contains(b, "expand") {
		t.Fatalf("unexpected range/expand: %s", b)
	}
	// GetObject por href.
	got, err := c.GetObject(ctx, objs[0].Href)
	if err != nil || got.ETag != objs[0].ETag || !strings.Contains(got.Data, "UID:t1") {
		t.Fatalf("GetObject = %+v err %v", got, err)
	}
}

func TestDeleteSendsIfMatch(t *testing.T) {
	c, f := setup(t)
	f.AddCalendar("tareas", "Tareas", "VTODO")
	ctx := context.Background()
	etag, err := c.PutObject(ctx, "tareas", "t1", todoICS, "")
	if err != nil || etag == "" {
		t.Fatalf("put: %q %v", etag, err)
	}
	// ETag viejo: conflicto.
	href := "/remote.php/dav/calendars/ana/tareas/t1.ics"
	if err := c.DeleteObject(ctx, href, `"viejo"`); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("want conflict, got %v", err)
	}
	if err := c.DeleteObject(ctx, href, etag); err != nil {
		t.Fatal(err)
	}
	if got := f.LastDeleteHeader().Get("If-Match"); got != etag {
		t.Fatalf("If-Match = %q, want %q", got, etag)
	}
	if _, err := c.GetObject(ctx, href); err == nil {
		t.Fatal("object should be gone")
	}
}
