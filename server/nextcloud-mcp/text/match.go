package text

import (
	"sort"
	"strings"
)

// Find returns indices of titles equal (normalized) to q; if none, those
// containing q; else nil.
func Find(titles []string, q string) []int {
	nq := Normalize(q)
	if nq == "" {
		return nil // an empty query would match every title
	}
	var exact, contains []int
	for i, t := range titles {
		nt := Normalize(t)
		if nt == nq {
			exact = append(exact, i)
		} else if strings.Contains(nt, nq) {
			contains = append(contains, i)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if len(contains) > 0 {
		return contains
	}
	return nil
}

// Candidates returns up to n titles sharing at least one normalized word with
// q, ordered by shared-word count (desc) then alphabetically.
func Candidates(titles []string, q string, n int) []string {
	qw := map[string]bool{}
	for _, w := range strings.Fields(Normalize(q)) {
		qw[w] = true
	}
	type sc struct {
		t string
		n int
	}
	var all []sc
	for _, t := range titles {
		seen := map[string]bool{}
		c := 0
		for _, w := range strings.Fields(Normalize(t)) {
			if qw[w] && !seen[w] {
				seen[w] = true
				c++
			}
		}
		if c > 0 {
			all = append(all, sc{t, c})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].t < all[j].t
	})
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].t)
	}
	return out
}
