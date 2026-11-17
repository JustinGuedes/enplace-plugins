package versionfile

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Version styles and what a release bumps.
const (
	Short    = "short"  // 1.4
	Semantic = "semver" // 1.4.0
	Calendar = "calver" // 2026.11
	Number   = "number" // 14

	Major = "major"
	Minor = "minor"
	Patch = "patch"
)

// Next is the version after v.
//   - short:  "1.4" -> "2.0" (major), "1.5" (minor) or "1.4.1" (patch)
//   - semver: "1.4.0" -> "2.0.0", "1.5.0" or "1.4.1"
//   - calver: year.month of the day it ships (YYYY-MM-DD), then .1, .2
//     for more in that month
//   - number: "14" -> "15"
func Next(v, style, bump, ship string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	if style == Calendar {
		t, err := time.Parse("2006-01-02", ship)
		if err != nil {
			return v + ".1"
		}
		month := fmt.Sprintf("%d.%d", t.Year(), int(t.Month()))
		if v == month {
			return month + ".1"
		}
		if rest, ok := strings.CutPrefix(v, month+"."); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				return fmt.Sprintf("%s.%d", month, n+1)
			}
		}
		return month
	}
	nums := make([]int, 0, 3)
	if v != "" {
		for _, p := range strings.Split(v, ".") {
			n, err := strconv.Atoi(p)
			if err != nil {
				return v + ".1"
			}
			nums = append(nums, n)
		}
	}
	if style == Number {
		if len(nums) == 0 {
			return "1"
		}
		return strconv.Itoa(nums[0] + 1)
	}
	if len(nums) == 0 {
		if style == Semantic {
			return "1.0.0"
		}
		return "1.0"
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	switch bump {
	case Major:
		nums = []int{nums[0] + 1, 0, 0}
	case Patch:
		nums[2]++
	default:
		nums = []int{nums[0], nums[1] + 1, 0}
	}
	if style == Semantic || nums[2] != 0 {
		return fmt.Sprintf("%d.%d.%d", nums[0], nums[1], nums[2])
	}
	return fmt.Sprintf("%d.%d", nums[0], nums[1])
}

// NextBuild is the build number after b: "42" -> "43", "" -> "1". A build
// number that isn't a plain number, like 1.4.2, counts up its last part.
func NextBuild(b string) string {
	b = strings.TrimSpace(b)
	if b == "" {
		return "1"
	}
	head, last := "", b
	if i := strings.LastIndex(b, "."); i >= 0 {
		head, last = b[:i+1], b[i+1:]
	}
	n, err := strconv.Atoi(last)
	if err != nil {
		return b
	}
	return head + strconv.Itoa(n+1)
}
