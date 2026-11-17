package main

import "strings"

// fromTag reads a version from a tag name like "v1.4", "1.4.0" or
// "release-1.4", or "" when it isn't one.
func fromTag(tag string) string {
	t := strings.TrimPrefix(tag, "release-")
	t = strings.TrimPrefix(strings.TrimPrefix(t, "release/"), "v")
	if numbers(t) == nil {
		return ""
	}
	return t
}

// newer says whether a is a later version than b. Versions it can't read
// are never newer.
func newer(a, b string) bool {
	na, nb := numbers(a), numbers(b)
	if na == nil {
		return false
	}
	if nb == nil {
		return true
	}
	for i := 0; i < len(na) || i < len(nb); i++ {
		x, y := 0, 0
		if i < len(na) {
			x = atoi(na[i])
		}
		if i < len(nb) {
			y = atoi(nb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// numbers splits "v1.4.0-beta+3" into ["1","4"], or nil.
func numbers(v string) []string {
	v = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "release-"), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" || len(p) > 9 {
			return nil
		}
	}
	return parts
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
