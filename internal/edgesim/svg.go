package edgesim

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
)

const (
	W = 480.0
	H = 300.0
)

func rnd(a, b float64) float64 { return a + rand.Float64()*(b-a) }
func chance(p float64) bool    { return rand.Float64() < p }
func pick[T any](a []T) T      { return a[rand.IntN(len(a))] }

func wpick(keys []string, weights []float64) string {
	t := 0.0
	for _, w := range weights {
		t += w
	}
	r := rand.Float64() * t
	for i, w := range weights {
		if r -= w; r <= 0 {
			return keys[i]
		}
	}
	return keys[0]
}

// svg is a tiny SVG writer for synthetic sensor frames.
type svg struct{ b strings.Builder }

func f(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", v), "0"), ".")
}

func (s *svg) raw(format string, a ...any) { fmt.Fprintf(&s.b, format, a...) }

func (s *svg) rect(x, y, w, h float64, fill string) {
	s.raw(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f(x), f(y), f(math.Max(0, w)), f(math.Max(0, h)), fill)
}

func (s *svg) ellipse(cx, cy, rx, ry float64, fill string) {
	s.raw(`<ellipse cx="%s" cy="%s" rx="%s" ry="%s" fill="%s"/>`, f(cx), f(cy), f(math.Abs(rx)), f(math.Abs(ry)), fill)
}

func (s *svg) ring(cx, cy, rx, ry float64, stroke string, width float64) {
	s.raw(`<ellipse cx="%s" cy="%s" rx="%s" ry="%s" fill="none" stroke="%s" stroke-width="%s"/>`, f(cx), f(cy), f(math.Abs(rx)), f(math.Abs(ry)), stroke, f(width))
}

func (s *svg) circle(cx, cy, r float64, fill string) { s.ellipse(cx, cy, r, r, fill) }

func (s *svg) poly(pts [][2]float64, fill string) {
	var p []string
	for _, q := range pts {
		p = append(p, f(q[0])+","+f(q[1]))
	}
	s.raw(`<polygon points="%s" fill="%s"/>`, strings.Join(p, " "), fill)
}

func (s *svg) path(d, fill, stroke string, width float64) {
	s.raw(`<path d="%s" fill="%s" stroke="%s" stroke-width="%s"/>`, d, fill, stroke, f(width))
}

func (s *svg) open(transform, attrs string) { s.raw(`<g transform="%s" %s>`, transform, attrs) }
func (s *svg) close()                       { s.b.WriteString(`</g>`) }

func esc(t string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(t)
}
