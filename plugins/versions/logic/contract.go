package main

import (
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/versions/versionfile"
)

// What Versions and the plugins linked to it publish for each other, as
// "public." state on a project or product. A code plugin (GitHub) reads
// the files Versions asks for and opens the pull requests it asks for;
// Release cycle numbers its releases by Versions' rules.

// Want ("public.want", from Versions) is what a code plugin reads for it at
// each new commit: the files whose names match Patterns (best first, case
// doesn't matter), outside the Skip folders and no deeper than MaxDepth;
// the Read best of those (best first, then nearest the root) and every one
// of Paths. A file bigger than a code plugin likes to share comes as just
// its lines that say one of Grep.
type Want struct {
	Patterns []string `json:"patterns"`
	Skip     []string `json:"skip"`
	MaxDepth int      `json:"max_depth"`
	Read     int      `json:"read"`
	Paths    []string `json:"paths,omitempty"`
	Grep     []string `json:"grep"`
}

// Files ("public.files", from a code plugin) is what it read at commit SHA:
// every file that matched (Matched) and the ones it read (Files).
type Files struct {
	SHA     string    `json:"sha"`
	Matched []string  `json:"matched"`
	Files   []File    `json:"files"`
	At      time.Time `json:"at"`
}

// File is one file a code plugin read. Partial: Text is only the lines
// that say one of Want.Grep; Missing: there's no such file.
type File struct {
	Path    string `json:"path"`
	Text    string `json:"text,omitempty"`
	Partial bool   `json:"partial,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

// Bump ("public.bump", from Versions) asks a code plugin to open a pull
// request from Branch, off the head of the project's branch, that makes
// Edits. A code plugin does each ID once.
type Bump struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Body    string             `json:"body"`
	Branch  string             `json:"branch"`
	Edits   []versionfile.Edit `json:"edits"`
	Version string             `json:"version"`
	Build   string             `json:"build,omitempty"`
	// Release is the id of the release it's for, when a cut-off asked.
	Release string    `json:"release,omitempty"`
	At      time.Time `json:"at"`
}

// Bumped ("public.bumps", from a code plugin) is how each Bump went, by
// its ID.
type Bumped struct {
	State  string    `json:"state"` // "opened" or "failed"
	URL    string    `json:"url,omitempty"`
	Number int       `json:"number,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

// Published ("public.versions", from Versions) is the version in the code
// and how releases are numbered, for Release cycle and anyone else.
type Published struct {
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	File    string `json:"file,omitempty"`
	// Style is short, semver, calver or number; Bump major, minor or
	// patch; NextVersion where numbering starts.
	Style       string `json:"style"`
	Bump        string `json:"bump"`
	NextVersion string `json:"next_version,omitempty"`
}

// numbering ("public.numbering", from Release cycle) is how it numbered
// releases before Versions did, kept so nothing changes on switching
// Versions on.
type numbering struct {
	Style       string `json:"style"`
	Bump        string `json:"bump"`
	NextVersion string `json:"next_version"`
}

// codeInfo is what a code plugin publishes as "public.code": here, the
// file its own version file setting named before Versions took it over.
type codeInfo struct {
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	VersionFile string `json:"version_file"`
}

// release is the part of Release cycle's "public.releases" Versions reads.
type release struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Cutoff   string `json:"cutoff"`
	ShipDate string `json:"ship_date"`
	State    string `json:"state"`
	Deploy   bool   `json:"deploy"`
}
