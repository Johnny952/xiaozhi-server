package nc

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Entry is a file or folder; Path is relative to the user's root.
type Entry struct {
	Path     string
	Name     string
	Dir      bool
	Size     int64
	Modified time.Time
}

const searchLimit = 20

// Get downloads a file and returns its ETag.
func (c *Client) Get(ctx context.Context, p string) ([]byte, string, error) {
	resp, err := c.do(ctx, http.MethodGet, c.filesRoot()+escapePath(p), nil, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", statusErr("GET", p, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("nextcloud: read body of %s: %w", p, err)
	}
	return b, cleanETag(resp.Header.Get("ETag")), nil
}

// Put writes a file conditionally: etag=="" means "only if it does not exist",
// otherwise "only if it still has this ETag". A 423 (locked) is retried once
// after LockWait. A failed precondition returns ErrConflict without retry.
func (c *Client) Put(ctx context.Context, p string, body []byte, etag string) (string, error) {
	hdr := map[string]string{}
	if etag == "" {
		hdr["If-None-Match"] = "*"
	} else {
		hdr["If-Match"] = etag
	}
	if body == nil {
		body = []byte{}
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.do(ctx, http.MethodPut, c.filesRoot()+escapePath(p), body, hdr)
		if err != nil {
			return "", err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusLocked && attempt == 0:
			select {
			case <-time.After(c.LockWait):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			continue
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return cleanETag(resp.Header.Get("ETag")), nil
		}
		return "", statusErr("PUT", p, resp.StatusCode)
	}
}

// Append reads the file (missing is fine), lets build produce the new
// content, and writes it conditionally. On a concurrent change (412) the
// whole cycle is repeated up to 2 more times, then ErrConflict is returned.
func (c *Client) Append(ctx context.Context, p string, build func(old []byte, exists bool) ([]byte, error)) error {
	const attempts = 3
	for i := 0; i < attempts; i++ {
		old, etag, err := c.Get(ctx, p)
		exists := true
		if errors.Is(err, ErrNotFound) {
			old, etag, exists, err = nil, "", false, nil
		}
		if err != nil {
			return err
		}
		nb, err := build(old, exists)
		if err != nil {
			return err
		}
		_, err = c.Put(ctx, p, nb, etag)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w: %s changed concurrently", ErrConflict, p)
}

// MkdirAll creates every segment of dir; an existing folder (405) is fine.
func (c *Client) MkdirAll(ctx context.Context, dir string) error {
	cur := ""
	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur += "/" + seg
		resp, err := c.do(ctx, "MKCOL", c.filesRoot()+escapePath(cur), nil, nil)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusMethodNotAllowed || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
			continue
		}
		return statusErr("MKCOL", cur, resp.StatusCode)
	}
	return nil
}

const propfindBody = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/><d:getlastmodified/><d:displayname/></d:prop></d:propfind>`

// List returns the direct children of dir (not dir itself).
func (c *Client) List(ctx context.Context, dir string) ([]Entry, error) {
	dir = "/" + strings.Trim(dir, "/")
	resp, err := c.do(ctx, "PROPFIND", c.filesRoot()+escapePath(dir), []byte(propfindBody),
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusErr("PROPFIND", dir, resp.StatusCode)
	}
	all, err := c.parseMultistatus(resp.Body)
	if err != nil {
		return nil, err
	}
	self := strings.TrimRight(dir, "/")
	out := all[:0]
	for _, e := range all {
		if strings.TrimRight(e.Path, "/") == self || e.Path == "" {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// SearchName finds files and folders whose name contains name (max 20).
func (c *Client) SearchName(ctx context.Context, name string) ([]Entry, error) {
	var esc bytes.Buffer
	// LIKE wildcards in the user's text must match literally; backslash is the
	// escape character Nextcloud's search uses.
	lit := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(name)
	xml.EscapeText(&esc, []byte(lit))
	body := fmt.Sprintf(`<?xml version="1.0"?>
<d:searchrequest xmlns:d="DAV:"><d:basicsearch>
<d:select><d:prop><d:resourcetype/><d:getcontentlength/><d:getlastmodified/><d:displayname/></d:prop></d:select>
<d:from><d:scope><d:href>/files/%s</d:href><d:depth>infinity</d:depth></d:scope></d:from>
<d:where><d:like><d:prop><d:displayname/></d:prop><d:literal>%%%s%%</d:literal></d:like></d:where>
<d:orderby/>
<d:limit><d:nresults>%d</d:nresults></d:limit>
</d:basicsearch></d:searchrequest>`, xmlText(c.user), esc.String(), searchLimit)
	resp, err := c.do(ctx, "SEARCH", "/remote.php/dav/", []byte(body),
		map[string]string{"Content-Type": "text/xml"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusErr("SEARCH", "/", resp.StatusCode)
	}
	es, err := c.parseMultistatus(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(es) > searchLimit {
		es = es[:searchLimit]
	}
	return es, nil
}

func xmlText(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

type multistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Status string `xml:"status"`
			Prop   struct {
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Length   int64  `xml:"getcontentlength"`
				Modified string `xml:"getlastmodified"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

func (c *Client) parseMultistatus(r io.Reader) ([]Entry, error) {
	var ms multistatus
	if err := xml.NewDecoder(r).Decode(&ms); err != nil {
		return nil, fmt.Errorf("nextcloud: parse multistatus: %w", err)
	}
	marker := c.filesRoot()
	var out []Entry
	for _, rs := range ms.Responses {
		u, err := url.Parse(strings.TrimSpace(rs.Href))
		if err != nil {
			continue
		}
		p := u.Path // already percent-decoded
		i := strings.Index(p, marker)
		if i < 0 {
			// href may use a differently-escaped user; fall back to the generic marker
			j := strings.Index(p, "/remote.php/dav/files/")
			if j < 0 {
				continue
			}
			rest := p[j+len("/remote.php/dav/files/"):]
			k := strings.Index(rest, "/")
			if k < 0 {
				p = "/"
			} else {
				p = rest[k:]
			}
		} else {
			p = p[i+len(marker):]
		}
		if p == "" {
			p = "/"
		}
		e := Entry{Path: strings.TrimRight(p, "/")}
		if e.Path == "" {
			e.Path = "/"
		}
		e.Name = path.Base(e.Path)
		for _, ps := range rs.Propstat {
			if !strings.Contains(ps.Status, " 200") {
				continue
			}
			e.Dir = ps.Prop.ResourceType.Collection != nil
			e.Size = ps.Prop.Length
			if t, err := http.ParseTime(ps.Prop.Modified); err == nil {
				e.Modified = t
			}
		}
		out = append(out, e)
	}
	return out, nil
}
