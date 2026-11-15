package bundle

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Glyph is a plugin's own icon, drawn by the plugin's author and shipped in
// its manifest, so no two plugins share one and Enplace's own code holds
// none of them. It is data, not an image: a few layers on a 24 by 24 grid
// that the web app and the Apple app each draw natively, in the colours of
// whatever theme is on.
type Glyph []GlyphLayer

// GlyphLayer is one shape of a glyph.
type GlyphLayer struct {
	// D is the shape, as SVG path data kept to five absolute commands so
	// every platform can draw it in a few lines: M, L, C, Q and Z, with
	// coordinates from 0 to 24.
	D string `json:"d"`
	// Paint says how it's drawn. The glyph carries no colours of its own:
	//   "line" (the default): a 2 wide stroke with round caps and joins, in
	//     the colour of the text around it
	//   "solid": filled in that same colour
	//   "soft": filled with the plugin's colour, see-through, behind the rest
	Paint string `json:"paint,omitempty"`
}

// Glyph limits.
const (
	MaxGlyphLayers = 8
	MaxGlyphPath   = 1200
)

// glyphArgs is how many numbers each command takes.
var glyphArgs = map[byte]int{'M': 2, 'L': 2, 'Q': 4, 'C': 6, 'Z': 0}

// Check validates a glyph. An empty glyph is fine: the plugin has none.
func (g Glyph) Check() error {
	if len(g) > MaxGlyphLayers {
		return fmt.Errorf("a glyph has at most %d layers", MaxGlyphLayers)
	}
	for i, l := range g {
		switch l.Paint {
		case "", "line", "solid", "soft":
		default:
			return fmt.Errorf(`glyph layer %d: paint must be "line", "solid" or "soft"`, i+1)
		}
		if err := checkGlyphPath(l.D); err != nil {
			return fmt.Errorf("glyph layer %d: %w", i+1, err)
		}
	}
	return nil
}

func checkGlyphPath(d string) error {
	if d == "" || len(d) > MaxGlyphPath {
		return fmt.Errorf("its path must be 1-%d characters", MaxGlyphPath)
	}
	// Split into commands and numbers: "M12 2.5L20 7" and "M 12,2.5 L 20,7"
	// both read the same.
	var toks []string
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		switch {
		case c == ' ' || c == ',':
			flush()
		case c >= '0' && c <= '9' || c == '.':
			cur.WriteByte(c)
		default:
			if _, ok := glyphArgs[c]; !ok {
				return fmt.Errorf("its path may only use M, L, C, Q and Z with numbers from 0 to 24 (found %q)", string(c))
			}
			flush()
			toks = append(toks, string(c))
		}
	}
	flush()
	if len(toks) == 0 || toks[0] != "M" {
		return errors.New("its path must start with M")
	}
	for i := 0; i < len(toks); {
		n, ok := glyphArgs[toks[i][0]]
		if !ok || len(toks[i]) != 1 {
			return errors.New("its path has numbers without a command")
		}
		i++
		for j := 0; j < n; j, i = j+1, i+1 {
			if i >= len(toks) {
				return errors.New("its path ends mid-command")
			}
			v, err := strconv.ParseFloat(toks[i], 64)
			if err != nil || v < 0 || v > 24 {
				return fmt.Errorf("its path may only use numbers from 0 to 24 (found %q)", toks[i])
			}
		}
	}
	return nil
}
