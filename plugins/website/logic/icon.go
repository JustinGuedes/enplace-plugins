package main

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Icon is the site's own icon, as the project shows it.
type Icon struct {
	// Site is the address it was found for.
	Site string `json:"site"`
	// URL is the icon, or empty when the site has none Enplace can show.
	URL     string    `json:"url,omitempty"`
	FoundAt time.Time `json:"found_at"`
}

// iconEvery is how often the icon is looked for again.
const iconEvery = 24 * time.Hour

// fetcher fetches through Enplace (plugin.Fetch), or a stand-in in tests.
type fetcher func(plugin.FetchRequest) (*plugin.FetchResponse, error)

// iconCandidate is an icon a page or its manifest points at.
type iconCandidate struct {
	url  string
	size int  // its biggest side in pixels, guessed when not given
	svg  bool // drawn by browsers but not by the Apple app
	rank int  // which to prefer at the same size: higher wins
}

var (
	linkTag = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	baseTag = regexp.MustCompile(`(?is)<base\b[^>]*>`)
	attrRe  = regexp.MustCompile(`(?is)([a-z][a-z0-9_:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	sizeRe  = regexp.MustCompile(`(?i)(\d+)x(\d+)`)
)

func attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		v := m[2] + m[3] + m[4]
		out[strings.ToLower(m[1])] = html.UnescapeString(strings.TrimSpace(v))
	}
	return out
}

// biggest reads a sizes attribute like "16x16 32x32" or "any".
func biggest(sizes string) int {
	n := 0
	if strings.Contains(strings.ToLower(sizes), "any") {
		return 512 // scalable
	}
	for _, m := range sizeRe.FindAllStringSubmatch(sizes, -1) {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		n = max(n, w, h)
	}
	return n
}

func isSVG(link, typ string) bool {
	if strings.Contains(strings.ToLower(typ), "svg") {
		return true
	}
	u, err := url.Parse(link)
	return err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".svg")
}

// pageIcons reads the icons a page links to, and its manifest's address.
func pageIcons(page []byte, base *url.URL) (icons []iconCandidate, manifest string) {
	// Only the head holds icons; don't read a huge body.
	head := page
	if i := strings.Index(strings.ToLower(string(head[:min(len(head), 512<<10)])), "</head>"); i >= 0 {
		head = head[:i]
	} else if len(head) > 512<<10 {
		head = head[:512<<10]
	}
	if m := baseTag.Find(head); m != nil {
		if h := attrs(string(m))["href"]; h != "" {
			if b, err := base.Parse(h); err == nil {
				base = b
			}
		}
	}
	for _, tag := range linkTag.FindAll(head, -1) {
		a := attrs(string(tag))
		href := a["href"]
		if href == "" || strings.HasPrefix(href, "data:") {
			continue
		}
		u, err := base.Parse(href)
		if err != nil {
			continue
		}
		rels := strings.Fields(strings.ToLower(a["rel"]))
		switch {
		case slices.Contains(rels, "manifest"):
			if manifest == "" {
				manifest = u.String()
			}
		case slices.Contains(rels, "apple-touch-icon"), slices.Contains(rels, "apple-touch-icon-precomposed"):
			size := biggest(a["sizes"])
			if size == 0 {
				size = 180 // Apple's default size
			}
			icons = append(icons, iconCandidate{url: u.String(), size: size, svg: isSVG(u.String(), a["type"]), rank: 3})
		case slices.Contains(rels, "icon"), slices.Contains(rels, "shortcut"):
			size := biggest(a["sizes"])
			svg := isSVG(u.String(), a["type"])
			if size == 0 {
				size = 32
			}
			if svg {
				size = 512
			}
			icons = append(icons, iconCandidate{url: u.String(), size: size, svg: svg, rank: 1})
		}
	}
	return icons, manifest
}

// manifestIcons reads a web app manifest's icons.
func manifestIcons(body []byte, base *url.URL) []iconCandidate {
	var m struct {
		Icons []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	var out []iconCandidate
	for _, ic := range m.Icons {
		purpose := strings.Fields(strings.ToLower(ic.Purpose))
		// A maskable-only icon is padded for a mask; it looks shrunk as is.
		if len(purpose) > 0 && !slices.Contains(purpose, "any") {
			continue
		}
		u, err := base.Parse(ic.Src)
		if err != nil || ic.Src == "" || strings.HasPrefix(ic.Src, "data:") {
			continue
		}
		svg := isSVG(u.String(), ic.Type)
		size := biggest(ic.Sizes)
		if size == 0 {
			size = 192
		}
		out = append(out, iconCandidate{url: u.String(), size: size, svg: svg, rank: 2})
	}
	return out
}

// best orders icons: pictures before drawings (the Apple app can't draw
// SVG), then the biggest up to 512, then by rank.
func best(icons []iconCandidate) []iconCandidate {
	icons = slices.Clone(icons)
	slices.SortStableFunc(icons, func(a, b iconCandidate) int {
		if a.svg != b.svg {
			if a.svg {
				return 1
			}
			return -1
		}
		if sa, sb := min(a.size, 512), min(b.size, 512); sa != sb {
			return sb - sa
		}
		return b.rank - a.rank
	})
	return icons
}

// findIcon looks for a site's own icon: its Apple touch icon, its manifest's
// icons, the icons its page links to, then /favicon.ico. page is the
// site's home page as the check fetched it, if it did. Only an https icon
// can become the project's icon.
func findIcon(fetch fetcher, link string, page []byte) string {
	base, err := url.Parse(link)
	if err != nil || base.Host == "" {
		return ""
	}
	if page == nil {
		if r, err := fetch(plugin.FetchRequest{URL: link, Headers: pageHeaders}); err == nil && r.Status < 400 {
			page = r.Body
		}
	}
	icons, manifest := pageIcons(page, base)
	if manifest != "" {
		if mu, err := url.Parse(manifest); err == nil {
			if r, err := fetch(plugin.FetchRequest{URL: manifest, Headers: map[string]string{"Accept": "application/manifest+json,application/json,*/*"}}); err == nil && r.Status < 300 {
				icons = append(icons, manifestIcons(r.Body, mu)...)
			}
		}
	}
	root := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/favicon.ico"}
	// favicon.ico goes in last at its usual size, before any drawing.
	icons = append(icons, iconCandidate{url: root.String(), size: 32, rank: 0})
	tried := 0
	for _, ic := range best(icons) {
		if !strings.HasPrefix(ic.url, "https://") {
			continue
		}
		// Check the icon is really there. One on a host Enplace may not
		// fetch from (a CDN) is taken on the page's word.
		r, err := fetch(plugin.FetchRequest{URL: ic.url, Headers: map[string]string{"Accept": "image/*"}})
		if err != nil {
			if strings.Contains(err.Error(), "may not fetch") {
				return ic.url
			}
		} else if r.Status < 300 && looksLikeImage(r) {
			return ic.url
		}
		if tried++; tried >= 4 {
			break
		}
	}
	return ""
}

func looksLikeImage(r *plugin.FetchResponse) bool {
	if len(r.Body) == 0 {
		return false
	}
	ct := strings.ToLower(r.Headers["content-type"])
	if strings.HasPrefix(ct, "image/") {
		return true
	}
	// Some servers send icons as octet-stream or nothing; sniff instead.
	b := r.Body
	switch {
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n",
		len(b) >= 4 && string(b[:4]) == "\x00\x00\x01\x00", // ico
		len(b) >= 3 && string(b[:3]) == "\xff\xd8\xff",     // jpeg
		len(b) >= 6 && string(b[:4]) == "GIF8",
		len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return true
	}
	return false
}
