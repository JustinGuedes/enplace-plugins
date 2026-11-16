package bundle

import (
	"slices"
	"strings"
	"testing"
)

func icon(body string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">` + body + `</svg>`)
}

func TestIconBecomesGlyph(t *testing.T) {
	g, err := ParseIcon(icon(`
		<path class="soft" fill="currentColor" fill-opacity="0.34" stroke="none" d="M12 2.5L20 7L20 17L12 21.5L4 17L4 7Z"/>
		<path d="m8.5 12 3.5-3.5 3.5 3.5"/>
		<g fill="currentColor" stroke="none"><circle cx="12" cy="18" r="2"/></g>`))
	if err != nil {
		t.Fatal(err)
	}
	want := Glyph{
		{D: "M12 2.5L20 7L20 17L12 21.5L4 17L4 7Z", Paint: "soft"},
		{D: "M8.5 12L12 8.5L15.5 12"},
		{D: "M10 18C10 16.9 10.9 16 12 16C13.1 16 14 16.9 14 18C14 19.1 13.1 20 12 20C10.9 20 10 19.1 10 18Z", Paint: "solid"},
	}
	if !slices.Equal(g, want) {
		t.Errorf("got  %v\nwant %v", g, want)
	}
}

func TestIconPathsBecomeTheFiveCommands(t *testing.T) {
	cases := map[string]string{
		"M2 2H10V10h-4v-2Z":              "M2 2L10 2L10 10L6 10L6 8Z",
		"M2 2 4 4 6 2":                   "M2 2L4 4L6 2",
		"M2 12C2 8 6 8 6 12S10 16 10 12": "M2 12C2 8 6 8 6 12C6 16 10 16 10 12",
		"M2 12Q6 8 10 12T18 12":          "M2 12Q6 8 10 12Q14 16 18 12",
		"M2 12a4 4 0 0010 0":             "M2 12C2 14.76 4.24 17 7 17C9.76 17 12 14.76 12 12",
		"M2 2l1e1 0":                     "M2 2L12 2",
	}
	for in, want := range cases {
		got, err := glyphPath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
		} else if got != want {
			t.Errorf("%s\n got  %s\n want %s", in, got, want)
		}
	}
}

func TestIconShapes(t *testing.T) {
	for _, shape := range []string{
		`<rect x="3" y="5" width="18" height="14" rx="3"/>`,
		`<rect x="3" y="5" width="18" height="14"/>`,
		`<ellipse cx="12" cy="12" rx="8" ry="5"/>`,
		`<line x1="3" y1="12" x2="21" y2="12"/>`,
		`<polyline points="4,8 12,16 20,8"/>`,
		`<polygon points="12 3 21 20 3 20"/>`,
	} {
		if _, err := ParseIcon(icon(shape)); err != nil {
			t.Errorf("%s: %v", shape, err)
		}
	}
}

func TestIconsThatAreRefused(t *testing.T) {
	cases := map[string][]byte{
		"another viewBox":   []byte(`<svg viewBox="0 0 48 48"><path d="M2 2L20 20"/></svg>`),
		"a transform":       icon(`<path transform="rotate(45)" d="M2 2L20 20"/>`),
		"a picture":         icon(`<image href="https://example.com/a.png"/>`),
		"a script":          icon(`<script>alert(1)</script>`),
		"nothing drawn":     icon(``),
		"off the grid":      icon(`<path d="M2 2L30 30"/>`),
		"too many layers":   icon(strings.Repeat(`<path d="M2 2L20 20"/>`, 9)),
		"broken path data":  icon(`<path d="M2 2L20"/>`),
		"not SVG":           []byte(`<html><body/></html>`),
		"not even XML":      []byte(`icon`),
		"a bad arc flag":    icon(`<path d="M2 2A4 4 0 2 1 10 10"/>`),
		"an unknown letter": icon(`<path d="M2 2X10 10"/>`),
	}
	for name, svg := range cases {
		if _, err := ParseIcon(svg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIconFilesFillTheManifest(t *testing.T) {
	files := map[string][]byte{
		"manifest.json":     []byte(`{"id":"demo","name":"Demo","version":"1.0.0","kind":"extension","levels":["project"],"contributes":{"pages":[{"key":"board","name":"Board","where":"project"}]}}`),
		"icon.svg":          icon(`<path d="M4 4L20 20"/>`),
		"icons/board.svg":   icon(`<path d="M4 20L20 4"/>`),
		"logic.wasm":        []byte("\x00asm\x01\x00\x00\x00"),
		"skills/unused.txt": nil,
	}
	delete(files, "skills/unused.txt")
	data, err := Pack(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Manifest.Glyph; len(got) != 1 || got[0].D != "M4 4L20 20" {
		t.Errorf("plugin glyph %v", got)
	}
	if got := b.Manifest.Contributes.Pages[0].Glyph; len(got) != 1 || got[0].D != "M4 20L20 4" {
		t.Errorf("page glyph %v", got)
	}

	files["icons/missing.svg"] = icon(`<path d="M4 4L20 20"/>`)
	if _, err := Pack(files, nil); err == nil {
		t.Error("packed an icon for a page that doesn't exist")
	}
	delete(files, "icons/missing.svg")
	files["manifest.json"] = []byte(`{"id":"demo","name":"Demo","version":"1.0.0","kind":"extension","levels":["project"],"glyph":[{"d":"M2 2L4 4"}]}`)
	if _, err := Pack(files, nil); err == nil {
		t.Error("packed an icon given twice")
	}
}
