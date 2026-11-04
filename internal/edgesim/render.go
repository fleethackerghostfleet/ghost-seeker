package edgesim

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

// placed is a detection with the pixel-space geometry needed to draw it.
type placed struct {
	catalog.Detection
	x, y, w, h float64
	top        bool
	cx, cy     float64
	l, bw, a   float64
}

func gradient(id string, x1, y1, x2, y2 float64, stops []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<linearGradient id="%s" gradientUnits="userSpaceOnUse" x1="%s" y1="%s" x2="%s" y2="%s">`, id, f(x1), f(y1), f(x2), f(y2))
	for i, c := range stops {
		fmt.Fprintf(&b, `<stop offset="%s" stop-color="%s"/>`, f(float64(i)/float64(len(stops)-1)), c)
	}
	b.WriteString(`</linearGradient>`)
	return b.String()
}

func background(s *svg, scene, mode string, hz float64) {
	ir := mode == "ir"
	switch scene {
	case "harbor", "open":
		tod := pick([]string{"day", "day", "overcast", "dusk"})
		sky := map[string][]string{"day": {"#7fa7c9", "#d6e3ec"}, "overcast": {"#8e9ba6", "#c9d0d5"}, "dusk": {"#41506e", "#d99a6c"}}[tod]
		sea := map[string][]string{"day": {"#3f6a87", "#18324a"}, "overcast": {"#5b6b77", "#27343d"}, "dusk": {"#3d4a60", "#141b26"}}[tod]
		if ir {
			sky, sea = []string{"#1c1c1c", "#383838"}, []string{"#2a2a2a", "#121212"}
		}
		s.raw(`<defs>%s%s</defs>`, gradient("sky", 0, 0, 0, hz*H, sky), gradient("sea", 0, hz*H, 0, H, sea))
		s.rect(0, 0, W, hz*H, "url(#sky)")
		if scene == "harbor" {
			col := "#68757f"
			if ir {
				col = "#4b4b4b"
			} else if tod == "dusk" {
				col = "#2f3442"
			}
			for x := -10.0; x < W; {
				bw, bh := rnd(12, 38), rnd(6, 34)
				s.rect(x, hz*H-bh, bw, bh, col)
				x += bw + rnd(0, 6)
			}
			for i := 0; i < 2; i++ {
				cx := rnd(40, W-40)
				s.rect(cx, hz*H-58, 3, 58, col)
				s.rect(cx-22, hz*H-58, 46, 3, col)
				s.rect(cx+18, hz*H-58, 2, 20, col)
			}
		} else {
			haze := "rgba(255,255,255,.25)"
			if ir {
				haze = "rgba(90,90,90,.4)"
			}
			s.rect(0, hz*H-3, W, 4, haze)
		}
		s.rect(0, hz*H, W, H-hz*H, "url(#sea)")
		var d strings.Builder
		for i := 0; i < 170; i++ {
			y := rnd(hz*H+2, H)
			k := (y - hz*H) / (H - hz*H)
			x, l := rnd(0, W), 4+k*22
			fmt.Fprintf(&d, "M%s %sQ%s %s %s %s", f(x), f(y), f(x+l/2), f(y-1-k*2), f(x+l), f(y))
		}
		wave := "rgba(255,255,255,.13)"
		if ir {
			wave = "rgba(120,120,120,.25)"
		}
		s.path(d.String(), "none", wave, 1)
	case "sky":
		s.raw(`<defs>%s</defs>`, gradient("sky", 0, 0, 0, H, []string{"#4f88c0", "#a9cbe8", "#d7e6f1"}))
		s.rect(0, 0, W, H, "url(#sky)")
		for c := 0; c < 5; c++ {
			cx, cy := rnd(0, W), rnd(20, H*.7)
			for k := 0; k < 6; k++ {
				s.ellipse(cx+rnd(-40, 40), cy+rnd(-8, 8), rnd(20, 45), rnd(8, 16), "rgba(255,255,255,.55)")
			}
		}
		s.rect(0, hz*H, W, H, "#2f4030")
		for x := 0.0; x < W; x += 6 {
			r := rnd(3, 8)
			s.path(fmt.Sprintf("M%s %sA%s %s 0 0 1 %s %sZ", f(x-r), f(hz*H), f(r), f(r), f(x+r), f(hz*H)), "#253425", "none", 0)
		}
		for x := 20.0; x < W; x += 34 {
			s.rect(x, H-6, 3, 2, "#ffd27a")
		}
	case "aerial":
		s.rect(0, 0, W, H, "#1f4a63")
		var d strings.Builder
		for i := 0; i < 260; i++ {
			x, y := rnd(0, W), rnd(0, H)
			fmt.Fprintf(&d, "M%s %sL%s %s", f(x), f(y), f(x+rnd(6, 16)), f(y+rnd(-2, 2)))
		}
		s.path(d.String(), "none", "rgba(255,255,255,.07)", 1)
		if chance(.5) {
			side := chance(.5)
			edge, x := W, W-rnd(40, 110)
			if side {
				edge, x = 0, rnd(40, 110)
			}
			pts := [][2]float64{{edge, 0}}
			y := 0.0
			for y < H {
				pts = append(pts, [2]float64{x, y})
				y += 20
				x += rnd(-14, 14)
			}
			pts = append(pts, [2]float64{x, H}, [2]float64{edge, H})
			var p []string
			for _, q := range pts {
				p = append(p, f(q[0])+","+f(q[1]))
			}
			s.raw(`<polygon points="%s" fill="#8a8466" stroke="rgba(240,240,220,.5)" stroke-width="3"/>`, strings.Join(p, " "))
		}
	case "sat":
		s.rect(0, 0, W, H, "#0b0b0b")
		if chance(.6) {
			pts := [][2]float64{{0, 0}}
			x, y := 0.0, rnd(30, 90)
			pts = append(pts, [2]float64{0, y})
			for x < W {
				x += 24
				y += rnd(-12, 12)
				pts = append(pts, [2]float64{x, y})
			}
			pts = append(pts, [2]float64{W, 0})
			s.poly(pts, "#3a3a3a")
		}
	}
}

func drawSide(s *svg, d placed, mode string) {
	mono := mode == "ir"
	C := func(c string) string {
		if mono {
			if c == "hot" {
				return "#f4f4f4"
			}
			return "#a9a9a9"
		}
		return c
	}
	// hot spots (exhausts, engines, faces) only stand out in thermal
	hot := func(eo string) string {
		if mono {
			return "#f4f4f4"
		}
		return eo
	}
	dir := 1.0
	if chance(.5) {
		dir = -1
	}
	w, h := d.w, d.h
	tx := d.x
	if dir < 0 {
		tx = d.x + w
	}
	glow := ""
	if mono {
		glow = `filter="url(#glow)"`
	}
	s.open(fmt.Sprintf("translate(%s %s) scale(%s 1)", f(tx), f(d.y), f(dir)), glow)
	defer s.close()
	hull := func(top float64, col string) {
		s.poly([][2]float64{{0, h * top}, {w, h*top - h*.04}, {w * .96, h}, {w * .04, h}}, col)
	}
	cls := d.Class
	if (cls == "small boat" || cls == "fishing vessel" || cls == "warship") && d.Attrs["moving"] {
		foam := "rgba(255,255,255,.55)"
		if mono {
			foam = "rgba(200,200,200,.4)"
		}
		s.ellipse(-w*.15, h*.95, w*.45, h*.09, foam)
		s.ellipse(w*.92, h*.93, w*.08, h*.08, foam)
	}
	switch cls {
	case "cargo ship":
		hull(.58, C("#2a2f37"))
		s.rect(w*.04, h*.88, w*.9, h*.12, C("#8b2a2a"))
		cols := []string{"#c0392b", "#2e86c1", "#d68910", "#1e8449", "#7d3c98", "#ba4a00", "#5d6d7e"}
		for x := w * .2; x < w*.86; x += w * .055 {
			for r := 0.0; r < 2; r++ {
				s.rect(x, h*.58-(r+1)*h*.15, w*.05, h*.14, C(pick(cols)))
			}
		}
		s.rect(w*.05, h*.05, w*.11, h*.53, C("#e8e8e8"))
		s.rect(w*.06, h*.12, w*.09, h*.06, C("#1b2633"))
		s.rect(w*.12, 0, w*.03, h*.1, hot("#1b2633"))
	case "tanker":
		hull(.55, C("#3a2b2b"))
		s.rect(w*.04, h*.85, w*.9, h*.15, C("#7a2222"))
		s.path(fmt.Sprintf("M%s %sL%s %s", f(w*.2), f(h*.5), f(w*.9), f(h*.48)), "none", C("#b5b5b5"), 1)
		s.rect(w*.04, h*.08, w*.1, h*.45, C("#efefef"))
		s.rect(w*.06, 0, w*.03, h*.1, hot("#333"))
	case "warship":
		s.poly([][2]float64{{0, h * .55}, {w * .99, h * .48}, {w * .86, h}, {w * .03, h}}, C("#5c6670"))
		s.rect(w*.28, h*.25, w*.3, h*.32, C("#6d7782"))
		s.rect(w*.36, h*.08, w*.12, h*.2, C("#6d7782"))
		s.path(fmt.Sprintf("M%s %sL%s %sM%s %sL%s %s", f(w*.42), f(h*.08), f(w*.42), f(-h*.25), f(w*.36), f(-h*.1), f(w*.48), f(-h*.1)), "none", C("#4a525a"), math.Max(1, w*.012))
		s.rect(w*.72, h*.42, w*.06, h*.1, C("#4f5861"))
		s.rect(w*.75, h*.44, w*.12, h*.03, C("#4f5861"))
		s.rect(w*.2, h*.3, w*.05, h*.2, hot("#4f5861"))
		s.raw(`<text transform="translate(%s %s) scale(%s 1)" font-family="sans-serif" font-weight="bold" font-size="%s" fill="#fff" text-anchor="middle">%d</text>`, f(w*.82), f(h*.8), f(dir), f(math.Max(6, h*.22)), 50+int(rnd(0, 49)))
	case "fishing vessel":
		hull(.55, C("#e9edf0"))
		s.rect(w*.04, h*.82, w*.9, h*.18, C("#2364a4"))
		s.rect(w*.15, h*.22, w*.3, h*.34, C("#f3f3f3"))
		s.rect(w*.18, h*.28, w*.24, h*.1, C("#24323f"))
		s.path(fmt.Sprintf("M%s %sL%s 0L%s %sM%s 0L%s %s", f(w*.62), f(h*.55), f(w*.62), f(w*.95), f(h*.5), f(w*.62), f(w*.3), f(h*.2)), "none", C("#3b3b3b"), 1)
	case "small boat":
		s.poly([][2]float64{{0, h * .45}, {w, h * .5}, {w * .8, h}, {w * .06, h}}, C(pick([]string{"#f2f2f2", "#d9d9d9", "#3a3f46"})))
		s.rect(w*.3, h*.1, w*.25, h*.35, C("#29323b"))
		s.rect(w*.02, h*.4, w*.08, h*.25, hot("#29323b"))
	case "submarine":
		s.path(fmt.Sprintf("M0 %sA%s %s 0 0 1 %s %sZ", f(h*.92), f(w*.5), f(h*.14), f(w), f(h*.92)), C("#151a1f"), "none", 0)
		s.rect(w*.42, h*.15, w*.16, h*.75, C("#151a1f"))
		s.rect(w*.5, -h*.15, w*.02, h*.32, C("#151a1f"))
	case "military aircraft":
		s.ellipse(w*.5, h*.55, w*.5, h*.13, C("#6b737b"))
		s.poly([][2]float64{{w * .35, h * .55}, {w * .62, h * .55}, {w * .42, h * .98}}, C("#59616a"))
		s.poly([][2]float64{{w * .02, h * .5}, {w * .14, h * .5}, {w * .06, h * .05}}, C("#59616a"))
		s.circle(w*.8, h*.45, w*.08, C("#22303b"))
		if mono {
			s.rect(0, h*.48, w*.05, h*.14, "#fff")
		}
	case "civil aircraft":
		s.ellipse(w*.5, h*.55, w*.5, h*.11, C("#f4f4f4"))
		s.poly([][2]float64{{w * .4, h * .58}, {w * .56, h * .58}, {w * .36, h}}, C("#9aa3ab"))
		s.poly([][2]float64{{w * .02, h * .5}, {w * .12, h * .5}, {w * .04, 0}}, C("#1f5fa8"))
		s.rect(w*.4, h*.68, w*.08, h*.08, C("#8c949b"))
		for x := w * .2; x < w*.88; x += w * .04 {
			s.rect(x, h*.5, w*.015, h*.04, C("#3d4b57"))
		}
	case "helicopter":
		body := C("#3f4a3a")
		s.ellipse(w*.62, h*.6, w*.24, h*.24, body)
		s.rect(w*.08, h*.52, w*.4, h*.08, body)
		s.rect(w*.04, h*.36, w*.06, h*.22, body)
		s.path(fmt.Sprintf("M%s %sL%s %sM%s %sL%s %sM%s %sL%s %s", f(w*.15), f(h*.2), f(w), f(h*.2), f(w*.62), f(h*.2), f(w*.62), f(h*.36), f(w*.45), f(h*.95), f(w*.82), f(h*.95)), "none", C("#222"), 1.5)
		s.ellipse(w*.76, h*.55, w*.08, h*.13, C("#9fc3d6"))
	case "drone":
		s.path(fmt.Sprintf("M%s %sL%s %sM%s %sL%s %s", f(w*.1), f(h*.2), f(w*.9), f(h*.8), f(w*.9), f(h*.2), f(w*.1), f(h*.8)), "none", C("#222"), math.Max(1, w*.06))
		s.rect(w*.35, h*.35, w*.3, h*.3, C("#333"))
		for _, p := range [][2]float64{{.1, .2}, {.9, .2}, {.1, .8}, {.9, .8}} {
			s.ring(w*p[0], h*p[1], w*.14, h*.08, C("rgba(40,40,40,.6)"), 1)
		}
	case "person":
		splash := "rgba(255,255,255,.35)"
		if mono {
			splash = "rgba(150,150,150,.4)"
		}
		s.ellipse(w*.5, h*.85, w*.5, h*.15, splash)
		s.rect(w*.25, h*.55, w*.5, h*.3, C("#ff7a1a"))
		s.circle(w*.5, h*.38, w*.2, hot("#d9a07a"))
	case "bird":
		s.path(fmt.Sprintf("M0 %sQ%s 0 %s %sQ%s 0 %s %s", f(h*.3), f(w*.25), f(w*.5), f(h*.6), f(w*.75), f(w), f(h*.3)), "none", C("#1d1d1d"), 1.5)
	case "buoy":
		s.poly([][2]float64{{w * .5, 0}, {w, h * .85}, {0, h * .85}}, C(pick([]string{"#c0392b", "#1e8449"})))
		s.circle(w*.5, h*.05, w*.15, hot("#ffe27a"))
	case "debris":
		for i := 0; i < 4; i++ {
			s.poly([][2]float64{{rnd(0, w*.5), rnd(h*.4, h)}, {rnd(w*.3, w), rnd(h*.4, h)}, {rnd(w*.3, w), rnd(h*.7, h)}}, C(pick([]string{"#6b4f2f", "#8a6a3f", "#e07b2b"})))
		}
	}
}

func drawTop(s *svg, d placed, mode string) {
	L, B := d.l, d.bw
	t := fmt.Sprintf("translate(%s %s) rotate(%s)", f(d.cx), f(d.cy), f(d.a*180/math.Pi))
	if mode == "sar" {
		s.open(t, `filter="url(#glow)"`)
		s.ellipse(0, 0, L/2, B/2, "#f0f0f0")
		s.raw(`<g opacity=".35">`)
		s.rect(-L*.9, -.5, L*1.8, 1.2, "#f0f0f0")
		s.rect(-.5, -L*.5, 1.2, L, "#f0f0f0")
		s.close()
		s.close()
		return
	}
	s.open(t, "")
	defer s.close()
	cls := d.Class
	if d.Attrs["moving"] && cls != "person" && cls != "debris" {
		s.poly([][2]float64{{-L / 2, 0}, {-L * 1.8, -B * 1.6}, {-L * 1.8, B * 1.6}}, "rgba(255,255,255,.45)")
		s.rect(-L*1.1, -B*.25, L*.6, B*.5, "rgba(255,255,255,.7)")
	}
	switch cls {
	case "submarine":
		s.raw(`<ellipse cx="0" cy="0" rx="%s" ry="%s" fill="#0d1b24" opacity=".7"/>`, f(L/2), f(B/2.4))
		s.rect(-L*.05, -B*.15, L*.12, B*.3, "#091218")
	case "person":
		s.circle(0, 0, L*.6, "rgba(255,255,255,.35)")
		s.circle(0, 0, L*.32, "#ff7a1a")
	case "debris":
		for i := 0; i < 5; i++ {
			s.poly([][2]float64{{rnd(-L/2, L/2), rnd(-B, B)}, {rnd(-L/2, L/2), rnd(-B, B)}, {rnd(-L/2, L/2), rnd(-B, B)}}, pick([]string{"#6b4f2f", "#8a6a3f", "#e07b2b"}))
		}
	default:
		col := map[string]string{"warship": "#6a737c", "cargo ship": "#30353c", "tanker": "#5a2a2a", "fishing vessel": "#e8ecef", "small boat": "#f4f4f4"}[cls]
		if col == "" {
			col = "#888"
		}
		s.poly([][2]float64{{-L / 2, -B / 2}, {L * .3, -B / 2}, {L / 2, 0}, {L * .3, B / 2}, {-L / 2, B / 2}}, col)
		switch cls {
		case "cargo ship":
			for x := -L * .3; x < L*.25; x += L * .07 {
				s.rect(x, -B*.38, L*.06, B*.76, pick([]string{"#c0392b", "#2e86c1", "#d68910", "#1e8449"}))
			}
			s.rect(-L*.45, -B*.4, L*.1, B*.8, "#eee")
		case "tanker":
			s.rect(-L*.3, -1, L*.6, 2, "#8a8a8a")
			s.rect(-L*.46, -B*.4, L*.1, B*.8, "#eee")
		case "warship":
			s.rect(-L*.15, -B*.3, L*.25, B*.6, "#7f8892")
			s.circle(L*.3, 0, B*.15, "#555")
		case "fishing vessel", "small boat":
			s.rect(-L*.15, -B*.3, L*.25, B*.6, "#2a3743")
		}
	}
}

// Render draws a synthetic sensor frame as SVG.
func Render(src catalog.Source, dets []placed, ts time.Time, frame int, hz float64) []byte {
	s := &svg{}
	s.raw(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, int(W), int(H), int(W), int(H))
	grain := map[string]float64{"sar": .32, "ir": .14, "eo": .08}[src.Mode]
	s.raw(`<defs><filter id="gray"><feColorMatrix type="saturate" values="0"/></filter>`)
	s.raw(`<filter id="glow" x="-50%%" y="-50%%" width="200%%" height="200%%"><feGaussianBlur stdDeviation="2.5" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>`)
	s.raw(`<filter id="grain" x="0" y="0" width="100%%" height="100%%"><feTurbulence type="fractalNoise" baseFrequency=".85" numOctaves="2" seed="%d"/><feColorMatrix type="saturate" values="0"/></filter>`, int(rnd(0, 9999)))
	s.raw(`<radialGradient id="vig" gradientUnits="userSpaceOnUse" cx="%s" cy="%s" r="%s"><stop offset=".44" stop-color="#000" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity=".35"/></radialGradient></defs>`, f(W/2), f(H/2), f(H*.9))
	gray := ""
	if src.Mode == "ir" || src.Mode == "sar" {
		gray = `filter="url(#gray)"`
	}
	s.open("", gray)
	background(s, src.Scene, src.Mode, hz)
	order := append([]placed(nil), dets...)
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && order[j].y+order[j].h < order[j-1].y+order[j-1].h; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	for _, d := range order {
		if d.top {
			drawTop(s, d, src.Mode)
		} else {
			drawSide(s, d, src.Mode)
		}
	}
	s.close()
	s.raw(`<rect width="%d" height="%d" filter="url(#grain)" opacity="%s" style="mix-blend-mode:overlay"/>`, int(W), int(H), f(grain*2))
	s.rect(0, 0, W, H, "url(#vig)")
	s.rect(0, 0, W, 15, "rgba(0,0,0,.45)")
	font := `font-family="ui-monospace,Menlo,Consolas,monospace" font-size="10" fill="#e8f1f7"`
	s.raw(`<text x="6" y="11" %s>%s  %sZ</text>`, font, esc(src.ID), ts.UTC().Format("2006-01-02 15:04:05"))
	s.raw(`<text x="%s" y="11" text-anchor="end" %s>FRM %06d  %s</text>`, f(W-6), font, frame, strings.ToUpper(src.Mode))
	s.raw(`</svg>`)
	return []byte(s.b.String())
}
