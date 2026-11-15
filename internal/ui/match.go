package ui

import (
	"slices"
	"strings"
)

// matchTier says how well a query matches an entry. A higher tier is a
// better match.
type matchTier int

const (
	noMatch matchTier = iota
	fuzzyTier
	containsTier
	prefixTier
)

// tierOf returns the best tier of the fields for query. A prefix match
// counts only on the lead fields, for example the name of a command. The
// other tiers count on all fields. query must be in lower case.
func tierOf(query string, lead, rest []string) matchTier {
	best := noMatch
	for i, f := range slices.Concat(lead, rest) {
		f = strings.ToLower(f)
		switch {
		case i < len(lead) && strings.HasPrefix(f, query):
			return prefixTier
		case strings.Contains(f, query):
			best = max(best, containsTier)
		case fuzzyMatch(f, query):
			best = max(best, fuzzyTier)
		}
	}
	return best
}

// rankMatches returns the indexes of the n entries that match query: the
// prefix matches first, then the entries that contain query, then the fuzzy
// matches. Each group keeps the order of the entries. fields returns the
// lead fields and the other fields of entry i. An empty query matches all
// entries. rankMatches reuses the storage of dst.
func rankMatches(dst []int, query string, n int, fields func(i int) (lead, rest []string)) []int {
	dst = dst[:0]
	query = strings.ToLower(query)
	if query == "" {
		for i := range n {
			dst = append(dst, i)
		}
		return dst
	}
	tiers := make([]matchTier, n)
	for i := range n {
		lead, rest := fields(i)
		tiers[i] = tierOf(query, lead, rest)
	}
	for _, t := range []matchTier{prefixTier, containsTier, fuzzyTier} {
		for i, ti := range tiers {
			if ti == t {
				dst = append(dst, i)
			}
		}
	}
	return dst
}

// fuzzyMatch returns true if all characters of pattern appear in s in
// order. It compares runes, so letters outside ASCII match too.
func fuzzyMatch(s, pattern string) bool {
	p := []rune(pattern)
	pi := 0
	for _, r := range s {
		if pi == len(p) {
			break
		}
		if r == p[pi] {
			pi++
		}
	}
	return pi == len(p)
}
