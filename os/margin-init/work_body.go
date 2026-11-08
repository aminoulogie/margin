package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"margin-init/ui"
)

// The body around the training, as Soma keeps it on the phone (training-feedback.ts,
// smart-training.ts): what was done outside the gym and what aches, on the day's nutrition
// record (activities: [{id, hours, effort}], aches: [{where, level}]), so the recovery map
// on the phone sees a climb or a sore knee logged here; how each exercise went (the wrap on
// a session's exercise: {feel, held, painAt, note, at}, and straps); and the week's hard
// sets per muscle group against the floor below which a group is under-trained.

// --- outside activity and aches (training-feedback.ts) ----------------------------------------

var bodyActs = []struct{ id, label, icon string }{
	{"climbing", "Climbing", "mountain"}, {"hiking", "Hike", "footprints"}, {"running", "Run", "activity"},
	{"football", "Football", "trophy"}, {"swimming", "Swim", "waves"}, {"cycling", "Ride", "bike"}, {"combat", "Combat", "hand"},
}

var bodyJoints = []struct{ id, label string }{
	{"shoulder", "Shoulder"}, {"elbow", "Elbow"}, {"wrist", "Wrist"}, {"forearm", "Forearms"},
	{"lower_back", "Lower back"}, {"hip", "Hip"}, {"knee", "Knee"}, {"neck", "Neck"},
}

var (
	effortLabels = []string{"Easy", "Normal", "Hard"}
	acheLabels   = []string{"", "niggle", "sore", "hurts"}
)

func actLabel(id string) (string, string) {
	for _, a := range bodyActs {
		if a.id == id {
			return a.label, a.icon
		}
	}
	return id, "activity"
}

func jointLabel(id string) string {
	for _, j := range bodyJoints {
		if j.id == id {
			return j.label
		}
	}
	return id
}

// dayList is a list field of the day's record, as objects.
func dayList(day obj, field string) []obj {
	l, _ := day[field].([]any)
	var out []obj
	for _, v := range l {
		if o, ok := v.(obj); ok {
			out = append(out, o)
		}
	}
	return out
}

func setDayList(day obj, field string, l []obj) {
	out := make([]any, len(l))
	for i, o := range l {
		out[i] = o
	}
	day[field] = out
}

func acheLevel(day obj, where string) int {
	for _, a := range dayList(day, "aches") {
		if str(a["where"]) == where {
			return int(num(a["level"]))
		}
	}
	return 0
}

// chipFlow lays chips of these labels from x0 to x1, wrapping: each chip's rectangle (y is
// relative to the first row's top) and the rows' total height.
func chipFlow(labels []string, icons []string, x0, x1 int) ([]image.Rectangle, int) {
	f := wkFont("medium", 21)
	var out []image.Rectangle
	x, y := x0, 0
	for i, l := range labels {
		w := ui.TextWidth(f, l) + 40
		if i < len(icons) && icons[i] != "" {
			w += 34
		}
		if x > x0 && x+w > x1 {
			x, y = x0, y+66
		}
		out = append(out, image.Rect(x, y, x+w, y+53))
		x += w + 13
	}
	return out, y + 53
}

// chip draws one (page coordinates): fill zero is the plain dark one.
func (k *wk) chip(id string, r image.Rectangle, icon, label string, fill color.RGBA) {
	rr := k.R(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
	fg := wkText2
	if fill.A == 0 {
		ui.RoundRect(k.img, rr, 12, wkRim)
		ui.RoundRect(k.img, rr.Inset(2), 10, rgb(0x15161c))
	} else {
		ui.RoundRect(k.img, rr, 12, fill)
		fg = accentInk(fill)
	}
	tx := rr.Min.X + 20
	if icon != "" {
		lucideIcon(k.img, icon, tx+12, (rr.Min.Y+rr.Max.Y)/2, 24, fg)
		tx += 34
	}
	apText(k.img, wkFont("medium", 21), tx, (rr.Min.Y+rr.Max.Y)/2+8, fg, label)
	k.btn(id, rr)
}

// outsideAches is the evening's card for outside activity and aches, today's or yesterday's.
func (k *wk) outsideAches(y int, today string) int {
	which := k.sel("seg:evday", 0)
	date := today
	if which == 1 {
		date = wkAddDays(today, -1)
	}
	day := somaRead("nutrition/" + date)
	acts := dayList(day, "activities")
	var aLabels, aIcons []string
	for _, a := range bodyActs {
		aLabels, aIcons = append(aLabels, a.label), append(aIcons, a.icon)
	}
	var jLabels []string
	for _, j := range bodyJoints {
		l := j.label
		if lv := acheLevel(day, j.id); lv > 0 {
			l += " · " + acheLabels[min(lv, 3)]
		}
		jLabels = append(jLabels, l)
	}
	ar, ah := chipFlow(aLabels, aIcons, 188, wkX1-28)
	jr, jh := chipFlow(jLabels, nil, 188, wkX1-28)
	h := 104 + 34 + ah + 20 + 84*len(acts) + 30 + 34 + jh + 30
	cr := image.Rect(wkX0, y, wkX1, y+h)
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "mountain", "Outside & aches", "· the phone's recovery map counts them")
	k.segBar("wk:seg:evday", image.Rect(wkX1-330, y+22, wkX1-28, y+80), []string{"Today", "Yesterday"}, which)

	ry := y + 104
	k.text(wkFont("semi", 19), 190, ry+20, wkMuted, "Outside the gym · tap to add")
	ry += 34
	for i, r := range ar {
		a := bodyActs[i]
		k.chip("wk:evact:"+date+":"+a.id, r.Add(image.Pt(0, ry)), a.icon, a.label, color.RGBA{})
	}
	ry += ah + 20
	for i, a := range acts {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		label, icon := actLabel(str(a["id"]))
		k.icon(icon, 206, ry+42, 26, wkLime)
		k.text(wkFont("semi", 22), 240, ry+50, wkFG, label)
		cy := ry + 42
		arg := fmt.Sprintf("%s:%d", date, i)
		k.iconBtn("wk:evacth:"+arg+":-0.5", "minus", 470, cy, 22, rgb(0x20222a), wkFG)
		k.textC(wkFont("bold", 22), 545, cy+8, wkFG, fmt.Sprintf("%g h", num(a["hours"])))
		k.iconBtn("wk:evacth:"+arg+":0.5", "plus", 620, cy, 22, rgb(0x20222a), wkFG)
		eff := min(max(int(num(a["effort"])), 1), 3)
		k.segBar("wk:evacte:"+arg, image.Rect(670, ry+16, 1060, ry+68), effortLabels, eff-1)
		k.iconBtn("wk:evactx:"+arg, "x", 1110, cy, 22, rgb(0x20222a), wkMuted)
		ry += 84
	}
	ui.Fill(k.img, k.R(wkX0+2, ry+10, wkX1-2, ry+12), wkLine)
	ry += 30
	k.text(wkFont("semi", 19), 190, ry+20, wkMuted, "Aches · each tap: niggle, sore, hurts, none")
	ry += 34
	for i, r := range jr {
		var fill color.RGBA
		switch acheLevel(day, bodyJoints[i].id) {
		case 1:
			fill = blend(rgb(0x15161c), wkAmber, 0.45)
		case 2:
			fill = wkAmber
		case 3:
			fill = wkRed
		}
		k.chip("wk:evache:"+date+":"+bodyJoints[i].id, r.Add(image.Pt(0, ry)), "", jLabels[i], fill)
	}
	return cr.Max.Y
}

// trainingToday is the evening's card of today's exercises and how each went (tap: say).
func (k *wk) trainingToday(y int, today string) int {
	o := somaRead("history/" + today)
	exs, _ := o["exercises"].([]any)
	if len(exs) == 0 {
		return y - 24 // no card
	}
	cr := image.Rect(wkX0, y, wkX1, y+92+84*len(exs))
	k.card(cr)
	answered := 0
	for _, e := range exs {
		eo, _ := e.(obj)
		w, _ := eo["wrap"].(obj)
		if !wrapEmpty(w) {
			answered++
		}
	}
	k.cardHead(wkX0+28, y+53, "dumbbell", "Training today", fmt.Sprintf("· how it went, %d of %d said", answered, len(exs)))
	ry := y + 92
	for xi, e := range exs {
		eo, _ := e.(obj)
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 21), 190, ry+36, wkFG, clip(wkFont("semi", 21), str(eo["name"]), 860))
		w, _ := eo["wrap"].(obj)
		straps, _ := eo["straps"].(bool)
		sum, col := wrapSummary(w, straps), wrapColour(w)
		if sum == "" {
			sum, col = "How did it go? Tap to say", wkFaint
		}
		if n := strings.TrimSpace(str(w["note"])); n != "" {
			sum += " · “" + n + "”"
		}
		k.text(wkFont("regular", 18), 190, ry+66, col, clip(wkFont("regular", 18), sum, 900))
		k.icon("chevron-right", 1124, ry+42, 22, wkFaint)
		k.btn(fmt.Sprintf("wk:sheet-open:hwrap:%s:%d", today, xi), k.R(wkX0, ry, wkX1, ry+84))
		ry += 84
	}
	return cr.Max.Y
}

// --- how an exercise went (training-feedback.ts) ----------------------------------------------

var (
	feelLabels = []string{"", "Off", "OK", "Strong"}
	heldBy     = []struct{ id, label, short string }{
		{"muscle", "Target muscle", "Muscle"}, {"joint", "Joint pain", "Joint"}, {"grip", "Grip / forearms", "Grip"},
		{"carried", "Tired from before", "Carried"}, {"energy", "Low energy", "Energy"}, {"form", "Form", "Form"},
	}
)

func wrapStrings(w obj, field string) []string {
	l, _ := w[field].([]any)
	var out []string
	for _, v := range l {
		out = append(out, str(v))
	}
	return out
}

// wrapEmpty is isEmptyWrap: nothing answered.
func wrapEmpty(w obj) bool {
	return w == nil || num(w["feel"]) == 0 && len(wrapStrings(w, "held")) == 0 && len(wrapStrings(w, "painAt")) == 0 &&
		strings.TrimSpace(str(w["note"])) == ""
}

// wrapSummary is the phone's one line: "Off · Joint pain (Shoulder) · straps".
func wrapSummary(w obj, straps bool) string {
	var parts []string
	if f := int(num(w["feel"])); f >= 1 && f <= 3 {
		parts = append(parts, feelLabels[f])
	}
	pain := wrapStrings(w, "painAt")
	for _, h := range wrapStrings(w, "held") {
		label := h
		for _, x := range heldBy {
			if x.id == h {
				label = x.label
			}
		}
		if h == "joint" && len(pain) > 0 {
			var js []string
			for _, j := range pain {
				js = append(js, jointLabel(j))
			}
			label += " (" + strings.Join(js, ", ") + ")"
		}
		parts = append(parts, label)
	}
	if straps {
		parts = append(parts, "straps")
	}
	return strings.Join(parts, " · ")
}

// wrapColour: amber when it went badly or hurt, else quiet.
func wrapColour(w obj) color.RGBA {
	if num(w["feel"]) == 1 || slices.Contains(wrapStrings(w, "held"), "joint") {
		return wkAmber
	}
	if num(w["feel"]) == 3 {
		return wkGreen
	}
	return wkMuted
}

// patchWrap changes one exercise's wrap (or straps), without recomputing the session's
// totals: how it went changes none of them.
func patchWrap(date string, xi int, fn func(eo, w obj)) {
	o := somaObj("history/" + date)
	eo := sessExercise(o, xi)
	if eo == nil {
		return
	}
	w, _ := eo["wrap"].(obj)
	if w == nil {
		w = obj{}
	}
	fn(eo, w)
	if wrapEmpty(w) {
		delete(eo, "wrap")
	} else {
		w["at"] = float64(time.Now().UnixMilli())
		eo["wrap"] = w
	}
	somaPut("history/"+date, o)
}

func toggled(l []string, v string) []any {
	var out []any
	had := false
	for _, x := range l {
		if x == v {
			had = true
			continue
		}
		out = append(out, x)
	}
	if !had {
		out = append(out, v)
	}
	return out
}

func init() {
	wkActions["evact"] = func(c *console, arg string) {
		date, id, _ := strings.Cut(arg, ":")
		patchFoodDay(date, func(day obj) {
			// The phone's defaults: two hours, normal effort; the row's buttons change them.
			setDayList(day, "activities", append(dayList(day, "activities"), obj{"id": id, "hours": 2.0, "effort": 2.0}))
		})
	}
	actAt := func(arg string, fn func(l []obj, i int, rest string) []obj) {
		p := strings.SplitN(arg, ":", 3)
		if len(p) < 2 {
			return
		}
		i, _ := strconv.Atoi(p[1])
		rest := ""
		if len(p) == 3 {
			rest = p[2]
		}
		patchFoodDay(p[0], func(day obj) {
			l := dayList(day, "activities")
			if i >= 0 && i < len(l) {
				setDayList(day, "activities", fn(l, i, rest))
			}
		})
	}
	wkActions["evacth"] = func(c *console, arg string) {
		actAt(arg, func(l []obj, i int, rest string) []obj {
			d, _ := strconv.ParseFloat(rest, 64)
			l[i]["hours"] = math.Min(math.Max(num(l[i]["hours"])+d, 0.5), 12)
			return l
		})
	}
	wkActions["evacte"] = func(c *console, arg string) {
		actAt(arg, func(l []obj, i int, rest string) []obj {
			n, _ := strconv.Atoi(rest)
			l[i]["effort"] = float64(min(max(n+1, 1), 3))
			return l
		})
	}
	wkActions["evactx"] = func(c *console, arg string) {
		actAt(arg, func(l []obj, i int, _ string) []obj { return append(l[:i], l[i+1:]...) })
	}
	wkActions["evache"] = func(c *console, arg string) {
		date, where, _ := strings.Cut(arg, ":")
		patchFoodDay(date, func(day obj) {
			var rest []obj
			level := 0
			for _, a := range dayList(day, "aches") {
				if str(a["where"]) == where {
					level = int(num(a["level"]))
					continue
				}
				rest = append(rest, a)
			}
			if level = (level + 1) % 4; level > 0 {
				rest = append(rest, obj{"where": where, "level": float64(level)})
			}
			setDayList(day, "aches", rest)
		})
	}

	// How an exercise went: the phone's "How was it?" after the last set.
	wkSheets["hwrap"] = func(k *wk, arg string) *wkSheet {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		eo := sessExercise(somaRead("history/"+date), xi)
		if eo == nil {
			return nil
		}
		w, _ := eo["wrap"].(obj)
		straps, _ := eo["straps"].(bool)
		held, pain := wrapStrings(w, "held"), wrapStrings(w, "painAt")
		id := "wk:hwrap:" + arg + ":"
		var feel []wkOpt
		for f := 1; f <= 3; f++ {
			feel = append(feel, wkOpt{id: fmt.Sprintf("%sfeel:%d", id, f), label: feelLabels[f], sel: int(num(w["feel"])) == f})
		}
		hb := func(from, to int) []wkOpt {
			var out []wkOpt
			for _, h := range heldBy[from:to] {
				out = append(out, wkOpt{id: id + "held:" + h.id, label: h.short, sel: slices.Contains(held, h.id)})
			}
			return out
		}
		sub := wrapSummary(w, straps)
		if sub == "" {
			sub = wkDate(date) + " · nothing said yet"
		}
		sh := &wkSheet{title: str(eo["name"]), sub: sub, done: "Done", rows: []wkRow{
			{kind: "seg", label: "How it felt", opts: feel},
			{kind: "seg", label: "What stopped it (any)", opts: hb(0, 3)},
			{kind: "seg", opts: hb(3, 6)},
		}}
		if slices.Contains(held, "joint") {
			jo := func(from, to int) []wkOpt {
				var out []wkOpt
				for _, j := range bodyJoints[from:to] {
					out = append(out, wkOpt{id: id + "pain:" + j.id, label: j.label, sel: slices.Contains(pain, j.id)})
				}
				return out
			}
			sh.rows = append(sh.rows, wkRow{kind: "seg", label: "Where it hurt", opts: jo(0, 4)}, wkRow{kind: "seg", opts: jo(4, 8)})
		}
		note := strings.TrimSpace(str(w["note"]))
		if note == "" {
			note = "add one"
		}
		sh.rows = append(sh.rows,
			wkRow{kind: "seg", label: "Straps", opts: []wkOpt{{id: id + "straps:0", label: "No straps", sel: !straps}, {id: id + "straps:1", label: "Straps", sel: straps}}},
			wkRow{id: "wk:type-open:hwrapnote:" + arg, label: "Note", icon: "pencil", detail: clip(wkFont("regular", 20), note, 380)})
		return sh
	}
	wkActions["hwrap"] = func(c *console, arg string) {
		c.somaUI.keep = true
		p := strings.SplitN(arg, ":", 4)
		if len(p) != 4 {
			return
		}
		xi, _ := strconv.Atoi(p[1])
		field, v := p[2], p[3]
		patchWrap(p[0], xi, func(eo, w obj) {
			switch field {
			case "feel":
				n, _ := strconv.Atoi(v)
				if int(num(w["feel"])) == n {
					delete(w, "feel") // tapped again: unsaid
				} else {
					w["feel"] = float64(n)
				}
			case "held":
				w["held"] = toggled(wrapStrings(w, "held"), v)
				if v == "joint" && !slices.Contains(wrapStrings(w, "held"), "joint") {
					delete(w, "painAt")
				}
			case "pain":
				w["painAt"] = toggled(wrapStrings(w, "painAt"), v)
			case "straps":
				if v == "1" {
					eo["straps"] = true
				} else {
					delete(eo, "straps")
				}
			}
		})
	}
	wkPrompts["hwrapnote"] = func(arg string) (string, string) {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		w, _ := sessExercise(somaRead("history/"+date), xi)["wrap"].(obj)
		return "A note on how it went", str(w["note"])
	}
	wkTypers["hwrapnote"] = func(c *console, arg, text string) {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		patchWrap(date, xi, func(_, w obj) {
			if t := strings.TrimSpace(text); t != "" {
				w["note"] = t
			} else {
				delete(w, "note")
			}
		})
	}
}

// --- the week's coverage (smart-training.ts) --------------------------------------------------

type muscleVariant struct {
	id, label string
	match     *regexp.Regexp
	core      bool
}

type muscleGroup struct {
	id, label string
	keys      []string
	weeklyMin int
	variants  []muscleVariant
}

func mv(id, label, re string, core bool) muscleVariant {
	return muscleVariant{id, label, regexp.MustCompile("(?i)" + re), core}
}

// muscleGroups are smart-training.ts's GROUPS: the recovery map's keys, the weekly floor of
// hard sets, and the variations (heads, patterns) each wants. The patterns are the phone's;
// the calf raise's "not seated or soleus" is done in variantMatch (Go has no lookahead).
var muscleGroups = []muscleGroup{
	{"chest", "Chest", []string{"chest"}, 8, []muscleVariant{
		mv("upper", "Upper chest", `upper pec|clavicular|incline|low-to-high`, true),
		mv("mid", "Mid chest", `sternal|flat|bench press|chest press|pec deck|press-around|push-?up`, true),
		mv("lower", "Lower chest", `costal|dip|high-to-low|decline`, false)}},
	{"shoulders", "Shoulders", []string{"deltoids", "deltoids_back"}, 8, []muscleVariant{
		mv("side", "Side delts", `side delt|lateral raise|y-raise|upright row`, true),
		mv("rear", "Rear delts", `rear delt|posterior|face pull|reverse pec|reverse fly|rear fly`, true),
		mv("front", "Front delts", `front delt|anterior|shoulder press|overhead press|ohp|arnold|front raise`, false)}},
	{"triceps", "Triceps", []string{"triceps", "triceps_back"}, 6, []muscleVariant{
		mv("long", "Long head", `long head|long &|overhead|skull|cross-body|french|jm press`, true),
		mv("lateral", "Lateral / medial head", `lateral &|lateral head|medial|pushdown|push-down|dip|diamond|close-?grip`, true)}},
	{"biceps", "Biceps", []string{"biceps"}, 6, []muscleVariant{
		mv("long", "Long head", `long head|incline (dumbbell )?curl|bayesian`, true),
		mv("short", "Short head", `short head|preacher|concentration|spider`, true),
		mv("brachialis", "Brachialis", `brachialis|hammer|reverse.grip|reverse curl`, false)}},
	{"back", "Back", []string{"upper_back", "trapezius_back"}, 10, []muscleVariant{
		mv("vertical", "Lats (vertical pull)", `\blats?\b|vertical|pulldown|pull-?up|chin-?up|pullover`, true),
		mv("horizontal", "Mid-back (rows)", `mid-back|horizontal|rhomboid|upper back|\brow\b`, true)}},
	{"quads", "Quads", []string{"quadriceps"}, 8, []muscleVariant{
		mv("squat", "Squat pattern", `quad|squat|leg press|hack|lunge|step-up`, true),
		mv("rectus", "Rectus femoris", `rectus|leg extension`, false)}},
	{"hamstrings", "Hamstrings", []string{"hamstring"}, 6, []muscleVariant{
		mv("hinge", "Hip hinge (stretched)", `hamstrings \(lengthened\)|romanian|rdl|stiff.?leg|good morning|deadlift`, true),
		mv("curl", "Knee flexion (curls)", `knee flexion|hamstrings \(shortened\)|leg curl`, true)}},
	{"calves", "Calves", []string{"calves", "calves_back"}, 4, []muscleVariant{
		mv("gastroc", "Gastrocnemius", `gastroc|standing.*calf|leg press calf|calf raise`, true),
		mv("soleus", "Soleus", `soleus|seated calf`, false)}},
	{"abs", "Abs", []string{"abs", "obliques"}, 4, []muscleVariant{
		mv("abs", "Abs", `\babs?\b|crunch|leg raise|knee raise|ab wheel|rollout|plank|oblique`, true)}},
}

var (
	reSeatedSoleus = regexp.MustCompile(`(?i)soleus|seated`)
	reGastroc      = regexp.MustCompile(`(?i)gastroc|standing.*calf|leg press calf`)
)

func variantMatch(g, v string, re *regexp.Regexp, text string) bool {
	if !re.MatchString(text) {
		return false
	}
	if g == "calves" && v == "gastroc" { // /gastroc|standing.*calf|leg press calf|^(?!.*(soleus|seated)).*calf raise/
		return reGastroc.MatchString(text) || !reSeatedSoleus.MatchString(text)
	}
	return true
}

// groupOfKey is GROUP_BY_KEY: the group a recovery key belongs to (forearms count as biceps).
func groupOfKey(k string) string {
	if k == "forearm" {
		return "biceps"
	}
	for _, g := range muscleGroups {
		if slices.Contains(g.keys, k) {
			return g.id
		}
	}
	return ""
}

// liftVariants is variantsOf: the group variations a lift hits, credited to the group the lift
// is for (its first key's), or when that key is no group's, to any group it lists.
func liftVariants(name, subTarget string, keys []string) [][2]string {
	ck := name + "|" + subTarget + "|" + strings.Join(keys, ",")
	if v, ok := liftVariantsMemo.Load(ck); ok {
		return v.([][2]string)
	}
	v := liftVariantsOf(name, subTarget, keys)
	liftVariantsMemo.Store(ck, v)
	return v
}

// liftVariantsMemo: a lift's variants never change (the patterns and the library are fixed),
// and the coverage card asks for the same few dozen lifts on every draw.
var liftVariantsMemo sync.Map

func liftVariantsOf(name, subTarget string, keys []string) [][2]string {
	if subTarget == "" {
		if e, ok := libFind(name); ok {
			subTarget = e.SubTarget
		}
	}
	text := subTarget + " " + name
	var out [][2]string
	for _, g := range muscleGroups {
		for _, v := range g.variants {
			if !variantMatch(g.id, v.id, v.match, text) {
				continue
			}
			if len(keys) > 0 {
				if owner := groupOfKey(keys[0]); owner != "" {
					if owner != g.id {
						continue
					}
				} else if !slices.ContainsFunc(keys, func(k string) bool { return slices.Contains(g.keys, k) }) {
					continue
				}
			}
			out = append(out, [2]string{g.id, v.id})
		}
	}
	return out
}

// liftGroups is groupsOf: its variants' groups and the one its first key is filed under.
func liftGroups(name, subTarget string, keys []string) []string {
	var out []string
	for _, v := range liftVariants(name, subTarget, keys) {
		if !slices.Contains(out, v[0]) {
			out = append(out, v[0])
		}
	}
	if len(keys) > 0 {
		if g := groupOfKey(keys[0]); g != "" && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

type groupCov struct {
	group, label string
	sets, min    int
	days         []string
	hit, missing []string // core variants' labels
}

// weekCoverage is coverage(): hard sets per group logged from one date to another (both in).
func weekCoverage(from, to string) []groupCov {
	by := map[string]*groupCov{}
	var out []*groupCov
	for _, g := range muscleGroups {
		c := &groupCov{group: g.id, label: g.label, min: g.weeklyMin}
		by[g.id] = c
		out = append(out, c)
	}
	hit := map[string]bool{}
	for _, k := range soma.keysWith("history/") {
		date := strings.TrimPrefix(k, "history/")
		if date < from || date > to {
			continue
		}
		exs, _ := somaRead(k)["exercises"].([]any)
		for _, e := range exs {
			eo, _ := e.(obj)
			n := 0
			sets, _ := eo["sets"].([]any)
			for _, x := range sets {
				so, _ := x.(obj)
				if d, _ := so["done"].(bool); d && str(so["type"]) != "warmup" && str(so["type"]) != "feeder" {
					n++
				}
			}
			if n == 0 {
				continue
			}
			var keys []string
			tk, _ := eo["targetKeys"].([]any)
			for _, t := range tk {
				keys = append(keys, str(t))
			}
			name, sub := str(eo["name"]), str(eo["subTarget"])
			for _, g := range liftGroups(name, sub, keys) {
				c := by[g]
				c.sets += n
				if !slices.Contains(c.days, date) {
					c.days = append(c.days, date)
				}
			}
			for _, v := range liftVariants(name, sub, keys) {
				hit[v[0]+"/"+v[1]] = true
			}
		}
	}
	res := make([]groupCov, 0, len(out))
	for i, g := range muscleGroups {
		c := out[i]
		sort.Strings(c.days)
		for _, v := range g.variants {
			if !v.core {
				continue
			}
			if hit[g.id+"/"+v.id] {
				c.hit = append(c.hit, v.label)
			} else {
				c.missing = append(c.missing, v.label)
			}
		}
		res = append(res, *c)
	}
	return res
}

// coverageCard is the week's coverage on Training's overview: each group's hard sets against
// its floor this week (Monday on), what's still at nothing, and last week's misses.
func (k *wk) coverageCard(y int, now time.Time) int {
	mon := weekStart(now)
	this := weekCoverage(somaDate(mon), somaDate(now))
	last := weekCoverage(somaDate(mon.AddDate(0, 0, -7)), somaDate(mon.AddDate(0, 0, -1)))
	daysLeft := 6 - int(dayStart(now).Sub(mon).Hours()/24+0.5)
	var missed, lastMissed []string
	covered := 0
	for i, c := range this {
		if c.sets >= c.min {
			covered++
		}
		if c.sets == 0 {
			missed = append(missed, c.label)
		}
		if last[i].sets == 0 {
			lastMissed = append(lastMissed, last[i].label)
		}
	}
	const cols = 3
	rows := (len(this) + cols - 1) / cols
	foot := 0
	if len(missed) > 0 {
		foot += 40
	}
	if len(lastMissed) > 0 {
		foot += 40
	}
	cr := image.Rect(wkX0, y, wkX1, y+96+rows*92+foot+20)
	k.card(cr)
	left := "the last day"
	if daysLeft > 0 {
		left = fmt.Sprintf("%d %s left", daysLeft, map[bool]string{true: "day", false: "days"}[daysLeft == 1])
	}
	k.cardHead(wkX0+28, y+44, "target", "This week's coverage", fmt.Sprintf("· %d of %d groups at their floor · %s", covered, len(this), left))
	cw := (wkX1 - wkX0 - 56 - 2*16) / cols
	for i, c := range this {
		x0 := wkX0 + 28 + (i%cols)*(cw+16)
		ry := y + 84 + (i/cols)*92
		col := wkLime
		switch {
		case c.sets == 0:
			col = wkRed
		case c.sets < c.min:
			col = wkAmber
		}
		k.text(wkFont("semi", 20), x0, ry+26, wkFG, c.label)
		k.textR(wkFont("semi", 19), x0+cw, ry+26, col, fmt.Sprintf("%d / %d", c.sets, c.min))
		k.hBar(k.R(x0, ry+40, x0+cw, ry+50), math.Min(float64(c.sets)/float64(c.min), 1), col)
		sub := "nothing yet"
		if len(c.missing) > 0 && c.sets > 0 {
			sub = "no " + strings.ToLower(strings.Join(c.missing, ", "))
		} else if c.sets > 0 {
			sub = strings.Join(c.hit, ", ")
		}
		k.text(wkFont("regular", 16), x0, ry+74, wkFaint, clip(wkFont("regular", 16), sub, cw))
	}
	fy := y + 96 + rows*92
	if len(missed) > 0 {
		k.text(wkFont("medium", 19), 188, fy+20, wkRed, clip(wkFont("medium", 19), "Not trained yet: "+strings.Join(missed, ", "), 940))
		fy += 40
	}
	if len(lastMissed) > 0 {
		k.text(wkFont("regular", 18), 188, fy+20, wkMuted, clip(wkFont("regular", 18), "Missed last week: "+strings.Join(lastMissed, ", "), 940))
	}
	return cr.Max.Y
}
