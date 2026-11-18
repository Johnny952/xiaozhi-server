package nc

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nextcloud-mcp/text"
)

// Calendar is a CalDAV calendar collection of the user.
type Calendar struct {
	ID     string
	Name   string
	Events bool
	Todos  bool
}

// CalObject is one .ics resource. Href is the path below the server base.
type CalObject struct {
	Href, ETag, Data string
}

func (c *Client) calHome() string {
	return "/remote.php/dav/calendars/" + url.PathEscape(c.user)
}

const calendarsPropfind = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:displayname/><d:resourcetype/><c:supported-calendar-component-set/></d:prop></d:propfind>`

var systemCalendars = map[string]bool{
	"inbox": true, "outbox": true, "trashbin": true, "contact_birthdays": true,
}

type calMultistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Status string `xml:"status"`
			Prop   struct {
				DisplayName  string `xml:"displayname"`
				ResourceType struct {
					Calendar *struct{} `xml:"calendar"`
				} `xml:"resourcetype"`
				Comps []struct {
					Name string `xml:"name,attr"`
				} `xml:"supported-calendar-component-set>comp"`
				ETag string `xml:"getetag"`
				Data string `xml:"calendar-data"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

// Calendars lists the user's real calendars, skipping scheduling/system
// collections and anything that is not a C:calendar (e.g. address books).
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	resp, err := c.do(ctx, "PROPFIND", c.calHome()+"/", []byte(calendarsPropfind),
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusErr("PROPFIND", "calendars", resp.StatusCode)
	}
	var ms calMultistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("nextcloud: parse calendars: %w", err)
	}
	var out []Calendar
	for _, rs := range ms.Responses {
		id := lastSegment(rs.Href)
		if id == "" || systemCalendars[id] || strings.EqualFold(id, c.user) {
			continue
		}
		for _, ps := range rs.Propstat {
			if !strings.Contains(ps.Status, " 200") || ps.Prop.ResourceType.Calendar == nil {
				continue
			}
			cal := Calendar{ID: id, Name: ps.Prop.DisplayName}
			if cal.Name == "" {
				cal.Name = id
			}
			if len(ps.Prop.Comps) == 0 {
				cal.Events, cal.Todos = true, true // no set advertised: all allowed
			}
			for _, comp := range ps.Prop.Comps {
				switch strings.ToUpper(comp.Name) {
				case "VEVENT":
					cal.Events = true
				case "VTODO":
					cal.Todos = true
				}
			}
			out = append(out, cal)
		}
	}
	return out, nil
}

func lastSegment(href string) string {
	p := strings.TrimSpace(href)
	if u, err := url.Parse(p); err == nil {
		p = u.Path
	}
	p = strings.Trim(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// MkCalendarTodos creates a calendar that only accepts VTODO.
func (c *Client) MkCalendarTodos(ctx context.Context, name string) (Calendar, error) {
	id := text.Slug(name)
	if id == "" {
		return Calendar{}, fmt.Errorf("nextcloud: invalid calendar name")
	}
	body := `<?xml version="1.0"?>
<c:mkcalendar xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:d="DAV:"><d:set><d:prop>` +
		`<d:displayname>` + xmlText(name) + `</d:displayname>` +
		`<c:supported-calendar-component-set><c:comp name="VTODO"/></c:supported-calendar-component-set>` +
		`</d:prop></d:set></c:mkcalendar>`
	resp, err := c.do(ctx, "MKCALENDAR", c.calHome()+"/"+url.PathEscape(id)+"/", []byte(body),
		map[string]string{"Content-Type": "application/xml"})
	if err != nil {
		return Calendar{}, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusConflict {
		return Calendar{}, fmt.Errorf("%w: %s", ErrCollision, id)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Calendar{}, statusErr("MKCALENDAR", id, resp.StatusCode)
	}
	return Calendar{ID: id, Name: name, Todos: true}, nil
}

const icalTime = "20060102T150405Z"

// Query runs a calendar-query REPORT for components of type comp (VEVENT or
// VTODO). With a range it adds C:time-range (UTC); with expand (and a range)
// it asks the server to expand recurrences.
func (c *Client) Query(ctx context.Context, calID, comp string, from, to *time.Time, expand bool) ([]CalObject, error) {
	rng, data := "", "<C:calendar-data/>"
	if from != nil && to != nil {
		s, e := from.UTC().Format(icalTime), to.UTC().Format(icalTime)
		rng = fmt.Sprintf(`<C:time-range start="%s" end="%s"/>`, s, e)
		if expand {
			data = fmt.Sprintf(`<C:calendar-data><C:expand start="%s" end="%s"/></C:calendar-data>`, s, e)
		}
	}
	body := `<?xml version="1.0"?>
<C:calendar-query xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:D="DAV:"><D:prop><D:getetag/>` + data + `</D:prop>` +
		`<C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="` + xmlText(comp) + `">` + rng +
		`</C:comp-filter></C:comp-filter></C:filter></C:calendar-query>`
	resp, err := c.do(ctx, "REPORT", c.calHome()+"/"+url.PathEscape(calID)+"/", []byte(body),
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusErr("REPORT", calID, resp.StatusCode)
	}
	var ms calMultistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("nextcloud: parse report: %w", err)
	}
	var out []CalObject
	for _, rs := range ms.Responses {
		for _, ps := range rs.Propstat {
			if !strings.Contains(ps.Status, " 200") || ps.Prop.Data == "" {
				continue
			}
			out = append(out, CalObject{Href: c.relHref(rs.Href), ETag: cleanETag(strings.TrimSpace(ps.Prop.ETag)), Data: ps.Prop.Data})
		}
	}
	return out, nil
}

// relHref turns a server href into a path usable with c.base (drops any
// scheme/host and the base's own path prefix, keeps escaping).
func (c *Client) relHref(href string) string {
	p := strings.TrimSpace(href)
	u, err := url.Parse(p)
	if err != nil {
		return p
	}
	p = u.EscapedPath()
	if bu, err := url.Parse(c.base); err == nil {
		if prefix := strings.TrimRight(bu.EscapedPath(), "/"); prefix != "" {
			p = strings.TrimPrefix(p, prefix)
		}
	}
	return p
}

// GetObject downloads one calendar object by href (as returned by Query).
func (c *Client) GetObject(ctx context.Context, href string) (CalObject, error) {
	href = c.relHref(href)
	resp, err := c.do(ctx, http.MethodGet, href, nil, nil)
	if err != nil {
		return CalObject{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CalObject{}, statusErr("GET", href, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return CalObject{}, fmt.Errorf("nextcloud: read calendar object: %w", err)
	}
	return CalObject{Href: href, ETag: cleanETag(resp.Header.Get("ETag")), Data: string(b)}, nil
}

// PutObject writes {uid}.ics into calID and returns the new ETag. etag==""
// means create-only (If-None-Match: *), otherwise If-Match: etag.
func (c *Client) PutObject(ctx context.Context, calID, uid, data, etag string) (string, error) {
	hdr := map[string]string{"Content-Type": "text/calendar; charset=utf-8"}
	if etag == "" {
		hdr["If-None-Match"] = "*"
	} else {
		hdr["If-Match"] = etag
	}
	p := c.calHome() + "/" + url.PathEscape(calID) + "/" + url.PathEscape(uid) + ".ics"
	resp, err := c.do(ctx, http.MethodPut, p, []byte(data), hdr)
	if err != nil {
		return "", err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", statusErr("PUT", calID+"/"+uid+".ics", resp.StatusCode)
	}
	return cleanETag(resp.Header.Get("ETag")), nil
}

// DeleteObject removes a calendar object, guarded by If-Match.
func (c *Client) DeleteObject(ctx context.Context, href, etag string) error {
	href = c.relHref(href)
	hdr := map[string]string{}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	resp, err := c.do(ctx, http.MethodDelete, href, nil, hdr)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErr("DELETE", href, resp.StatusCode)
	}
	return nil
}
