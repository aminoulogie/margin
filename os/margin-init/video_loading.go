package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"

	"margin-init/fonts"
)

// The player's loading screen (2026-11-08, user: "make the loading ring like the iPhone one,
// info updating all the time, no background behind the text, white on black, 20% smaller, more
// info"): an iOS-style activity indicator (12 tapered spokes, the lit one going round once a
// second) and plain white text straight on the black screen, at 80% of the player's text sizes.
// videoTicker redraws it about 30 times a second, so the clock and the numbers move live.

type loadFonts struct{ status, big, line, small, button font.Face }

var (
	loadFontsOnce sync.Once
	loadFace      *loadFonts
)

// loadingFonts are the player's faces at 80% (apple(): title 44, headline 33, callout 30,
// caption 25).
func loadingFonts() *loadFonts {
	loadFontsOnce.Do(func() {
		k := textScale * 0.8
		loadFace = &loadFonts{
			status: textFace("inter-semibold", fonts.InterSemiBold, true, 33*k),
			big:    textFace("inter-bold", fonts.InterBold, true, 44*k),
			line:   textFace("inter", fonts.InterRegular, false, 30*k),
			small:  textFace("inter", fonts.InterRegular, false, 25*k),
			button: textFace("inter-semibold", fonts.InterSemiBold, true, 33*k),
		}
	})
	return loadFace
}

// spinnerStep is the lit spoke now: iOS's indicator goes round once a second, in 12 steps.
func spinnerStep(now time.Time) int { return int(now.UnixMilli()/83) % 12 }

// ovSpinner draws the iOS activity indicator centred on (cx, cy), outer radius r: 12 rounded
// spokes, the lit one white and the ones behind it fading. halo draws a soft dark edge first,
// for use over a picture.
func ovSpinner(img *image.RGBA, cx, cy, r float64, step int, halo bool) {
	inner, width := r*0.44, r*0.22
	for i := 0; i < 12; i++ {
		behind := (step - i + 12) % 12
		a := 1 - 0.78*float64(behind)/11
		ang := float64(i)*math.Pi/6 - math.Pi/2
		ca, sa := math.Cos(ang), math.Sin(ang)
		x0, y0 := cx+inner*ca, cy+inner*sa
		x1, y1 := cx+(r-width/2)*ca, cy+(r-width/2)*sa
		if halo {
			ovCapsule(img, x0, y0, x1, y1, width+4, color.RGBA{0, 0, 0, uint8(110 * a)})
		}
		v := uint8(255 * a)
		ovCapsule(img, x0, y0, x1, y1, width, color.RGBA{v, v, v, v}) // white, premultiplied
	}
}

// ovCapsule paints a line with round ends, anti-aliased, blending a premultiplied colour.
func ovCapsule(img *image.RGBA, x0, y0, x1, y1, w float64, c color.RGBA) {
	if c.A == 0 {
		return
	}
	hw := w / 2
	b := image.Rect(int(math.Min(x0, x1)-hw-1), int(math.Min(y0, y1)-hw-1),
		int(math.Max(x0, x1)+hw+2), int(math.Max(y0, y1)+hw+2)).Intersect(img.Rect)
	dx, dy := x1-x0, y1-y0
	ll := dx*dx + dy*dy
	for y := b.Min.Y; y < b.Max.Y; y++ {
		py := float64(y) + 0.5
		for x := b.Min.X; x < b.Max.X; x++ {
			px := float64(x) + 0.5
			t := 0.0
			if ll > 0 {
				t = math.Max(0, math.Min(1, ((px-x0)*dx+(py-y0)*dy)/ll))
			}
			d := math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
			cov := math.Min(1, hw+0.5-d)
			if cov <= 0 {
				continue
			}
			o := img.PixOffset(x, y)
			p := img.Pix[o : o+4 : o+4]
			k := 1 - float64(c.A)/255*cov
			p[0] = uint8(float64(c.R)*cov + float64(p[0])*k)
			p[1] = uint8(float64(c.G)*cov + float64(p[1])*k)
			p[2] = uint8(float64(c.B)*cov + float64(p[2])*k)
			p[3] = uint8(float64(c.A)*cov + float64(p[3])*k)
		}
	}
}

// elapsedText is a running clock with hundredths: "0:07.42".
func elapsedText(d time.Duration) string {
	s := d.Seconds()
	return fmt.Sprintf("%d:%05.2f", int(s)/60, math.Mod(s, 60))
}

// loadingInfo is what the loading screen says: a status line, a big figure (or ""), the lines
// under it and a small last line (the file).
func (c *console) loadingInfo(now time.Time) (status, big string, lines []string, small string) {
	v := c.vid
	var st tstatus
	if v.req.job != nil {
		st = engine.status(v.req.job)
	}
	peers := func() string {
		return fmt.Sprintf("%d connected · %d seeders · %d known", st.Peers, st.Seeds, st.All)
	}
	got := func() string {
		if st.Size <= 0 {
			return ""
		}
		left := ""
		if st.Speed > 1024 && st.Done < st.Size {
			left = " · whole file in " + clockText(float64(st.Size-st.Done)/st.Speed)
		}
		return fmt.Sprintf("%s of %s · %s/s%s", humanBytes(float64(st.Done)), humanBytes(float64(st.Size)), humanBytes(st.Speed), left)
	}
	add := func(l string) {
		if l != "" {
			lines = append(lines, l)
		}
	}
	switch v.state {
	case "install":
		status = "Getting the player ready"
		add(v.msg)
	case "torrent":
		switch {
		case v.req.job == nil:
			status = "Starting the torrent"
			add("Opening the torrent client and adding the source")
		case st.State == "resolving":
			status = "Finding the torrent"
			add("Asking trackers and the DHT for peers")
			add(peers())
		default:
			status = "Caching the start of the film"
			if st.Size > 0 {
				big = fmt.Sprintf("%.2f%%", float64(st.Done)/float64(st.Size)*100)
			}
			add(got())
			add(peers())
		}
	case "probing":
		status = "Reading the film's details"
		add(got())
		if v.req.job != nil {
			add(peers())
		}
	case "failed":
		status = "Playback failed"
		add(v.msg)
	default: // starting
		status = "Starting the picture"
		if p := v.probe; p != nil {
			add(fmt.Sprintf("%d×%d · %s · %.2f fps · %s", p.w, p.h, strings.ToUpper(p.videoCodec), p.rateVal, clockText(p.duration)))
			if v.pos > 1 {
				add("From " + clockText(v.pos))
			}
		}
		if v.req.job != nil {
			add(fmt.Sprintf("Buffering · %s/s · %d connected", humanBytes(st.Speed), st.Peers))
		}
	}
	if v.state != "failed" {
		add("Elapsed " + elapsedText(now.Sub(v.loadAt)))
	}
	small = st.Name
	if small == "" && v.req.local != "" {
		small = v.req.local
	}
	return
}

// videoLoadingCard: before the first frame, the spinner and what's happening, in white on the
// black screen (no card behind it).
func (c *console) videoLoadingCard() *ovPart {
	v := c.vid
	lf := loadingFonts()
	W, H := c.s.fbW, c.s.fbH
	now := time.Now()
	status, big, lines, small := c.loadingInfo(now)
	pw := min(W-160, 960)
	textW := pw - 40
	var wrapped []string
	for _, l := range lines {
		wrapped = append(wrapped, wrapText(lf.line, l, textW)...)
	}
	const spin = 36.0 // the spinner's outer radius
	lineH := lf.line.Metrics().Height.Ceil() + 8
	h := int(2*spin) + 40 + 50
	if big != "" {
		h += 62
	}
	h += lineH*len(wrapped) + 40 + 60 + 20
	if small != "" {
		h += 40
	}
	p := newOvPart(pw, h, (W-pw)/2, (H-h)/2)
	img := p.img
	cx := pw / 2
	y := 0
	if v.state != "failed" {
		ovSpinner(img, float64(cx), spin+2, spin, spinnerStep(now), false)
	}
	y = int(2*spin) + 40
	ovTextCenter(img, lf.status, cx, y+20, status)
	y += 50
	if big != "" {
		ovTextCenter(img, lf.big, cx, y+28, big)
		y += 62
	}
	for _, l := range wrapped {
		ovTextCenter(img, lf.line, cx, y+lineH/2, l)
		y += lineH
	}
	if small != "" {
		ovTextCenter(img, lf.small, cx, y+20, clip(lf.small, small, textW))
		y += 40
	}
	y += 30
	bw := 200
	if v.state == "failed" {
		rr := image.Rect(cx-bw-12, y, cx-12, y+60)
		ovRound(img, rr, 30, apBlue)
		ovTextCenter(img, lf.button, (rr.Min.X+rr.Max.X)/2, y+30, "Try Again")
		v.buttons = append(v.buttons, button{"v:play", rr.Add(image.Pt(p.x, p.y))})
		br := image.Rect(cx+12, y, cx+bw+12, y+60)
		ovRound(img, br, 30, color.RGBA{44, 44, 46, 255})
		ovTextCenter(img, lf.button, (br.Min.X+br.Max.X)/2, y+30, "Close")
		v.buttons = append(v.buttons, button{"v:close", br.Add(image.Pt(p.x, p.y)).Inset(-10)})
	} else {
		br := image.Rect(cx-bw/2, y, cx+bw/2, y+60)
		ovRound(img, br, 30, color.RGBA{44, 44, 46, 255})
		ovTextCenter(img, lf.button, cx, y+30, "Close")
		v.buttons = append(v.buttons, button{"v:close", br.Add(image.Pt(p.x, p.y)).Inset(-10)})
	}
	return p.seal()
}

// videoSpinnerPart is the spinner alone over the picture (seeking, starting again), with a dark
// edge so it shows on bright frames.
func (c *console) videoSpinnerPart() *ovPart {
	W, H := c.s.fbW, c.s.fbH
	sp := newOvPart(100, 100, W/2-50, H/2-50)
	ovSpinner(sp.img, 50, 50, 36, spinnerStep(time.Now()), true)
	return sp.seal()
}
