package main

import (
	"strings"
	"unicode"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// platforms are the words that name each platform, in product names and
// keys and in job names (Unity's target names among them).
var platforms = [][]string{
	{"ios", "iphone", "ipad"},
	{"android", "aab", "apk"},
	{"mac", "macos", "osx", "standaloneosx"},
	{"windows", "win", "win64", "pc", "steam", "standalonewindows", "standalonewindows64"},
	{"linux", "standalonelinux64"},
	{"webgl", "web"},
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) })
}

// platform is the platform a product is for, from its key and name.
func platform(p plugin.Project) []string {
	ws := append(words(p.Product), words(p.ProductName)...)
	for _, pl := range platforms {
		for _, w := range ws {
			for _, x := range pl {
				if w == x {
					return pl
				}
			}
		}
	}
	return nil
}

// names says a job name is for a platform: a word of it, or, for the
// longer words, part of one (StandaloneWindows64 is Windows).
func names(job string, pl []string) bool {
	for _, w := range words(job) {
		for _, x := range pl {
			if w == x || len(x) >= 5 && strings.Contains(w, x) {
				return true
			}
		}
	}
	return false
}

// jobFor is the job that builds a product: the one its settings pick, or
// the one its platform names. It's "" for the project, and for a product
// no job names. This is the one place products meet jobs.
func (r *run) jobFor(p plugin.Project) string {
	if p.Product == "" {
		return ""
	}
	if j := r.setup(p.ID).Job; j != "" {
		return j
	}
	pl := platform(p)
	if pl == nil {
		return ""
	}
	if s := r.status(); s != nil {
		for _, x := range s.Runs {
			for _, j := range x.Jobs {
				if names(j.Name, pl) {
					return j.Name
				}
			}
		}
	}
	return ""
}

// productFor is the key of the product a job builds, "" for none.
func (r *run) productFor(job string) string {
	sc := r.scope()
	if sc.Product != "" {
		return "" // a product's own read: its work is already its own
	}
	for _, pr := range sc.Products {
		p := plugin.Project{ID: sc.ID + "/" + pr.Key, Product: pr.Key, ProductName: pr.Name, Parent: sc.ID}
		if pl := platform(p); r.setup(p.ID).Job == job || r.setup(p.ID).Job == "" && pl != nil && names(job, pl) {
			return pr.Key
		}
	}
	return ""
}

// latest is the newest run that has the named job, and that job.
func latest(s *Status, job string) (Run, Job, bool) {
	if s != nil {
		for _, x := range s.Runs {
			for _, j := range x.Jobs {
				if j.Name == job {
					return x, j, true
				}
			}
		}
	}
	return Run{}, Job{}, false
}
