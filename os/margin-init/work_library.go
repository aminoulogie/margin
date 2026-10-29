package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"margin-init/ui"
)

// Training as the phone has it: the exercise library (Soma's BASE_EXERCISE_DB with its
// photos, src/lib/soma/data.ts and public/exercises, copied here), browsed by muscle; an
// exercise's own page; and today's workout, logged as it happens: a split loaded or
// exercises picked from the library, each set's weight and reps changed in place, ticked
// when done (which starts the rest timer), more sets, and Finish.

//go:embed exercises
var exerciseFS embed.FS

type libExercise struct {
	Name       string   `json:"name"`
	Muscle     string   `json:"muscle"`
	SubTarget  string   `json:"subTarget"`
	TargetKeys []string `json:"targetKeys"`
	Position   string   `json:"position"`
	Risk       string   `json:"risk"`
	Tier       string   `json:"tier"`
	IsAxial    bool     `json:"isAxial"`
	IsBW       bool     `json:"isBW"`
	UsesBar    bool     `json:"usesBar"`
	BarWeight  float64  `json:"barWeight"`
	Img        string   `json:"img"`
}

type libSplit struct {
	Name      string   `json:"name"`
	Exercises []string `json:"exercises"`
}

var exLib struct {
	once   sync.Once
	ex     []libExercise
	splits []libSplit
	byKey  map[string]int
	mu     sync.Mutex
	photos map[string]*image.RGBA // name@WxH, drawn to fill
}

func libLoad() {
	exLib.once.Do(func() {
		b, _ := exerciseFS.ReadFile("exercises/exercises.json")
		var f struct {
			Exercises []libExercise `json:"exercises"`
			Splits    []libSplit    `json:"splits"`
		}
		json.Unmarshal(b, &f)
		exLib.ex, exLib.splits = f.Exercises, f.Splits
		exLib.byKey = map[string]int{}
		for i, e := range exLib.ex {
			exLib.byKey[exerciseKey(e.Name)] = i
		}
		exLib.photos = map[string]*image.RGBA{}
	})
}

func libExercises() []libExercise { libLoad(); return exLib.ex }
func libSplits() []libSplit       { libLoad(); return exLib.splits }

// libFind is the library's exercise of that name (Soma's names, or close to them).
func libFind(name string) (libExercise, bool) {
	libLoad()
	if i, ok := exLib.byKey[exerciseKey(name)]; ok {
		return exLib.ex[i], true
	}
	return libExercise{}, false
}

// libMuscles are the library's groups, in Soma's order, for the chips.
var libMuscles = []string{"All", "Chest", "Back", "Shoulders", "Biceps", "Triceps", "Legs", "Abs", "Neck"}

// exPhoto is an exercise's photo filling w x h (cropped to fit), or nil if it has none.
func exPhoto(name string, w, h int) *image.RGBA {
	e, ok := libFind(name)
	if !ok || e.Img == "" || w <= 0 || h <= 0 {
		return nil
	}
	key := fmt.Sprintf("%s@%dx%d", e.Img, w, h)
	exLib.mu.Lock()
	defer exLib.mu.Unlock()
	if p, ok := exLib.photos[key]; ok {
		return p
	}
	var out *image.RGBA
	if b, err := exerciseFS.ReadFile("exercises/" + e.Img); err == nil {
		if src, err := jpeg.Decode(bytes.NewReader(b)); err == nil {
			sb := src.Bounds()
			// The middle of the photo at the box's shape.
			crop := sb
			if sb.Dx()*h > sb.Dy()*w {
				cw := sb.Dy() * w / h
				crop = image.Rect(sb.Min.X+(sb.Dx()-cw)/2, sb.Min.Y, sb.Min.X+(sb.Dx()-cw)/2+cw, sb.Max.Y)
			} else {
				ch := sb.Dx() * h / w
				top := sb.Min.Y + (sb.Dy()-ch)/3 // a little above the middle: heads, not feet
				crop = image.Rect(sb.Min.X, top, sb.Max.X, top+ch)
			}
			out = image.NewRGBA(image.Rect(0, 0, w, h))
			xdraw.CatmullRom.Scale(out, out.Rect, src, crop, xdraw.Src, nil)
		}
	}
	if len(exLib.photos) > 200 {
		clear(exLib.photos)
	}
	exLib.photos[key] = out
	return out
}

// muscleColour is a muscle group's tint, for an exercise with no photo.
func muscleColour(m string) color.RGBA {
	switch m {
	case "Chest":
		return rgb(0xf06a8a)
	case "Back":
		return rgb(0x5b8cff)
	case "Shoulders":
		return rgb(0xf0a63c)
	case "Biceps", "Triceps":
		return rgb(0xa98bfa)
	case "Legs":
		return rgb(0x3ecf8e)
	case "Abs":
		return rgb(0xe3f25a)
	}
	return rgb(0x8a8f9a)
}

// exFace draws an exercise's photo into r with rounded corners, or a tile with its
// initials and a dumbbell when it has none.
func exFace(img *image.RGBA, r image.Rectangle, name string, rad int) {
	if p := exPhoto(name, r.Dx(), r.Dy()); p != nil {
		for y := 0; y < r.Dy(); y++ {
			row := roundedRow(image.Rect(0, 0, r.Dx(), r.Dy()), rad, y)
			if row.Empty() || r.Min.Y+y < img.Rect.Min.Y || r.Min.Y+y >= img.Rect.Max.Y {
				continue
			}
			x0, x1 := max(row.Min.X, img.Rect.Min.X-r.Min.X), min(row.Max.X, img.Rect.Max.X-r.Min.X)
			if x0 >= x1 {
				continue
			}
			copy(img.Pix[img.PixOffset(r.Min.X+x0, r.Min.Y+y):][:4*(x1-x0)], p.Pix[p.PixOffset(x0, y):][:4*(x1-x0)])
		}
		return
	}
	e, _ := libFind(name)
	col := muscleColour(e.Muscle)
	ui.RoundRect(img, r, rad, blend(wkBG, col, 0.22))
	ini := wkInitials(name)
	if r.Dy() > 140 {
		lucideIcon(img, "dumbbell", (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2-22, r.Dy()/4, blend(col, rgb(0xffffff), 0.2))
		apTextCenter(img, wkFont("bold", float64(r.Dy())/7), (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2+r.Dy()/5, blend(col, rgb(0xffffff), 0.2), ini)
		return
	}
	apTextCenter(img, wkFont("bold", float64(r.Dy())*0.36), (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, blend(col, rgb(0xffffff), 0.2), ini)
}

// tierColour is Soma's: S gold, A steel blue.
func tierColour(t string) (color.RGBA, bool) {
	t = strings.ToLower(t)
	switch {
	case strings.HasPrefix(t, "s"):
		return rgb(0xf5c542), true
	case strings.HasPrefix(t, "a"):
		return rgb(0x7fb4ff), true
	}
	return color.RGBA{}, false
}

// libObj is a session exercise for a library movement: what it was last time if it was
// done before, else the library's own description of it, with no sets yet.
func libObj(name string) obj {
	n := newExercise(name)
	if e, ok := libFind(name); ok && str(n["muscle"]) == "Other" {
		var tk []any
		for _, t := range e.TargetKeys {
			tk = append(tk, t)
		}
		n = obj{"name": e.Name, "muscle": e.Muscle, "subTarget": e.SubTarget, "targetKeys": tk, "position": e.Position,
			"risk": e.Risk, "tier": e.Tier, "isAxial": e.IsAxial, "isBW": e.IsBW, "usesBar": e.UsesBar,
			"barWeight": e.BarWeight, "supersetGroup": "", "sets": []any{}}
	}
	return n
}

// --- today's workout -------------------------------------------------------------------------

// workoutEnsure is today's session, begun (named split) if there's none yet.
func workoutEnsure(split string) string {
	today := somaToday()
	if somaObj("history/"+today) == nil {
		if split == "" {
			split = "Workout"
		}
		somaPut("history/"+today, obj{"timestamp": nowMs(), "split": split, "durationFormatted": "0:00", "caloriesBurned": 0,
			"totalVol": 0, "totalSets": 0, "axialVol": 0, "exercises": []any{}, "muscles": obj{}, "startedAt": nowMs()})
	}
	return today
}

// workoutAdd puts an exercise in today's workout with its first sets (not ticked yet).
func workoutAdd(name string, sets int) {
	date := workoutEnsure("")
	patchSession(date, func(o obj) {
		exs, _ := o["exercises"].([]any)
		o["exercises"] = append(exs, libObj(name))
	})
	exs, _ := somaObj("history/" + date)["exercises"].([]any)
	xi := len(exs) - 1
	for range max(sets, 1) {
		addSet(date, xi)
	}
	patchSession(date, func(o obj) {
		eo := sessExercise(o, xi)
		ss, _ := eo["sets"].([]any)
		for _, s := range ss {
			if so, ok := s.(obj); ok {
				so["done"] = false
			}
		}
	})
}

func (k *wk) workout(y int) int {
	su := &k.c.somaUI
	today := somaToday()
	o := somaObj("history/" + today)
	if o == nil {
		return k.workoutStart(y)
	}
	s := parseSession(today, o)
	// The figures: what's been done so far, and the clock since the first set.
	done, vol := 0, 0.0
	for _, ex := range s.Ex {
		for _, st := range ex.Sets {
			if st.Done && st.Type != "warmup" {
				done++
				vol += st.W * st.Reps
			}
		}
	}
	box := k.row4(y)
	title := s.Split
	if title == "" {
		title = "Workout"
	}
	k.small(box(0), "dumbbell", "Today", clip(wkFont("bold", 36), title, box(0).Dx()-48), "", fmt.Sprintf("%d exercises", len(s.Ex)), wkFG)
	k.btn("wk:type-open:srename:"+today, box(0))
	k.small(box(1), "list-checks", "Sets done", fmt.Sprint(done), "", "working sets ticked", wkFG)
	k.small(box(2), "chart-column", "Volume", fmtThousands(int(vol)), "kg", "kg × reps", wkFG)
	el := "0:00"
	if st := msTime(o["startedAt"]); !st.IsZero() {
		d := time.Since(st)
		el = fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60)
	}
	k.small(box(3), "timer", "Time", el, "", "since you began", wkFG)
	y += 184
	for xi, ex := range s.Ex {
		y = k.workoutExercise(today, xi, ex, y) + 18
	}
	half := (wkX1 - wkX0 - 16) / 2
	k.boxBtn("wk:libpick:"+today, wkX0, y, wkX0+half, y+76, "plus", "Add an exercise", wkLime, wkOnLime)
	k.boxBtn("wk:wfinish:"+today, wkX0+half+16, y, wkX1, y+76, "check", "Finish workout", color.RGBA{}, wkFG)
	y += 96
	if su.restEnd.After(time.Now()) {
		y += 20
	}
	return y
}

// workoutStart is the Workout tab before anything's logged today: Soma's splits to load,
// or an empty workout to fill from the library.
func (k *wk) workoutStart(y int) int {
	cr := image.Rect(wkX0, y, wkX1, y+190)
	k.cardIn(cr, blend(wkCard, wkLime, 0.06))
	k.icon("dumbbell", wkX0+54, y+62, 40, wkLime)
	k.text(wkFont("bold", 32), wkX0+96, y+74, wkFG, "Start today's workout")
	k.text(wkFont("regular", 20), wkX0+96, y+110, wkMuted, "Load a split below, or begin empty and add exercises from the library.")
	k.boxBtn("wk:wempty:", wkX0+28, y+128, wkX0+330, y+176, "play", "Empty workout", wkLime, wkOnLime)
	k.boxBtn("wk:libpick:"+somaToday(), wkX0+346, y+128, wkX0+700, y+176, "search", "Pick exercises", color.RGBA{}, wkFG)
	y = cr.Max.Y + 34
	k.text(wkFont("semi", 19), wkX0+4, y, wkMuted, "SPLITS")
	y += 22
	cw := (wkX1 - wkX0 - 20) / 2
	for i, sp := range libSplits() {
		x0 := wkX0 + (i%2)*(cw+20)
		r := image.Rect(x0, y, x0+cw, y+176)
		k.card(r)
		name, focus, _ := strings.Cut(sp.Name, " (")
		k.text(wkFont("bold", 25), x0+24, y+46, wkFG, name)
		k.text(wkFont("regular", 18), x0+24, y+74, wkMuted, clip(wkFont("regular", 18), strings.TrimSuffix(focus, ")"), cw-48))
		// The first few exercises' faces.
		fx := x0 + 24
		for j, n := range sp.Exercises {
			if j == 5 {
				k.text(wkFont("semi", 18), fx+6, y+140, wkMuted, fmt.Sprintf("+%d", len(sp.Exercises)-5))
				break
			}
			exFace(k.img, k.R(fx, y+96, fx+64, y+160), n, 14)
			fx += 72
		}
		k.btn(fmt.Sprintf("wk:wsplit:%d", i), k.R(x0, y, x0+cw, y+176))
		if i%2 == 1 || i == len(libSplits())-1 {
			y += 196
		}
	}
	return y
}

// workoutExercise is an exercise of today's workout: its face, name and muscles, and each
// set with its weight and reps to change and its tick.
func (k *wk) workoutExercise(date string, xi int, ex wkEx, y int) int {
	h := 132 + 84*len(ex.Sets) + 70
	cr := image.Rect(wkX0, y, wkX1, y+h)
	k.card(cr)
	exFace(k.img, k.R(wkX0+22, y+22, wkX0+118, y+118), ex.Name, 18)
	k.text(wkFont("semi", 26), wkX0+140, y+58, wkFG, clip(wkFont("semi", 26), ex.Name, 640))
	sub := ex.Muscle
	if e, ok := libFind(ex.Name); ok && e.SubTarget != "" {
		sub += " · " + e.SubTarget
	}
	if ex.BW {
		sub += " · bodyweight"
	}
	k.text(wkFont("regular", 19), wkX0+140, y+92, wkMuted, clip(wkFont("regular", 19), sub, 600))
	if e, ok := libFind(ex.Name); ok {
		if col, ok := tierColour(e.Tier); ok {
			k.badge(1060, y+52, strings.TrimSuffix(e.Tier, "-Tier"), col)
		}
		k.btn("wk:libex:"+strconv.Itoa(exLib.byKey[exerciseKey(e.Name)]), k.R(wkX0, y, 900, y+128))
	}
	k.outline(fmt.Sprintf("wk:sheet-open:hex:%s:%d", date, xi), "ellipsis", "", 1136, y+28, 52)
	ry := y + 132
	hf := wkFont("semi", 15)
	k.text(hf, wkX0+34, ry-6, wkFaint, "SET")
	k.text(hf, wkX0+200, ry-6, wkFaint, "KG")
	k.text(hf, wkX0+500, ry-6, wkFaint, "REPS")
	k.textR(hf, 1140, ry-6, wkFaint, "DONE")
	for si, st := range ex.Sets {
		arg := fmt.Sprintf("%s:%d:%d", date, xi, si)
		row := k.R(wkX0+12, ry+6, wkX1-12, ry+78)
		if st.Done {
			ui.RoundRect(k.img, row, 14, blend(wkCard, wkLime, 0.07))
		}
		cy := ry + 42
		lab := fmt.Sprint(si + 1)
		switch st.Type {
		case "warmup":
			lab = "W"
		case "dropset":
			lab = "D"
		}
		k.textC(wkFont("bold", 22), wkX0+44, cy+8, map[bool]color.RGBA{true: wkFG, false: wkMuted}[st.Done], lab)
		k.btn("wk:sheet-open:hset:"+arg, k.R(wkX0, ry, wkX0+90, ry+84))
		// Weight and reps, each with − and + on either side.
		stepper := func(x int, val, d float64, field string) {
			k.iconBtn(fmt.Sprintf("wk:hsetv:%s:%s:%g", arg, field, -d), "minus", x+26, cy, 26, rgb(0x20222a), wkFG)
			vr := k.R(x+60, cy-30, x+200, cy+30)
			ui.RoundRect(k.img, vr, 12, rgb(0x101117))
			apTextCenter(k.img, wkFont("bold", 26), (vr.Min.X+vr.Max.X)/2, (vr.Min.Y+vr.Max.Y)/2, wkFG, strconv.FormatFloat(val, 'f', -1, 64))
			k.btn(fmt.Sprintf("wk:type-open:hset%s:%s", field[:1], arg), vr)
			k.iconBtn(fmt.Sprintf("wk:hsetv:%s:%s:%g", arg, field, d), "plus", x+234, cy, 26, rgb(0x20222a), wkFG)
		}
		stepper(wkX0+110, st.W, 2.5, "weight")
		stepper(wkX0+410, st.Reps, 1, "reps")
		// The tick.
		tr := k.R(1068, cy-30, 1140, cy+30)
		if st.Done {
			ui.RoundRect(k.img, tr, 16, wkLime)
			lucideIcon(k.img, "check", (tr.Min.X+tr.Max.X)/2, (tr.Min.Y+tr.Max.Y)/2, 30, wkOnLime)
		} else {
			ui.RoundRect(k.img, tr, 16, rgb(0x2a2c35))
			ui.RoundRect(k.img, tr.Inset(3), 13, rgb(0x15161c))
		}
		k.btn("wk:wtick:"+arg, tr.Add(image.Pt(0, k.oy)).Inset(-8).Sub(image.Pt(0, k.oy)))
		ry += 84
	}
	ar := k.R(wkX0+24, ry+12, wkX0+300, ry+58)
	ui.RoundRect(k.img, ar, 12, rgb(0x191a21))
	lucideIcon(k.img, "plus", ar.Min.X+30, (ar.Min.Y+ar.Max.Y)/2, 22, wkMuted)
	apText(k.img, wkFont("medium", 19), ar.Min.X+52, (ar.Min.Y+ar.Max.Y)/2+7, wkMuted, "Add a set")
	k.btn(fmt.Sprintf("wk:waddset:%s:%d", date, xi), ar)
	return cr.Max.Y
}

// restPill is the rest timer, in the band at the bottom while it runs.
func (k *wk) restPill(h int) bool {
	su := &k.c.somaUI
	left := time.Until(su.restEnd)
	if left <= 0 {
		return false
	}
	ui.Fill(k.img, image.Rect(wkSideW+1, h-wkTimerH, k.c.s.W, h), wkBG)
	ui.Fill(k.img, image.Rect(0, h-wkTimerH, wkSideW-1, h), wkSide)
	ui.Fill(k.img, image.Rect(wkSideW-1, h-wkTimerH, wkSideW+1, h), rgb(0x1a1b1f))
	k.p.footer = wkTimerH
	cx := 660
	r := image.Rect(cx-300, h-120, cx+300, h-30)
	ui.RoundRect(k.img, r, 45, rgb(0x2a2c35))
	ui.RoundRect(k.img, r.Inset(2), 43, rgb(0x16171f))
	cy := (r.Min.Y + r.Max.Y) / 2
	total := max(su.restTotal, time.Second)
	frac := 1 - float64(left)/float64(total)
	ring(k.img, r.Min.X+48, cy, 26, 6, min(max(frac, 0), 1), rgb(0x2a2c35), wkLime)
	apText(k.img, wkFont("regular", 18), r.Min.X+90, cy-8, wkText2, "Rest")
	secs := int(left.Seconds() + 0.999)
	apText(k.img, wkFont("bold", 30), r.Min.X+90, cy+26, wkFG, fmt.Sprintf("%d:%02d", secs/60, secs%60))
	pr := image.Rect(r.Max.X-250, cy-28, r.Max.X-150, cy+28)
	ui.RoundRect(k.img, pr, 28, rgb(0x262833))
	apTextCenter(k.img, wkFont("semi", 21), (pr.Min.X+pr.Max.X)/2, cy, wkFG, "+30s")
	k.btn("wk:rest:30", pr)
	sr := image.Rect(r.Max.X-138, cy-28, r.Max.X-16, cy+28)
	ui.RoundRect(k.img, sr, 28, wkLime)
	apTextCenter(k.img, wkFont("semi", 21), (sr.Min.X+sr.Max.X)/2, cy, wkOnLime, "Skip")
	k.btn("wk:rest:0", sr)
	return true
}

// --- the library -------------------------------------------------------------------------------

func (k *wk) library(y int) int {
	su := &k.c.somaUI
	pick, picking := strings.CutPrefix(su.workSub, "pick:")
	mi := k.sel("lib", 0)
	k.chips("wk:lib", y, libMuscles, nil, mi)
	// Search: what's typed into the field.
	q := strings.ToLower(strings.TrimSpace(su.libQuery))
	sr := k.R(wkX0, y+74, wkX1, y+140)
	ui.RoundRect(k.img, sr, 16, wkRim)
	ui.RoundRect(k.img, sr.Inset(2), 14, rgb(0x101117))
	lucideIcon(k.img, "search", sr.Min.X+34, (sr.Min.Y+sr.Max.Y)/2, 24, wkMuted)
	if su.libQuery == "" {
		apText(k.img, wkFont("regular", 22), sr.Min.X+64, (sr.Min.Y+sr.Max.Y)/2+8, wkFaint, "Search exercises, muscles, targets")
	} else {
		apText(k.img, wkFont("medium", 22), sr.Min.X+64, (sr.Min.Y+sr.Max.Y)/2+8, wkFG, su.libQuery)
		k.iconBtn("wk:libq:", "x", wkX1-40, y+107, 20, rgb(0x2a2c35), wkFG)
	}
	k.btn("wk:type-open:libsearch:", k.R(wkX0, y+74, wkX1-70, y+140))
	y += 168
	if picking {
		k.text(wkFont("regular", 20), wkX0+4, y, wkLime, "Tap exercises to add them to today's workout")
		k.outline("wk:libdone:", "check", "Done", wkX1, y-34, 52)
		y += 28
	}
	var list []int
	for i, e := range libExercises() {
		if mi > 0 && e.Muscle != libMuscles[mi] {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Name+" "+e.Muscle+" "+e.SubTarget), q) {
			continue
		}
		list = append(list, i)
	}
	// Photos first, then the rest; S-tier before A-tier within each.
	sort.SliceStable(list, func(a, b int) bool {
		ea, eb := libExercises()[list[a]], libExercises()[list[b]]
		if (ea.Img != "") != (eb.Img != "") {
			return ea.Img != ""
		}
		rank := func(t string) int { return strings.Index("sab", strings.ToLower(t + "z")[:1]) + 1 } // S, A, B, then the rest
		ra, rb := rank(ea.Tier), rank(eb.Tier)
		if ra == 0 {
			ra = 9
		}
		if rb == 0 {
			rb = 9
		}
		return ra < rb
	})
	inToday := map[string]bool{}
	if picking {
		for _, ex := range parseSession(pick, somaObj("history/"+pick)).Ex {
			inToday[exerciseKey(ex.Name)] = true
		}
	}
	log := trainingLog()
	const cols = 3
	gap := 18
	cw := (wkX1 - wkX0 - gap*(cols-1)) / cols
	ph := cw * 2 / 3
	ch := ph + 128
	for n, i := range list {
		e := libExercises()[i]
		x0 := wkX0 + (n%cols)*(cw+gap)
		r := image.Rect(x0, y, x0+cw, y+ch)
		k.card(r)
		exFace(k.img, k.R(x0+2, y+2, x0+cw-2, y+2+ph), e.Name, 18)
		if col, ok := tierColour(e.Tier); ok {
			br := k.R(x0+14, y+14, x0+14+52, y+14+34)
			ui.RoundRect(k.img, br, 17, rgb(0x0b0c10))
			apTextCenter(k.img, wkFont("bold", 18), (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, col, strings.TrimSuffix(e.Tier, "-Tier"))
		}
		f := wkFont("semi", 21)
		ty := y + ph + 36
		for li, l := range wkWrap(f, e.Name, cw-36, 2) {
			k.text(f, x0+18, ty+li*26, wkFG, l)
		}
		foot := e.Muscle
		if e.SubTarget != "" {
			foot = e.SubTarget
		}
		if l := log[exerciseKey(e.Name)]; l != nil {
			if b, _ := l.best(); b > 0 {
				foot = fmt.Sprintf("best %.0f kg e1RM", b)
			}
		}
		k.text(wkFont("regular", 17), x0+18, y+ch-20, wkMuted, clip(wkFont("regular", 17), foot, cw-36))
		if picking {
			added := inToday[exerciseKey(e.Name)]
			cr := k.R(x0+cw-68, y+ph-56, x0+cw-14, y+ph-2)
			if added {
				ui.Circle(k.img, (cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2, 26, wkLime)
				lucideIcon(k.img, "check", (cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2, 26, wkOnLime)
			} else {
				ui.Circle(k.img, (cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2, 26, rgb(0x0b0c10))
				lucideIcon(k.img, "plus", (cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2, 26, wkFG)
			}
			k.btn(fmt.Sprintf("wk:libadd:%d", i), k.R(x0, y, x0+cw, y+ch))
		} else {
			k.btn(fmt.Sprintf("wk:libex:%d", i), k.R(x0, y, x0+cw, y+ch))
		}
		if n%cols == cols-1 || n == len(list)-1 {
			y += ch + gap
		}
	}
	if len(list) == 0 {
		k.emptyNote(wkX0+20, y+40, "No exercise matches. Try another muscle or word.")
		y += 100
	}
	return y
}

// libExercisePage is an exercise of the library: its photo, what it works, and what you've
// lifted on it.
func (k *wk) libExercisePage(i int) int {
	if i < 0 || i >= len(libExercises()) {
		k.c.somaUI.workSub = ""
		return k.training()
	}
	e := libExercises()[i]
	k.header([]string{"Personal", "Training ›"}, clip(wkFont("bold", 42), e.Name, 760), e.Muscle+" · "+e.SubTarget, color.RGBA{})
	k.square("wk:back", "chevron-left", wkX1)
	y := 236
	pr := image.Rect(wkX0, y, wkX1, y+600)
	k.card(pr)
	exFace(k.img, k.R(wkX0+2, y+2, wkX1-2, y+598), e.Name, 18)
	y = pr.Max.Y + 24
	// What it is.
	facts := [][2]string{{"Muscle", e.Muscle}, {"Target", e.SubTarget}, {"Position", e.Position}, {"Risk", e.Risk}, {"Tier", e.Tier}}
	if e.IsBW {
		facts = append(facts, [2]string{"Load", "Bodyweight (+ added)"})
	}
	if e.IsAxial {
		facts = append(facts, [2]string{"Spine", "Axial load"})
	}
	fr := image.Rect(wkX0, y, wkX1, y+40+60*((len(facts)+1)/2))
	k.card(fr)
	half := (wkX1 - wkX0) / 2
	for j, f := range facts {
		x := wkX0 + 28 + (j%2)*half
		fy := y + 54 + (j/2)*60
		k.text(wkFont("regular", 18), x, fy-20, wkFaint, strings.ToUpper(f[0]))
		col := wkFG
		if f[0] == "Tier" {
			if c, ok := tierColour(f[1]); ok {
				col = c
			}
		}
		k.text(wkFont("semi", 22), x, fy+8, col, clip(wkFont("semi", 22), f[1], half-48))
	}
	y = fr.Max.Y + 24
	// What you've done on it.
	if l := trainingLog()[exerciseKey(e.Name)]; l != nil {
		ds := l.dates()
		b, bd := l.best()
		box := k.row4(y)
		k.small(box(0), "trophy", "Best e1RM", fmt.Sprintf("%.1f", b), "kg", wkDate(bd), wkFG)
		k.small(box(1), "calendar", "Days", fmt.Sprint(len(ds)), "", "logged", wkFG)
		last := "—"
		if len(ds) > 0 && len(l.Days[ds[len(ds)-1]]) > 0 {
			last = l.Days[ds[len(ds)-1]][0].label()
		}
		k.small(box(2), "history", "Last top set", last, "", wkDate(ds[len(ds)-1]), wkFG)
		k.small(box(3), "trending-up", "Progress", "Open", "", "every day of it", wkLime)
		k.btn("wk:move:"+l.Key, box(3))
		y += 184
	} else {
		k.emptyNote(wkX0+20, y+30, "You haven't logged this one yet.")
		y += 80
	}
	k.boxBtn(fmt.Sprintf("wk:libadd:%d", i), wkX0, y, wkX1, y+76, "plus", "Add to today's workout", wkLime, wkOnLime)
	return y + 100
}

func init() {
	wkActions["libex"] = func(c *console, i string) { c.wkGo("training", "libex:"+i) }
	wkActions["libpick"] = func(c *console, date string) {
		c.wkGo("training", "pick:"+date)
	}
	wkActions["libdone"] = func(c *console, _ string) {
		c.wkGo("training", "")
		c.wkSetSel("training", "tab", 0)
	}
	wkActions["libadd"] = func(c *console, i string) {
		n, _ := strconv.Atoi(i)
		if n < 0 || n >= len(libExercises()) {
			return
		}
		name := libExercises()[n].Name
		date := somaToday()
		for _, ex := range parseSession(date, somaObj("history/"+date)).Ex {
			if exerciseKey(ex.Name) == exerciseKey(name) { // already in it: out again
				patchSession(date, func(o obj) {
					exs, _ := o["exercises"].([]any)
					for j := len(exs) - 1; j >= 0; j-- {
						if eo, _ := exs[j].(obj); exerciseKey(str(eo["name"])) == exerciseKey(name) {
							o["exercises"] = append(exs[:j], exs[j+1:]...)
							break
						}
					}
				})
				return
			}
		}
		workoutAdd(name, 3)
		if !strings.HasPrefix(c.somaUI.workSub, "pick:") { // from its page: to the workout
			c.wkGo("training", "")
			c.wkSetSel("training", "tab", 0)
		}
		c.toastMsg(name + " added")
	}
	wkActions["libq"] = func(c *console, _ string) { c.somaUI.libQuery = "" }
	wkPrompts["libsearch"] = func(string) (string, string) { return "An exercise, a muscle, a target", "" }
	wkTypers["libsearch"] = func(c *console, _, text string) { c.somaUI.libQuery = text }
	wkActions["wempty"] = func(c *console, _ string) { workoutEnsure("Workout") }
	wkActions["wsplit"] = func(c *console, i string) {
		n, _ := strconv.Atoi(i)
		if n < 0 || n >= len(libSplits()) {
			return
		}
		sp := libSplits()[n]
		name, _, _ := strings.Cut(sp.Name, " (")
		workoutEnsure(name)
		for _, ex := range sp.Exercises {
			workoutAdd(ex, 3)
		}
		c.toastMsg(name + " loaded · " + fmt.Sprint(len(sp.Exercises)) + " exercises")
	}
	wkActions["waddset"] = func(c *console, arg string) {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		addSet(date, xi)
		patchSession(date, func(o obj) {
			ss, _ := sessExercise(o, xi)["sets"].([]any)
			if len(ss) > 0 {
				if so, ok := ss[len(ss)-1].(obj); ok {
					so["done"] = false
				}
			}
		})
	}
	wkActions["wtick"] = func(c *console, arg string) {
		parts := strings.Split(arg, ":")
		if len(parts) != 3 {
			return
		}
		date := parts[0]
		xi, _ := strconv.Atoi(parts[1])
		si, _ := strconv.Atoi(parts[2])
		now := false
		patchSession(date, func(o obj) {
			so := exSet(sessExercise(o, xi), si)
			if so == nil {
				return
			}
			d, ok := so["done"].(bool)
			now = !(d || !ok)
			so["done"] = now
		})
		if now { // a set done: rest, as long as Soma rests between sets
			secs := 90.0 // Soma's restForSet: the default, a third for a warm-up, 15 s after a drop set
			if v := num(jsonValue(soma.get("settings.restDefault"))); v > 0 {
				secs = v
			}
			if so := exSet(sessExercise(somaObj("history/"+date), xi), si); so != nil {
				switch str(so["type"]) {
				case "warmup":
					secs = min(30, math.Round(secs*0.3))
				case "dropset":
					secs = 15
				}
			}
			c.startRest(time.Duration(secs) * time.Second)
			vibrate(12)
		}
	}
	wkActions["rest"] = func(c *console, arg string) {
		n, _ := strconv.Atoi(arg)
		su := &c.somaUI
		if n == 0 {
			su.restEnd = time.Time{}
			return
		}
		su.restEnd = su.restEnd.Add(time.Duration(n) * time.Second)
		su.restTotal += time.Duration(n) * time.Second
	}
	wkActions["wfinish"] = func(c *console, date string) {
		o := somaObj("history/" + date)
		if o == nil {
			return
		}
		if st := msTime(o["startedAt"]); !st.IsZero() {
			d := time.Since(st)
			patchSession(date, func(o obj) { o["durationFormatted"] = fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60) })
		}
		c.somaUI.restEnd = time.Time{}
		c.toastMsg("Workout saved")
		c.wkGo("training", "sess:"+date)
	}
}

// startRest starts the rest timer: the band at the bottom counts it down, and it buzzes when
// it's over.
func (c *console) startRest(d time.Duration) {
	su := &c.somaUI
	su.restEnd, su.restTotal = time.Now().Add(d), d
	end := su.restEnd
	time.AfterFunc(d, func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.somaUI.restEnd.Equal(end) {
			c.somaUI.restEnd = time.Time{}
			vibrate(60)
			if c.mode == modeSoma && c.screenOn && !c.somaUI.typing {
				c.toastMsg("Rest over · next set")
				c.showPage()
			}
		}
	})
}

// wkSetSel chooses option n of a page's group (its tab, chip or segment).
func (c *console) wkSetSel(page, group string, n int) {
	if c.somaUI.workSel == nil {
		c.somaUI.workSel = map[string]string{}
	}
	c.somaUI.workSel[page+"wk:"+group] = strconv.Itoa(n)
}
