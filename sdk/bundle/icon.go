package bundle

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"strconv"
	"strings"
)

// Icon files. A plugin draws its icon as an SVG file, the way it comes out
// of a drawing tool, and packing turns it into the plugin's Glyph:
//
//	icon.svg           the plugin's icon
//	icons/<page>.svg   a page's own icon, by the page's key
//
// The SVG is never served. Enplace only ever draws the glyph it becomes,
// so a plugin's icon can't carry scripts, links or pictures.
const (
	IconFile = "icon.svg"
	IconsDir = "icons/"
)

// iconsFromFiles fills in the manifest's glyphs from its icon files.
func iconsFromFiles(m *Manifest, files map[string][]byte) error {
	if svg, ok := files[IconFile]; ok {
		if len(m.Glyph) > 0 {
			return errors.New("the plugin's icon is in both icon.svg and the manifest's glyph: keep icon.svg")
		}
		g, err := ParseIcon(svg)
		if err != nil {
			return fmt.Errorf("%s: %w", IconFile, err)
		}
		m.Glyph = g
	}
	for name, svg := range files {
		key, ok := strings.CutPrefix(name, IconsDir)
		if !ok {
			continue
		}
		key, ok = strings.CutSuffix(key, ".svg")
		i := pageIndex(m.Contributes.Pages, key)
		if !ok || i < 0 {
			return fmt.Errorf("%s: name it icons/<page key>.svg after one of the plugin's pages", name)
		}
		p := &m.Contributes.Pages[i]
		if len(p.Glyph) > 0 {
			return fmt.Errorf("page %s's icon is in both %s and the manifest's glyph: keep %s", key, name, name)
		}
		g, err := ParseIcon(svg)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		p.Glyph = g
	}
	return nil
}

func pageIndex(pages []Page, key string) int {
	for i, p := range pages {
		if p.Key == key {
			return i
		}
	}
	return -1
}

// ParseIcon turns an icon's SVG into a Glyph. The SVG has a 0 0 24 24
// viewBox and up to 8 shapes: path, circle, ellipse, rect, line, polyline
// or polygon, with no transforms. Each shape becomes a layer, painted by
// its class ("line", "solid" or "soft") or else by how it's drawn: a
// stroke is a line, a see-through fill is soft, any other fill is solid.
// Colours are dropped, since a glyph takes them from the theme.
func ParseIcon(svg []byte) (Glyph, error) {
	dec := xml.NewDecoder(bytes.NewReader(svg))
	dec.Strict = true
	var g Glyph
	root := true
	// paints are the fill and stroke each open element passes down, as SVG
	// does: a stroke set on <svg> or a <g> holds for the shapes inside.
	var paints []map[string]string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not readable SVG: %w", err)
		}
		if _, ok := tok.(xml.EndElement); ok && len(paints) > 0 {
			paints = paints[:len(paints)-1]
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := map[string]string{}
		for _, a := range el.Attr {
			attrs[a.Name.Local] = a.Value
		}
		paint := map[string]string{}
		if len(paints) > 0 {
			maps.Copy(paint, paints[len(paints)-1])
		}
		maps.Copy(paint, presentation(attrs))
		paints = append(paints, paint)
		if root {
			if el.Name.Local != "svg" {
				return nil, errors.New("not an SVG file")
			}
			if vb := strings.Join(strings.Fields(strings.ReplaceAll(attrs["viewBox"], ",", " ")), " "); vb != "0 0 24 24" {
				return nil, errors.New(`its viewBox must be "0 0 24 24"`)
			}
			root = false
			continue
		}
		if _, ok := attrs["transform"]; ok {
			return nil, fmt.Errorf("<%s> has a transform: flatten it in your drawing tool", el.Name.Local)
		}
		d, err := shapePath(el.Name.Local, attrs)
		if err != nil {
			return nil, err
		}
		if d == "" {
			continue
		}
		layer, err := glyphLayer(d, attrs["class"], paint)
		if err != nil {
			return nil, fmt.Errorf("<%s>: %w", el.Name.Local, err)
		}
		g = append(g, layer)
	}
	if root {
		return nil, errors.New("not an SVG file")
	}
	if len(g) == 0 {
		return nil, errors.New("it draws nothing")
	}
	if err := g.Check(); err != nil {
		return nil, err
	}
	return g, nil
}

// shapePath is a shape's outline as path data, or "" for an element that
// draws nothing itself, like a group or a title.
func shapePath(name string, a map[string]string) (string, error) {
	num := func(k string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(a[k], "px"), 64)
		return v
	}
	switch name {
	case "g", "title", "desc", "defs":
		return "", nil
	case "path":
		return a["d"], nil
	case "line":
		return fmt.Sprintf("M%g %g L%g %g", num("x1"), num("y1"), num("x2"), num("y2")), nil
	case "polyline", "polygon":
		pts := strings.Fields(strings.ReplaceAll(a["points"], ",", " "))
		if len(pts) < 4 || len(pts)%2 != 0 {
			return "", fmt.Errorf("<%s> needs pairs of points", name)
		}
		d := "M" + strings.Join(pts, " ")
		if name == "polygon" {
			d += "Z"
		}
		return d, nil
	case "circle":
		return ellipsePath(num("cx"), num("cy"), num("r"), num("r")), nil
	case "ellipse":
		return ellipsePath(num("cx"), num("cy"), num("rx"), num("ry")), nil
	case "rect":
		x, y, w, h := num("x"), num("y"), num("width"), num("height")
		rx, ry := num("rx"), num("ry")
		if _, ok := a["ry"]; !ok {
			ry = rx
		}
		if _, ok := a["rx"]; !ok {
			rx = ry
		}
		rx, ry = math.Min(rx, w/2), math.Min(ry, h/2)
		if rx <= 0 || ry <= 0 {
			return fmt.Sprintf("M%g %g H%g V%g H%g Z", x, y, x+w, y+h, x), nil
		}
		return fmt.Sprintf("M%g %g H%g A%g %g 0 0 1 %g %g V%g A%g %g 0 0 1 %g %g H%g A%g %g 0 0 1 %g %g V%g A%g %g 0 0 1 %g %g Z",
			x+rx, y, x+w-rx, rx, ry, x+w, y+ry, y+h-ry, rx, ry, x+w-rx, y+h, x+rx, rx, ry, x, y+h-ry, y+ry, rx, ry, x+rx, y), nil
	}
	return "", fmt.Errorf("<%s> isn't a shape an icon can use: use paths and simple shapes", name)
}

func ellipsePath(cx, cy, rx, ry float64) string {
	return fmt.Sprintf("M%g %g A%g %g 0 1 1 %g %g A%g %g 0 1 1 %g %g Z", cx-rx, cy, rx, ry, cx+rx, cy, rx, ry, cx-rx, cy)
}

// presentation is how an element says it's painted: its fill, stroke and
// opacities, from its attributes or its style.
func presentation(a map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"fill", "stroke", "fill-opacity", "opacity"} {
		if v, ok := a[k]; ok {
			out[k] = v
		}
	}
	for _, decl := range strings.Split(a["style"], ";") {
		if k, v, ok := strings.Cut(decl, ":"); ok {
			k = strings.TrimSpace(k)
			switch k {
			case "fill", "stroke", "fill-opacity", "opacity":
				out[k] = strings.TrimSpace(v)
			}
		}
	}
	return out
}

// glyphLayer is one shape as a layer: its path in glyph form, and its paint
// from its class, or else from how it's painted.
func glyphLayer(d, class string, look map[string]string) (GlyphLayer, error) {
	path, err := glyphPath(d)
	if err != nil {
		return GlyphLayer{}, err
	}
	get := func(k string) string { return look[k] }
	paint := ""
	for _, c := range strings.Fields(class) {
		switch c {
		case "line", "solid", "soft":
			paint = c
		}
	}
	if paint == "" {
		fill, stroke := get("fill"), get("stroke")
		see := func(k string) bool {
			v, err := strconv.ParseFloat(get(k), 64)
			return err == nil && v < 1
		}
		switch {
		case stroke != "" && stroke != "none":
			paint = "line"
		case fill == "none":
			paint = "line"
		case see("fill-opacity") || see("opacity"):
			paint = "soft"
		default:
			paint = "solid"
		}
	}
	if paint == "line" {
		paint = ""
	}
	return GlyphLayer{D: path, Paint: paint}, nil
}

// glyphPath rewrites any SVG path data with the five absolute commands a
// glyph uses: M, L, C, Q and Z. Arcs become curves.
func glyphPath(d string) (string, error) {
	p := pathReader{s: d}
	var out strings.Builder
	var x, y, sx, sy float64 // pen and subpath start
	var cx, cy float64       // last control point, for S and T
	var prev byte
	put := func(cmd byte, pts ...float64) {
		out.WriteByte(cmd)
		for i, v := range pts {
			if i > 0 {
				out.WriteByte(' ')
			}
			out.WriteString(glyphNum(v))
		}
	}
	for {
		cmd, ok := p.command()
		if !ok {
			break
		}
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		ox, oy := 0.0, 0.0
		if rel {
			ox, oy = x, y
		}
		first := true
		for first || p.more() {
			if up == 'Z' {
				put('Z')
				x, y = sx, sy
				prev = 'Z'
				break
			}
			first = false
			switch up {
			case 'M', 'L':
				nx, ny, err := p.pair()
				if err != nil {
					return "", err
				}
				x, y = ox+nx, oy+ny
				if up == 'M' {
					put('M', x, y)
					sx, sy = x, y
					up = 'L' // the pairs after a move are lines
				} else {
					put('L', x, y)
				}
			case 'H':
				v, err := p.num()
				if err != nil {
					return "", err
				}
				x = ox + v
				put('L', x, y)
			case 'V':
				v, err := p.num()
				if err != nil {
					return "", err
				}
				y = oy + v
				put('L', x, y)
			case 'C', 'S':
				var x1, y1 float64
				if up == 'C' {
					a, b, err := p.pair()
					if err != nil {
						return "", err
					}
					x1, y1 = ox+a, oy+b
				} else if prev == 'C' || prev == 'S' {
					x1, y1 = 2*x-cx, 2*y-cy
				} else {
					x1, y1 = x, y
				}
				x2, y2, err := p.pair()
				if err != nil {
					return "", err
				}
				ex, ey, err := p.pair()
				if err != nil {
					return "", err
				}
				cx, cy = ox+x2, oy+y2
				x, y = ox+ex, oy+ey
				put('C', x1, y1, cx, cy, x, y)
			case 'Q', 'T':
				if up == 'Q' {
					a, b, err := p.pair()
					if err != nil {
						return "", err
					}
					cx, cy = ox+a, oy+b
				} else if prev == 'Q' || prev == 'T' {
					cx, cy = 2*x-cx, 2*y-cy
				} else {
					cx, cy = x, y
				}
				ex, ey, err := p.pair()
				if err != nil {
					return "", err
				}
				x, y = ox+ex, oy+ey
				put('Q', cx, cy, x, y)
			case 'A':
				rx, ry, err := p.pair()
				if err != nil {
					return "", err
				}
				rot, err := p.num()
				if err != nil {
					return "", err
				}
				large, err1 := p.flag()
				sweep, err2 := p.flag()
				if err := errors.Join(err1, err2); err != nil {
					return "", err
				}
				ex, ey, err := p.pair()
				if err != nil {
					return "", err
				}
				ex, ey = ox+ex, oy+ey
				for _, c := range arcCurves(x, y, rx, ry, rot, large, sweep, ex, ey) {
					put('C', c[:]...)
				}
				x, y = ex, ey
			default:
				return "", fmt.Errorf("its path uses %q, which isn't SVG path data", string(cmd))
			}
			prev = up
			if rel {
				ox, oy = x, y
			}
		}
	}
	if p.err != nil {
		return "", p.err
	}
	return out.String(), nil
}

// glyphNum writes a coordinate to two decimals, pulling in anything that
// strays a hair outside the grid from rounding.
func glyphNum(v float64) string {
	v = math.Round(v*100) / 100
	if v < 0 && v > -0.05 {
		v = 0
	}
	if v > 24 && v < 24.05 {
		v = 24
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// arcCurves approximates an SVG arc with cubic curves of at most a quarter
// turn each (SVG 1.1, appendix F.6).
func arcCurves(x1, y1, rx, ry, rot float64, large, sweep bool, x2, y2 float64) [][6]float64 {
	if x1 == x2 && y1 == y2 {
		return nil
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		return [][6]float64{{x1, y1, x2, y2, x2, y2}}
	}
	phi := rot * math.Pi / 180
	sin, cos := math.Sincos(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	px, py := cos*dx+sin*dy, -sin*dx+cos*dy
	if l := px*px/(rx*rx) + py*py/(ry*ry); l > 1 {
		rx, ry = rx*math.Sqrt(l), ry*math.Sqrt(l)
	}
	num := rx*rx*ry*ry - rx*rx*py*py - ry*ry*px*px
	den := rx*rx*py*py + ry*ry*px*px
	k := math.Sqrt(math.Max(0, num/den))
	if large == sweep {
		k = -k
	}
	cpx, cpy := k*rx*py/ry, -k*ry*px/rx
	cx := cos*cpx - sin*cpy + (x1+x2)/2
	cy := sin*cpx + cos*cpy + (y1+y2)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	}
	start := angle(1, 0, (px-cpx)/rx, (py-cpy)/ry)
	delta := angle((px-cpx)/rx, (py-cpy)/ry, (-px-cpx)/rx, (-py-cpy)/ry)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	} else if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	step := delta / float64(n)
	t := 4.0 / 3 * math.Tan(step/4)
	point := func(a float64) (float64, float64) {
		ex, ey := rx*math.Cos(a), ry*math.Sin(a)
		return cos*ex - sin*ey + cx, sin*ex + cos*ey + cy
	}
	deriv := func(a float64) (float64, float64) {
		ex, ey := -rx*math.Sin(a), ry*math.Cos(a)
		return cos*ex - sin*ey, sin*ex + cos*ey
	}
	out := make([][6]float64, 0, n)
	a := start
	sxp, syp := x1, y1
	for i := 0; i < n; i++ {
		b := a + step
		ex, ey := point(b)
		if i == n-1 {
			ex, ey = x2, y2
		}
		d1x, d1y := deriv(a)
		d2x, d2y := deriv(b)
		out = append(out, [6]float64{sxp + t*d1x, syp + t*d1y, ex - t*d2x, ey - t*d2y, ex, ey})
		sxp, syp, a = ex, ey, b
	}
	return out
}

// pathReader reads SVG path data one command or number at a time.
type pathReader struct {
	s   string
	i   int
	err error
}

func (p *pathReader) skip() {
	for p.i < len(p.s) && strings.IndexByte(" \t\r\n,", p.s[p.i]) >= 0 {
		p.i++
	}
}

func (p *pathReader) command() (byte, bool) {
	p.skip()
	if p.err != nil || p.i >= len(p.s) {
		return 0, false
	}
	c := p.s[p.i]
	if strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", c) < 0 {
		p.err = fmt.Errorf("its path data doesn't start a command at %q", p.s[p.i:min(p.i+8, len(p.s))])
		return 0, false
	}
	p.i++
	return c, true
}

// more says whether a number comes next: a command's numbers repeat.
func (p *pathReader) more() bool {
	p.skip()
	return p.i < len(p.s) && strings.IndexByte("+-.0123456789", p.s[p.i]) >= 0
}

func (p *pathReader) num() (float64, error) {
	p.skip()
	start := p.i
	if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
		p.i++
	}
	dot, exp := false, false
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && !dot && !exp:
			dot = true
		case (c == 'e' || c == 'E') && !exp:
			exp = true
			if p.i+1 < len(p.s) && (p.s[p.i+1] == '+' || p.s[p.i+1] == '-') {
				p.i++
			}
		default:
			goto done
		}
		p.i++
	}
done:
	v, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		return 0, errors.New("its path data ends mid-command or has a number it can't read")
	}
	return v, nil
}

func (p *pathReader) pair() (float64, float64, error) {
	a, err := p.num()
	if err != nil {
		return 0, 0, err
	}
	b, err := p.num()
	return a, b, err
}

// flag reads an arc flag, which may run straight into what follows ("01").
func (p *pathReader) flag() (bool, error) {
	p.skip()
	if p.i < len(p.s) && (p.s[p.i] == '0' || p.s[p.i] == '1') {
		p.i++
		return p.s[p.i-1] == '1', nil
	}
	return false, errors.New("an arc in its path has a flag that isn't 0 or 1")
}
