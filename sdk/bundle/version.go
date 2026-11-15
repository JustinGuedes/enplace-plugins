package bundle

import (
	"errors"
	"strconv"
	"strings"
)

// Version is major.minor.patch. "1" and "1.2" are read as 1.0.0 and 1.2.0.
type Version [3]int

// ParseVersion reads a version like 1.2.3.
func ParseVersion(s string) (Version, error) {
	var v Version
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" {
		return v, errors.New("missing")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, errors.New("use major.minor.patch, like 1.2.0")
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 99999 {
			return v, errors.New("use major.minor.patch, like 1.2.0")
		}
		v[i] = n
	}
	return v, nil
}

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	for i := range v {
		if v[i] != o[i] {
			if v[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// String is the version as written, like 1.4.0.
func (v Version) String() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1]) + "." + strconv.Itoa(v[2])
}

// Range is a set of versions: "" or "*" (any), "1.2.0" (exactly),
// ">=1.2" (at least), "^1.2" (1.2 up to, not including, 2.0) or "~1.2"
// (1.2 up to, not including, 1.3).
type Range struct {
	min, max Version
	hasMax   bool
	exact    bool
}

// ParseRange reads a range.
func ParseRange(s string) (Range, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return Range{}, nil
	}
	op := ""
	for _, p := range []string{">=", "^", "~", "="} {
		if strings.HasPrefix(s, p) {
			op, s = p, s[len(p):]
			break
		}
	}
	v, err := ParseVersion(s)
	if err != nil {
		return Range{}, errors.New(`version range should look like "^1.2", "~1.2", ">=1.2" or "1.2.0"`)
	}
	r := Range{min: v}
	switch op {
	case "", "=":
		r.exact = true
	case "^":
		r.hasMax = true
		if v[0] > 0 {
			r.max = Version{v[0] + 1, 0, 0}
		} else {
			r.max = Version{0, v[1] + 1, 0}
		}
	case "~":
		r.hasMax, r.max = true, Version{v[0], v[1] + 1, 0}
	}
	return r, nil
}

// Allows reports whether v is in the range.
func (r Range) Allows(v Version) bool {
	if r.exact {
		return v.Compare(r.min) == 0
	}
	if v.Compare(r.min) < 0 {
		return false
	}
	return !r.hasMax || v.Compare(r.max) < 0
}

// Satisfies reports whether version string v is in range string r. Bad
// input never satisfies.
func Satisfies(v, r string) bool {
	vv, err := ParseVersion(v)
	if err != nil {
		return false
	}
	rr, err := ParseRange(r)
	if err != nil {
		return false
	}
	return rr.Allows(vv)
}
