package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"margin-init/ui"
)

// Training, all of it: the sessions logged in Soma (history/<date>) and the spreadsheet of
// lifts Soma imported (training-seed.json, with the corrections made to it, logOverrides),
// merged the way Soma's training log merges them (training-log.ts): one row per movement,
// keyed by exerciseKey, a day from a session winning over the sheet's. Overview, every
// movement with its progress, every session; sets, exercises and sessions can all be changed.

// --- the sheet --------------------------------------------------------------------------------------

// trainingSeedURL is the sheet as Soma ships it; the tablet keeps a copy beside Soma's records.
var trainingSeedURL = "https://raw.githubusercontent.com/aminoulogie/kite-bay-otter-topaz/main/src/lib/training-seed.json"

type seedFile struct {
	Exercises []struct {
		Name  string                `json:"name"`
		Key   string                `json:"key"`
		Group string                `json:"group"`
		BW    bool                  `json:"bw"`
		Days  map[string][]float64s `json:"days"`
	} `json:"exercises"`
}

// float64s is a set in the sheet: weight, reps, failure (any of them may be null).
type float64s = []any

var trainingSeed struct {
	sync.Mutex
	data  *seedFile
	tried time.Time
}

func seedPath() string { return somaDir + "/training-seed.json" }

// loadSeed is the sheet: from the copy on the tablet, or fetched (once an hour at most) in
// the background, the page drawn again when it arrives.
func loadSeed() *seedFile {
	soma.note("trainingSeed") // pages drawn without it are drawn again once it's here
	trainingSeed.Lock()
	defer trainingSeed.Unlock()
	if trainingSeed.data != nil {
		return trainingSeed.data
	}
	if b, err := os.ReadFile(seedPath()); err == nil {
		var f seedFile
		if json.Unmarshal(b, &f) == nil && len(f.Exercises) > 0 {
			trainingSeed.data = &f
			return &f
		}
	}
	if time.Since(trainingSeed.tried) > time.Hour && soma.linked() {
		trainingSeed.tried = time.Now()
		go fetchSeed()
	}
	return nil
}

func fetchSeed() {
	resp, err := webClient().Get(trainingSeedURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var f seedFile
	if err != nil || resp.StatusCode != 200 || json.Unmarshal(b, &f) != nil || len(f.Exercises) == 0 {
		return
	}
	os.MkdirAll(somaDir, 0o755)
	os.WriteFile(seedPath(), b, 0o644)
	trainingSeed.Lock()
	trainingSeed.data = &f
	trainingSeed.Unlock()
	soma.bump("trainingSeed")
	soma.mu.Lock()
	changed := soma.changed
	soma.mu.Unlock()
	if changed != nil {
		changed()
	}
}

// --- the log ----------------------------------------------------------------------------------------

var (
	reParen  = regexp.MustCompile(`\(.*?\)`)
	reDash   = regexp.MustCompile(`[-/]`)
	reOther  = regexp.MustCompile(`[^a-z0-9 ]`)
	rePlural = regexp.MustCompile(`\b(\w+?)s\b`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// exerciseKey is exercise-key.ts: "Leg Extensions (Machine)" and "leg extension" meet.
func exerciseKey(name string) string {
	s := strings.ToLower(name)
	s = reParen.ReplaceAllString(s, "")
	s = reDash.ReplaceAllString(s, " ")
	s = reOther.ReplaceAllString(s, "")
	s = rePlural.ReplaceAllString(s, "$1")
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

type lset struct {
	W, Reps, Fail float64
	BW            bool
	Added         float64
	Src           int // where it is in the session's exercise; -1 from the sheet
}

func (s lset) e1rm() float64 {
	if s.W <= 0 || s.Reps <= 0 {
		return 0
	}
	return s.W * (1 + s.Reps/30)
}

func (s lset) label() string {
	if s.BW {
		extra := ""
		if s.Added != 0 {
			extra = fmt.Sprintf(" +%g", s.Added)
		}
		return fmt.Sprintf("BW%s %gkg × %g", extra, math.Round(s.W*10)/10, s.Reps)
	}
	return fmt.Sprintf("%gkg × %g", math.Round(s.W*10)/10, s.Reps)
}

func dayBest(sets []lset) float64 {
	b := 0.0
	for _, s := range sets {
		b = max(b, s.e1rm())
	}
	return b
}

func dayTop(sets []lset) float64 {
	b := 0.0
	for _, s := range sets {
		b = max(b, s.W)
	}
	return b
}

func dayVol(sets []lset) float64 {
	v := 0.0
	for _, s := range sets {
		v += s.W * s.Reps
	}
	return v
}

type exLog struct {
	Name, Key, Muscle, Group string
	BW                       bool
	Days                     map[string][]lset
	Src                      map[string]int  // a session's day: its exercise's place in it
	Imp                      map[string]bool // the sheet's days
}

func (e *exLog) dates() []string {
	var ds []string
	for d := range e.Days {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	return ds
}

func (e *exLog) best() (float64, string) {
	b, at := 0.0, ""
	for d, sets := range e.Days {
		if v := dayBest(sets); v > b || (v == b && d > at) {
			b, at = v, d
		}
	}
	return b, at
}

// bodyweights is each day's weighing.
func bodyweights() map[string]float64 {
	out := map[string]float64{}
	for _, k := range soma.keysWith("nutrition/") {
		if w := num(somaRead(k)["bodyWeight"]); w > 0 {
			out[strings.TrimPrefix(k, "nutrition/")] = w
		}
	}
	return out
}

func nearestBW(bw map[string]float64, date string) float64 {
	t, _ := time.Parse("2006-01-02", date)
	best, gap := 0.0, math.Inf(1)
	for d, w := range bw {
		dt, _ := time.Parse("2006-01-02", d)
		if g := math.Abs(dt.Sub(t).Hours()); g < gap {
			best, gap = w, g
		}
	}
	return best
}

// trainingLog is buildTrainingLog: the sheet, then the sessions, then the corrections.
func trainingLog() map[string]*exLog {
	bw := bodyweights()
	by := map[string]*exLog{}
	if seed := loadSeed(); seed != nil {
		for _, e := range seed.Exercises {
			l := &exLog{Name: e.Name, Key: exerciseKey(e.Name), Muscle: e.Key, Group: e.Group, BW: e.BW, Days: map[string][]lset{}, Src: map[string]int{}, Imp: map[string]bool{}}
			for d, sets := range e.Days {
				var out []lset
				for _, s := range sets {
					get := func(i int) float64 {
						if i < len(s) {
							return num(s[i])
						}
						return 0
					}
					w, added := get(0), get(0)
					if e.BW {
						w = nearestBW(bw, d) + added
					}
					out = append(out, lset{W: w, Reps: get(1), Fail: get(2), BW: e.BW, Added: added, Src: -1})
				}
				l.Days[d], l.Imp[d] = out, true
			}
			by[l.Key] = l
		}
	}
	for _, s := range wkSessions() {
		for xi, ex := range s.Ex {
			var done []lset
			for si, st := range ex.Sets {
				if !st.Done {
					continue
				}
				w := st.W
				if ex.BW {
					w = nearestBW(bw, s.Date) + st.W
				}
				done = append(done, lset{W: w, Reps: st.Reps, Fail: max(0, st.Fail-3), BW: ex.BW, Added: st.W, Src: si})
			}
			if len(done) == 0 {
				continue
			}
			key := exerciseKey(ex.Name)
			l := by[key]
			if l == nil {
				l = &exLog{Key: key, Group: ex.Muscle, Days: map[string][]lset{}, Src: map[string]int{}, Imp: map[string]bool{}}
				if l.Group == "" {
					l.Group = "Other"
				}
				by[key] = l
			}
			l.Name = ex.Name
			if l.Muscle == "" && len(ex.Targets) > 0 {
				l.Muscle = ex.Targets[0]
			}
			l.BW = l.BW || ex.BW
			l.Days[s.Date], l.Src[s.Date] = done, xi
			delete(l.Imp, s.Date)
		}
	}
	for _, k := range soma.keysWith("logOverrides/") {
		key := strings.TrimPrefix(k, "logOverrides/")
		l := by[key]
		if l == nil {
			continue
		}
		for d, v := range somaObj(k) {
			arr, ok := v.([]any)
			if !ok { // null: the day deleted
				delete(l.Days, d)
				delete(l.Imp, d)
				continue
			}
			var out []lset
			for _, x := range arr {
				so, _ := x.(obj)
				b, _ := so["bodyweight"].(bool)
				out = append(out, lset{W: num(so["weight"]), Reps: num(so["reps"]), Fail: num(so["failure"]), BW: b, Added: num(so["added"]), Src: -1})
			}
			l.Days[d], l.Imp[d] = out, true
		}
	}
	for k, l := range by {
		if len(l.Days) == 0 {
			delete(by, k)
		}
	}
	return by
}

// --- sessions (HistorySession) -------------------------------------------------------------------

type wkSet struct {
	W, Reps, Fail float64
	Done          bool
	Type          string
}

type wkEx struct {
	Name, Muscle string
	Targets      []string
	BW, Axial    bool
	Sets         []wkSet
}

type wkSess struct {
	Date, Split, Duration string
	Cals, Vol             float64
	Sets                  int
	At                    time.Time
	Ex                    []wkEx
	Muscles               map[string]float64
}

func (s wkSess) minutes() float64 { return (&somaSession{Duration: s.Duration}).minutes() }

func parseSession(date string, o obj) wkSess {
	s := wkSess{Date: date, Split: str(o["split"]), Duration: str(o["durationFormatted"]), Cals: num(o["caloriesBurned"]),
		Vol: num(o["totalVol"]), Sets: int(num(o["totalSets"])), At: msTime(o["timestamp"]), Muscles: map[string]float64{}}
	exs, _ := o["exercises"].([]any)
	for _, e := range exs {
		eo, _ := e.(obj)
		ex := wkEx{Name: str(eo["name"]), Muscle: str(eo["muscle"])}
		ex.BW, _ = eo["isBW"].(bool)
		ex.Axial, _ = eo["isAxial"].(bool)
		tk, _ := eo["targetKeys"].([]any)
		for _, t := range tk {
			ex.Targets = append(ex.Targets, str(t))
		}
		sets, _ := eo["sets"].([]any)
		for _, x := range sets {
			so, _ := x.(obj)
			w := so["weight"]
			if w == nil {
				w = so["kg"]
			}
			st := wkSet{W: num(w), Reps: num(so["reps"]), Fail: num(so["failure"]), Type: str(so["type"]), Done: true}
			if d, ok := so["done"].(bool); ok {
				st.Done = d
			}
			if st.Type == "" {
				st.Type = "normal"
			}
			ex.Sets = append(ex.Sets, st)
		}
		s.Ex = append(s.Ex, ex)
	}
	ms, _ := o["muscles"].(obj)
	for k, v := range ms {
		mo, _ := v.(obj)
		s.Muscles[k] = num(mo["sets"])
	}
	return s
}

// wkSessions is every session, newest first.
func wkSessions() []wkSess {
	var out []wkSess
	for _, k := range soma.keysWith("history/") {
		if o := somaRead(k); o != nil {
			out = append(out, parseSession(strings.TrimPrefix(k, "history/"), o))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out
}

// recompute is recomputeSession: totals and the muscle tally follow the sets.
func recompute(o obj) {
	vol, axial, sets := 0.0, 0.0, 0
	mus := map[string][2]float64{}
	exs, _ := o["exercises"].([]any)
	for _, e := range exs {
		eo, _ := e.(obj)
		bw, _ := eo["isBW"].(bool)
		ax, _ := eo["isAxial"].(bool)
		tk, _ := eo["targetKeys"].([]any)
		ss, _ := eo["sets"].([]any)
		for _, x := range ss {
			so, _ := x.(obj)
			done, ok := so["done"].(bool)
			if ok && !done || str(so["type"]) == "warmup" {
				continue
			}
			sets++
			w, r := num(so["weight"]), math.Floor(num(so["reps"]))
			v := math.Round(w * r)
			if bw && w == 0 {
				v = math.Round(75 * 0.65 * r)
			}
			vol += v
			if ax {
				axial += v
			}
			if str(so["type"]) == "dropset" {
				continue
			}
			f := num(so["failure"])
			if f == 0 {
				f = 3
			}
			for _, t := range tk {
				m := mus[str(t)]
				share := 1 / float64(len(tk))
				mus[str(t)] = [2]float64{m[0] + share, m[1] + f*share}
			}
		}
	}
	tally := obj{}
	for k, m := range mus {
		tally[k] = obj{"sets": math.Round(m[0]*100) / 100, "avgFail": m[1] / m[0]}
	}
	o["totalVol"], o["totalSets"], o["axialVol"], o["muscles"] = vol, sets, axial, tally
}

// calories is calculateCaloriesBurned at an average effort.
func calories(mins, vol float64, sets int) float64 {
	return max(20, math.Round(mins*6*(0.8+3*0.12)+vol*0.0055))
}

func patchSession(date string, fn func(o obj)) {
	o := somaObj("history/" + date)
	if o == nil {
		return
	}
	fn(o)
	recompute(o)
	somaPut("history/"+date, o)
}

func sessExercise(o obj, i int) obj {
	exs, _ := o["exercises"].([]any)
	if i < 0 || i >= len(exs) {
		return nil
	}
	eo, _ := exs[i].(obj)
	return eo
}

func exSet(eo obj, j int) obj {
	ss, _ := eo["sets"].([]any)
	if j < 0 || j >= len(ss) {
		return nil
	}
	so, _ := ss[j].(obj)
	return so
}

// newExercise is a SessionExercise for a movement: what it was last time, else what the
// sheet says it works, else a plain one.
func newExercise(name string) obj {
	key := exerciseKey(name)
	for _, s := range wkSessions() {
		o := somaObj("history/" + s.Date)
		exs, _ := o["exercises"].([]any)
		for _, e := range exs {
			eo, _ := e.(obj)
			if exerciseKey(str(eo["name"])) == key {
				n := obj{}
				for k, v := range eo {
					if k != "sets" && k != "pump" {
						n[k] = v
					}
				}
				n["sets"] = []any{}
				return n
			}
		}
	}
	n := obj{"name": strings.TrimSpace(name), "muscle": "Other", "subTarget": "", "targetKeys": []any{}, "position": "", "risk": "Low",
		"tier": "", "isAxial": false, "isBW": false, "usesBar": false, "barWeight": 0, "supersetGroup": "", "sets": []any{}}
	if l := trainingLog()[key]; l != nil {
		n["name"], n["muscle"], n["isBW"] = l.Name, l.Group, l.BW
		if l.Muscle != "" {
			n["targetKeys"] = []any{l.Muscle}
		}
	}
	return n
}

// setOverride writes a day of the sheet as corrected (nil deletes it): setImportedDay.
func setOverride(key, date string, sets []lset) {
	o := somaObj("logOverrides/" + key)
	if o == nil {
		o = obj{}
	}
	if sets == nil {
		o[date] = nil
	} else {
		var arr []any
		for _, s := range sets {
			so := obj{"weight": s.W, "reps": s.Reps, "failure": s.Fail}
			if s.BW {
				so["bodyweight"], so["added"] = true, s.Added
			}
			arr = append(arr, so)
		}
		o[date] = arr
	}
	somaPut("logOverrides/"+key, o)
}

func muscleName(k string) string {
	if k == "" {
		return "Other"
	}
	return wkTitle(strings.ReplaceAll(k, "_", " "))
}

// --- the page ------------------------------------------------------------------------------------

func (k *wk) training() int {
	su := &k.c.somaUI
	log := trainingLog()
	if key, ok := strings.CutPrefix(su.workSub, "move:"); ok {
		if l := log[key]; l != nil {
			return k.movement(l)
		}
		su.workSub = ""
	}
	if date, ok := strings.CutPrefix(su.workSub, "sess:"); ok {
		return k.session(date, log)
	}
	if i, ok := strings.CutPrefix(su.workSub, "libex:"); ok {
		n, _ := strconv.Atoi(i)
		return k.libExercisePage(n)
	}
	if _, ok := strings.CutPrefix(su.workSub, "pick:"); ok { // exercises picked for today
		k.header([]string{"Personal", "Training ›"}, "Add exercises", "Today's workout · tap to add, tap again to take out", color.RGBA{})
		k.square("wk:libdone:", "chevron-left", wkX1)
		return k.library(236)
	}
	sessions := wkSessions()
	days := map[string]bool{}
	for _, l := range log {
		for d := range l.Days {
			days[d] = true
		}
	}
	for _, s := range sessions {
		days[s.Date] = true
	}
	sub := fmt.Sprintf("%d training days · %d movements", len(days), len(log))
	if loadSeed() == nil {
		sub += " · fetching your imported history…"
	}
	k.header([]string{"Personal", "Training"}, "Training", sub, color.RGBA{})
	x := k.primary("wk:libpick:"+somaToday(), "plus", "Exercise", wkX1)
	k.outline("wk:logsession", "notebook-pen", "Log a session", x-13, 103, 63)
	tab := k.sel("tab", 0)
	k.tabs("wk:tab", []string{"Workout", "Library", "Overview", "Movements", "Sessions"}, []string{"", fmt.Sprint(len(libExercises())), "", fmt.Sprint(len(log)), fmt.Sprint(len(days))}, tab)
	switch tab {
	case 0:
		return k.workout(312)
	case 1:
		return k.library(312)
	case 3:
		return k.movements(log)
	case 4:
		return k.sessionList(log, sessions, days)
	}
	return k.trainingOverview(log, sessions, days)
}

// dayVolume is a training day's volume across movements.
func dayVolume(log map[string]*exLog, d string) (vol float64, sets int) {
	for _, l := range log {
		vol += dayVol(l.Days[d])
		sets += len(l.Days[d])
	}
	return
}

func (k *wk) trainingOverview(log map[string]*exLog, sessions []wkSess, days map[string]bool) int {
	now := time.Now().In(displayZone)
	last28 := lastDays(28)
	var vol []float64
	n28, sets28 := 0, 0
	for _, d := range last28 {
		v, s := dayVolume(log, d)
		vol = append(vol, v)
		if days[d] {
			n28++
			sets28 += s
		}
	}
	tv := 0.0
	for _, v := range vol {
		tv += v
	}
	var all []string
	for d := range days {
		all = append(all, d)
	}
	sort.Strings(all)
	box := k.row4(312)
	k.small(box(0), "dumbbell", "Sessions", fmt.Sprint(n28), "", "last 28 days", wkFG)
	k.small(box(1), "chart-column", "Volume / session", fmtThousands(int(tv/math.Max(float64(n28), 1))), "kg", "lifted, all sets", wkFG)
	k.small(box(2), "list-checks", "Sets", fmt.Sprint(sets28), "", fmt.Sprintf("%.0f a session", float64(sets28)/math.Max(float64(n28), 1)), wkFG)
	lastS, lastF := "—", "nothing logged"
	if len(all) > 0 {
		d := all[len(all)-1]
		lastS, lastF = dayLabel(d), "from your sheet"
		for _, s := range sessions {
			if s.Date == d {
				lastF = s.Split
			}
		}
	}
	k.small(box(3), "calendar", "Last session", lastS, "", lastF, wkFG)
	if len(all) > 0 {
		k.btn("wk:sess:"+all[len(all)-1], k.R(box(3).Min.X, box(3).Min.Y, box(3).Max.X, box(3).Max.Y))
	}
	labels := make([]string, len(last28))
	for i, d := range last28 {
		labels[i] = dayLabel(d)
	}
	vr := image.Rect(wkX0, 496, wkX1, 816)
	k.card(vr)
	k.cardHead(wkX0+28, 540, "chart-column", "Volume", "· kg a day, last 28 days")
	k.bars(image.Rect(wkX0+90, 580, wkX1-28, 770), vol, wkLime, 0, labels, 7, "kg")

	// The week by muscle: sets this week against last week.
	mon := weekStart(now)
	this, prev := map[string]float64{}, map[string]float64{}
	for _, s := range sessions {
		t, _ := time.ParseInLocation("2006-01-02", s.Date, displayZone)
		target := map[string]float64(nil)
		switch {
		case !t.Before(mon):
			target = this
		case !t.Before(mon.AddDate(0, 0, -7)):
			target = prev
		}
		for m, n := range s.Muscles {
			if target != nil {
				target[m] += n
			}
		}
	}
	var ms []string
	for m := range this {
		ms = append(ms, m)
	}
	for m := range prev {
		if _, ok := this[m]; !ok {
			ms = append(ms, m)
		}
	}
	sort.Slice(ms, func(i, j int) bool { return this[ms[i]] > this[ms[j]] || this[ms[i]] == this[ms[j]] && ms[i] < ms[j] })
	y := 840
	mc := image.Rect(wkX0, y, 652, y+96+56*max(min(len(ms), 10), 1))
	k.card(mc)
	k.cardHead(wkX0+28, y+44, "activity", "Sets by muscle", "· this week")
	hi := 1.0
	for _, m := range ms {
		hi = max(hi, this[m], prev[m])
	}
	ry := y + 80
	for i, m := range ms {
		if i == 10 {
			break
		}
		k.text(wkFont("medium", 19), 188, ry+26, wkText2, clip(wkFont("medium", 19), muscleName(m), 150))
		k.hBar(k.R(350, ry+14, 560, ry+24), this[m]/hi, wkLime)
		k.textR(wkFont("semi", 19), 624, ry+26, wkFG, fmt.Sprintf("%g", math.Round(this[m]*10)/10))
		if p := prev[m]; p > 0 {
			k.text(wkFont("regular", 15), 350, ry+46, wkFaint, fmt.Sprintf("last week %g", math.Round(p*10)/10))
		}
		ry += 56
	}
	if len(ms) == 0 {
		k.emptyNote(188, ry+40, "Sessions logged with sets show here.")
	}
	// Records: days that beat every day before them.
	type pr struct {
		name, date string
		e1         float64
	}
	var prs []pr
	for _, l := range log {
		ds := l.dates()
		best := 0.0
		for _, d := range ds {
			v := dayBest(l.Days[d])
			if v > best && best > 0 && d >= wkAddDays(somaToday(), -90) {
				prs = append(prs, pr{l.Name, d, v})
			}
			best = max(best, v)
		}
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].date > prs[j].date })
	pc := image.Rect(672, y, wkX1, y+96+70*max(min(len(prs), 8), 1))
	k.card(pc)
	k.cardHead(700, y+44, "trophy", "Records", "· last 90 days")
	ry = y + 76
	for i, p := range prs {
		if i == 8 {
			break
		}
		ui.Fill(k.img, k.R(674, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 20), 700, ry+32, wkFG, clip(wkFont("semi", 20), p.name, 300))
		k.text(wkFont("regular", 16), 700, ry+56, wkFaint, dayLabel(p.date))
		k.textR(wkFont("bold", 21), 1136, ry+44, wkLime, fmt.Sprintf("%.1f kg", p.e1))
		k.btn("wk:move:"+exerciseKey(p.name), k.R(672, ry, wkX1, ry+70))
		ry += 70
	}
	if len(prs) == 0 {
		k.emptyNote(700, ry+40, "No new bests lately.")
	}
	y = max(mc.Max.Y, pc.Max.Y) + 24

	// Twelve weeks of training days.
	start := weekStart(now).AddDate(0, 0, -7*11)
	cc := image.Rect(wkX0, y, wkX1, y+330)
	k.card(cc)
	k.cardHead(wkX0+28, y+44, "calendar", "Training days", "· 12 weeks, tap one to open it")
	cw := (wkX1 - wkX0 - 160) / 12
	for w := range 12 {
		for d := range 7 {
			day := start.AddDate(0, 0, w*7+d)
			ds := day.Format("2006-01-02")
			x0, y0 := 240+w*cw, y+80+d*34
			r := k.R(x0, y0, x0+cw-8, y0+28)
			c := rgb(0x1e2027)
			if days[ds] {
				v, _ := dayVolume(log, ds)
				c = blend(rgb(0x1e2027), wkLime, 0.35+0.65*min(v/12000, 1))
			}
			if ds > somaToday() {
				c = rgb(0x15161c)
			}
			ui.RoundRect(k.img, r, 6, c)
			if days[ds] {
				k.btn("wk:sess:"+ds, r)
			}
			if d == 0 && (w == 0 || day.Day() <= 7) {
				k.text(wkFont("regular", 15), x0, y+76, wkFaint, day.Format("Jan"))
			}
		}
	}
	for i, l := range []string{"Mon", "", "Wed", "", "Fri", "", "Sun"} {
		k.text(wkFont("regular", 15), 188, y+100+i*34, wkFaint, l)
	}
	return cc.Max.Y + 20
}

// --- movements ---------------------------------------------------------------------------------------

var wkGroups = []string{"All", "Chest", "Back", "Legs", "Shoulders", "Biceps", "Triceps", "Other"}

func (k *wk) movements(log map[string]*exLog) int {
	gi := k.sel("chip", 0)
	k.chips("wk:chip", 308, wkGroups, nil, gi)
	si := k.sel("seg", 0)
	k.segBar("wk:seg", image.Rect(wkX0, 380, 640, 434), []string{"Newest", "Best 1RM", "Heaviest"}, si)
	var list []*exLog
	for _, l := range log {
		g := l.Group
		if !strings.Contains("Chest Back Legs Shoulders Biceps Triceps", g) || g == "" {
			g = "Other"
		}
		if gi > 0 && g != wkGroups[gi] {
			continue
		}
		list = append(list, l)
	}
	last := func(l *exLog) string { ds := l.dates(); return ds[len(ds)-1] }
	sort.Slice(list, func(i, j int) bool {
		switch si {
		case 1:
			a, _ := list[i].best()
			b, _ := list[j].best()
			return a > b
		case 2:
			a, b := 0.0, 0.0
			for _, s := range list[i].Days {
				a = max(a, dayTop(s))
			}
			for _, s := range list[j].Days {
				b = max(b, dayTop(s))
			}
			return a > b
		}
		return last(list[i]) > last(list[j]) || last(list[i]) == last(list[j]) && list[i].Name < list[j].Name
	})
	y := 458
	cr := image.Rect(wkX0, y, wkX1, y+16+96*max(len(list), 1))
	k.card(cr)
	ry := y + 8
	for i, l := range list {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		best, _ := l.best()
		ds := l.dates()
		k.text(wkFont("semi", 22), 188, ry+40, wkFG, clip(wkFont("semi", 22), l.Name, 500))
		k.text(wkFont("regular", 17), 188, ry+70, wkFaint, fmt.Sprintf("%s · %d days · last %s", l.Group, len(ds), dayLabel(ds[len(ds)-1])))
		var sp []float64
		for _, d := range ds[max(0, len(ds)-12):] {
			sp = append(sp, dayBest(l.Days[d]))
		}
		k.spark(k.R(760, ry+30, 900, ry+66), sp, wkLime)
		k.textR(wkFont("bold", 23), 1100, ry+44, wkFG, fmt.Sprintf("%.1f kg", best))
		k.textR(wkFont("regular", 15), 1100, ry+68, wkFaint, "best 1RM")
		k.icon("chevron-right", 1122, ry+48, 20, wkMuted)
		k.btn("wk:move:"+l.Key, k.R(wkX0, ry, wkX1, ry+96))
		ry += 96
	}
	if len(list) == 0 {
		k.emptyNote(188, ry+56, "No movements here yet.")
	}
	return cr.Max.Y + 20
}

func tierOf(best, v float64) (string, color.RGBA) {
	if best <= 0 || v <= 0 {
		return "Light", rgb(0x3ecf8e)
	}
	switch r := v / best; {
	case r >= 0.995:
		return "PR", rgb(0xf06a6a)
	case r >= 0.9:
		return "Near", rgb(0xff9e3d)
	case r >= 0.75:
		return "Mid", rgb(0xe3c55a)
	}
	return "Light", rgb(0x3ecf8e)
}

// movement is one movement's page: its progress and every day it was done.
func (k *wk) movement(l *exLog) int {
	ds := l.dates()
	best, bestAt := l.best()
	heavy := 0.0
	for _, s := range l.Days {
		heavy = max(heavy, dayTop(s))
	}
	k.header([]string{"Personal", "Training ›"}, clip(wkFont("bold", 42), l.Name, 700), fmt.Sprintf("%s · %d days since %s", l.Group, len(ds), dayLabel(ds[0])), color.RGBA{})
	k.square("wk:back", "chevron-left", wkX1)
	box := k.row4(236)
	k.small(box(0), "trophy", "Best 1RM", fmt.Sprintf("%.1f", best), "kg", dayLabel(bestAt), wkLime)
	k.small(box(1), "dumbbell", "Heaviest", fmt.Sprintf("%g", heavy), "kg", "top set", wkFG)
	k.small(box(2), "calendar", "Days", fmt.Sprint(len(ds)), "", "trained it", wkFG)
	k.small(box(3), "history", "Last", dayLabel(ds[len(ds)-1]), "", fmt.Sprintf("%d sets", len(l.Days[ds[len(ds)-1]])), wkFG)
	// Progression.
	metric := k.sel("seg", 1)
	y := 420
	cr := image.Rect(wkX0, y, wkX1, y+380)
	k.card(cr)
	k.cardHead(wkX0+28, y+48, "trending-up", "Progression", "")
	k.segBar("wk:seg", image.Rect(700, y+20, 1136, y+72), []string{"Top set", "Est. 1RM", "Volume"}, metric)
	var vals []float64
	for _, d := range ds {
		switch metric {
		case 0:
			vals = append(vals, dayTop(l.Days[d]))
		case 2:
			vals = append(vals, dayVol(l.Days[d]))
		default:
			vals = append(vals, dayBest(l.Days[d]))
		}
	}
	pr := k.R(270, y+110, 1110, y+320)
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		lo, hi = min(lo, v), max(hi, v)
	}
	pad := max((hi-lo)*0.15, 1)
	lo, hi = max(lo-pad, 0), hi+pad
	for i := 0; i <= 3; i++ {
		v := lo + (hi-lo)*float64(i)/3
		yy := pr.Max.Y - i*pr.Dy()/3
		apTextRight(k.img, wkFont("regular", 16), pr.Min.X-12, yy+6, wkFaint, fmtAxis(math.Round(v)))
		ui.Fill(k.img, image.Rect(pr.Min.X, yy, pr.Max.X, yy+1), rgb(0x1a1b21))
	}
	t0, _ := time.Parse("2006-01-02", ds[0])
	t1, _ := time.Parse("2006-01-02", ds[len(ds)-1])
	span := max(t1.Sub(t0).Hours(), 24)
	pt := func(i int) (int, int) {
		t, _ := time.Parse("2006-01-02", ds[i])
		x := pr.Min.X + int(t.Sub(t0).Hours()/span*float64(pr.Dx()))
		if len(ds) == 1 {
			x = (pr.Min.X + pr.Max.X) / 2
		}
		return x, pr.Max.Y - int((vals[i]-lo)/(hi-lo)*float64(pr.Dy()))
	}
	for i := 1; i < len(vals); i++ {
		x0, y0 := pt(i - 1)
		x1, y1 := pt(i)
		line(k.img, x0, y0, x1, y1, 3, wkLime)
	}
	for i := range vals {
		x, yy := pt(i)
		ui.Circle(k.img, x, yy, 5, wkLime)
	}
	apText(k.img, wkFont("regular", 16), pr.Min.X, pr.Max.Y+30, wkFaint, t0.Format("Jan 2006"))
	apTextRight(k.img, wkFont("regular", 16), pr.Max.X, pr.Max.Y+30, wkFaint, t1.Format("Jan 2006"))
	// Every day, newest first.
	y = cr.Max.Y + 24
	dc := image.Rect(wkX0, y, wkX1, y+80+104*len(ds))
	k.card(dc)
	k.cardHead(wkX0+28, y+48, "list", "Every day", "· tap one to correct it")
	ry := y + 80
	for i := len(ds) - 1; i >= 0; i-- {
		d := ds[i]
		sets := l.Days[d]
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		tier, tc := tierOf(best, dayBest(sets))
		k.text(wkFont("semi", 21), 188, ry+40, wkFG, dayLabel(d)+" "+d[:4])
		src := "from your sheet"
		if _, ok := l.Src[d]; ok {
			src = "logged session"
		}
		k.text(wkFont("regular", 16), 188, ry+68, wkFaint, src)
		var parts []string
		for _, s := range sets {
			parts = append(parts, s.label())
		}
		for li, ln := range wkWrap(wkFont("regular", 19), strings.Join(parts, "; "), 600, 2) {
			k.text(wkFont("regular", 19), 400, ry+40+li*28, wkText2, ln)
		}
		k.badge(1136, ry+40, tier, tc)
		k.textR(wkFont("regular", 16), 1136, ry+82, wkFaint, fmt.Sprintf("%.1f kg 1RM", dayBest(sets)))
		k.btn("wk:sheet-open:lday:"+l.Key+"|"+d, k.R(wkX0, ry, wkX1, ry+104))
		ry += 104
	}
	return dc.Max.Y + 20
}

// --- sessions --------------------------------------------------------------------------------------

func (k *wk) sessionList(log map[string]*exLog, sessions []wkSess, days map[string]bool) int {
	byDate := map[string]wkSess{}
	for _, s := range sessions {
		byDate[s.Date] = s
	}
	var all []string
	for d := range days {
		all = append(all, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(all)))
	shown := 40 * (1 + k.sel("more", 0))
	y := 312
	month := ""
	for i, d := range all {
		if i == shown {
			k.boxBtn(fmt.Sprintf("wk:more:%d", k.sel("more", 0)+1), wkX0, y, wkX1, y+64, "chevron-down", fmt.Sprintf("Show %d more", min(40, len(all)-shown)), color.RGBA{}, wkFG)
			y += 84
			break
		}
		if m := d[:7]; m != month {
			month = m
			t, _ := time.Parse("2006-01", m)
			k.text(wkFont("semi", 19), wkX0+8, y+30, wkMuted, strings.ToUpper(t.Format("January 2006")))
			y += 46
		}
		r := image.Rect(wkX0, y, wkX1, y+100)
		k.card(r)
		vol, sets := dayVolume(log, d)
		t, _ := time.Parse("2006-01-02", d)
		k.textC(wkFont("bold", 30), 214, y+52, wkFG, t.Format("2"))
		k.textC(wkFont("semi", 15), 214, y+76, wkFaint, strings.ToUpper(t.Format("Mon")))
		title, sub := "From your sheet", ""
		n := 0
		for _, l := range log {
			if len(l.Days[d]) > 0 {
				n++
			}
		}
		if s, ok := byDate[d]; ok {
			title = s.Split
			if title == "" {
				title = "Session"
			}
			if s.Duration != "" && s.Duration != "0:00" {
				sub = s.Duration + " · "
			}
			n = max(n, len(s.Ex))
		}
		sub += fmt.Sprintf("%d movements · %d sets", n, sets)
		k.text(wkFont("semi", 23), 270, y+44, wkFG, clip(wkFont("semi", 23), title, 560))
		k.text(wkFont("regular", 18), 270, y+74, wkFaint, sub)
		k.textR(wkFont("bold", 22), 1100, y+56, wkFG, fmtThousands(int(vol))+" kg")
		k.icon("chevron-right", 1122, y+50, 20, wkMuted)
		k.btn("wk:sess:"+d, k.R(wkX0, y, wkX1, y+100))
		y += 112
	}
	if len(all) == 0 {
		k.emptyNote(wkX0+20, y+40, "No training yet. Log a session, or wait for your imported history.")
		y += 80
	}
	return y
}

// session is a training day: its exercises and their sets, each one editable.
func (k *wk) session(date string, log map[string]*exLog) int {
	o := somaObj("history/" + date)
	t, _ := time.Parse("2006-01-02", date)
	if o == nil { // a day from the sheet only
		k.header([]string{"Personal", "Training ›"}, "From your sheet", t.Format("Monday 2 January 2006"), color.RGBA{})
		k.square("wk:back", "chevron-left", wkX1)
		var ls []*exLog
		for _, l := range log {
			if len(l.Days[date]) > 0 {
				ls = append(ls, l)
			}
		}
		sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
		y := 240
		for _, l := range ls {
			sets := l.Days[date]
			cr := image.Rect(wkX0, y, wkX1, y+96+56*len(sets))
			k.card(cr)
			k.text(wkFont("semi", 23), 188, y+48, wkFG, l.Name)
			k.text(wkFont("regular", 17), 188, y+74, wkFaint, l.Group)
			k.outline("wk:sheet-open:lday:"+l.Key+"|"+date, "pencil", "Edit", 1136, y+22, 50)
			ry := y + 92
			for i, s := range sets {
				k.text(wkFont("regular", 20), 188, ry+30, wkFaint, fmt.Sprint(i+1))
				k.text(wkFont("semi", 21), 240, ry+30, wkFG, s.label())
				ry += 56
			}
			y = cr.Max.Y + 20
		}
		if len(ls) == 0 {
			k.emptyNote(wkX0+20, y+40, "Nothing logged on this day.")
		}
		return y
	}
	s := parseSession(date, o)
	title := s.Split
	if title == "" {
		title = "Session"
	}
	k.header([]string{"Personal", "Training ›"}, clip(wkFont("bold", 42), title, 640), t.Format("Monday 2 January 2006"), color.RGBA{})
	k.btn("wk:type-open:srename:"+date, k.R(wkX0, 110, 800, 166))
	x := k.square("wk:sheet-open:sess:"+date, "ellipsis", wkX1)
	k.square("wk:back", "chevron-left", x-13)
	box := k.row4(236)
	k.small(box(0), "chart-column", "Volume", fmtThousands(int(s.Vol)), "kg", "working sets", wkFG)
	k.small(box(1), "list-checks", "Sets", fmt.Sprint(s.Sets), "", fmt.Sprintf("%d movements", len(s.Ex)), wkFG)
	dur := s.Duration
	if dur == "" {
		dur = "0:00"
	}
	k.small(box(2), "timer", "Time", dur, "", "tap ⋯ to change it", wkFG)
	k.small(box(3), "flame", "Burned", fmt.Sprintf("%.0f", s.Cals), "kcal", "estimated", wkFG)
	y := 420
	for xi, ex := range s.Ex {
		cr := image.Rect(wkX0, y, wkX1, y+124+64*len(ex.Sets)+80)
		k.card(cr)
		k.text(wkFont("semi", 24), 188, y+48, wkFG, clip(wkFont("semi", 24), ex.Name, 640))
		k.btn("wk:move:"+exerciseKey(ex.Name), k.R(wkX0, y, 860, y+90))
		sub := ex.Muscle
		if ex.BW {
			sub += " · bodyweight (+ added)"
		}
		k.text(wkFont("regular", 17), 188, y+76, wkFaint, sub)
		k.outline(fmt.Sprintf("wk:sheet-open:hex:%s:%d", date, xi), "ellipsis", "", 1136, y+22, 50)
		hf := wkFont("semi", 15)
		k.text(hf, 188, y+112, wkFaint, "SET")
		k.text(hf, 260, y+112, wkFaint, "KG")
		k.text(hf, 420, y+112, wkFaint, "REPS")
		k.text(hf, 580, y+112, wkFaint, "TYPE")
		k.textR(hf, 1136, y+112, wkFaint, "EST. 1RM")
		ry := y + 124
		for si, st := range ex.Sets {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			fg := wkFG
			if !st.Done {
				fg = wkFaint
			}
			k.text(wkFont("semi", 20), 192, ry+40, wkMuted, fmt.Sprint(si+1))
			k.text(wkFont("bold", 22), 260, ry+40, fg, fmt.Sprintf("%g", st.W))
			k.text(wkFont("bold", 22), 420, ry+40, fg, fmt.Sprintf("%g", st.Reps))
			typ := map[string]string{"warmup": "Warm-up", "dropset": "Drop set", "feeder": "Feeder", "stretch": "Stretch"}[st.Type]
			if !st.Done {
				typ = strings.TrimPrefix(typ+" · not done", " · ")
			}
			k.text(wkFont("regular", 18), 580, ry+40, wkMuted, typ)
			e1 := lset{W: st.W, Reps: st.Reps}.e1rm()
			if e1 > 0 {
				k.textR(wkFont("medium", 19), 1100, ry+40, wkText2, fmt.Sprintf("%.1f", e1))
			}
			k.icon("pencil", 1124, ry+33, 20, wkFaint)
			k.btn(fmt.Sprintf("wk:sheet-open:hset:%s:%d:%d", date, xi, si), k.R(wkX0, ry, wkX1, ry+64))
			ry += 64
		}
		ar := k.R(188, ry+12, 460, ry+60)
		ui.RoundRect(k.img, ar, 12, rgb(0x191a21))
		lucideIcon(k.img, "plus", ar.Min.X+30, (ar.Min.Y+ar.Max.Y)/2, 22, wkMuted)
		apText(k.img, wkFont("medium", 19), ar.Min.X+52, (ar.Min.Y+ar.Max.Y)/2+7, wkMuted, "Add a set")
		k.btn(fmt.Sprintf("wk:addset:%s:%d", date, xi), ar)
		y = cr.Max.Y + 20
	}
	k.boxBtn("wk:type-open:addex:"+date, wkX0, y, wkX1, y+70, "plus", "Add an exercise", wkLime, wkOnLime)
	return y + 90
}

// --- sheets, actions, fields ----------------------------------------------------------------------

func init() {
	wkActions["move"] = func(c *console, key string) { c.wkGo("training", "move:"+key) }
	wkActions["sess"] = func(c *console, date string) {
		if date != "" {
			c.wkGo("training", "sess:"+date)
		}
	}
	wkActions["more"] = func(c *console, n string) {
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel[c.somaUI.work+"wk:more"] = n
	}
	wkActions["logsession"] = func(c *console, _ string) {
		today := somaToday()
		if somaObj("history/"+today) != nil {
			c.wkGo("training", "sess:"+today)
			return
		}
		c.wkType("newsession:"+today, "What you trained, e.g. Push A", "")
	}
	wkTypers["newsession"] = func(c *console, date, text string) {
		if text == "" {
			return
		}
		at := nowMs()
		if date != somaToday() { // a day logged after the fact: midday on it
			if d, err := time.ParseInLocation("2006-01-02", date, displayZone); err == nil {
				at = float64(d.Add(12 * time.Hour).UnixMilli())
			}
		}
		somaPut("history/"+date, obj{"timestamp": at, "split": text, "durationFormatted": "0:00", "caloriesBurned": 0,
			"totalVol": 0, "totalSets": 0, "axialVol": 0, "exercises": []any{}, "muscles": obj{}})
		c.wkGo("training", "sess:"+date)
		c.wkType("addex:"+date, "An exercise, e.g. Incline Bench Press", "")
	}
	wkPrompts["addex"] = func(string) (string, string) { return "An exercise, e.g. Incline Bench Press", "" }
	wkTypers["addex"] = func(c *console, date, text string) {
		if text == "" {
			return
		}
		ex := newExercise(text)
		patchSession(date, func(o obj) {
			exs, _ := o["exercises"].([]any)
			o["exercises"] = append(exs, ex)
		})
		// A first set, from what was done last time.
		exs, _ := somaObj("history/" + date)["exercises"].([]any)
		addSet(date, len(exs)-1)
	}
	wkPrompts["srename"] = func(date string) (string, string) {
		return "What it's called", str(somaObj("history/" + date)["split"])
	}
	wkTypers["srename"] = func(c *console, date, text string) {
		if text != "" {
			patchSession(date, func(o obj) { o["split"] = text })
		}
	}
	wkActions["addset"] = func(c *console, arg string) {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		addSet(date, xi)
	}

	// A set of a session.
	wkSheets["hset"] = func(k *wk, arg string) *wkSheet {
		parts := strings.Split(arg, ":")
		if len(parts) != 3 {
			return nil
		}
		date := parts[0]
		xi, _ := strconv.Atoi(parts[1])
		si, _ := strconv.Atoi(parts[2])
		eo := sessExercise(somaObj("history/"+date), xi)
		so := exSet(eo, si)
		if so == nil {
			return nil
		}
		w := so["weight"]
		if w == nil {
			w = so["kg"]
		}
		done, ok := so["done"].(bool)
		done = done || !ok
		typ := str(so["type"])
		if typ == "" {
			typ = "normal"
		}
		var types []wkOpt
		for _, t := range []struct{ id, l string }{{"normal", "Working"}, {"warmup", "Warm-up"}, {"dropset", "Drop set"}} {
			types = append(types, wkOpt{id: "wk:hsetv:" + arg + ":type:" + t.id, label: t.l, sel: typ == t.id})
		}
		sh := &wkSheet{title: fmt.Sprintf("%s · set %d", str(eo["name"]), si+1), sub: wkDate(date), done: "Done", rows: []wkRow{
			{kind: "stepper", label: "Weight (kg)", detail: fmt.Sprintf("%g", num(w)), id: "wk:type-open:hsetw:" + arg, minus: "wk:hsetv:" + arg + ":weight:-2.5", plus: "wk:hsetv:" + arg + ":weight:2.5"},
			{kind: "stepper", label: "Reps", detail: fmt.Sprintf("%g", num(so["reps"])), id: "wk:type-open:hsetr:" + arg, minus: "wk:hsetv:" + arg + ":reps:-1", plus: "wk:hsetv:" + arg + ":reps:1"},
			{kind: "seg", label: "Kind", opts: types},
			{kind: "seg", label: "Done", opts: []wkOpt{{id: "wk:hsetv:" + arg + ":done:1", label: "Done", sel: done}, {id: "wk:hsetv:" + arg + ":done:0", label: "Not done", sel: !done}}},
			{id: "wk:hsetdel:" + arg, label: "Delete set", icon: "trash-2", danger: true},
		}}
		return sh
	}
	wkActions["hsetv"] = func(c *console, arg string) {
		parts := strings.Split(arg, ":")
		if len(parts) != 5 {
			return
		}
		date := parts[0]
		xi, _ := strconv.Atoi(parts[1])
		si, _ := strconv.Atoi(parts[2])
		field, v := parts[3], parts[4]
		patchSession(date, func(o obj) {
			so := exSet(sessExercise(o, xi), si)
			if so == nil {
				return
			}
			switch field {
			case "weight", "reps":
				d, _ := strconv.ParseFloat(v, 64)
				cur := so[field]
				if field == "weight" && cur == nil {
					cur = so["kg"]
				}
				so[field] = max(num(cur)+d, 0)
				delete(so, "kg")
			case "type":
				so["type"] = v
			case "done":
				so["done"] = v == "1"
			}
		})
		c.somaUI.keep = true
	}
	setTyped := func(field string) func(c *console, arg, text string) {
		return func(c *console, arg, text string) {
			parts := strings.Split(arg, ":")
			v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
			if len(parts) != 3 || err != nil || v < 0 {
				return
			}
			xi, _ := strconv.Atoi(parts[1])
			si, _ := strconv.Atoi(parts[2])
			patchSession(parts[0], func(o obj) {
				if so := exSet(sessExercise(o, xi), si); so != nil {
					so[field] = v
					delete(so, "kg")
				}
			})
		}
	}
	wkTypers["hsetw"], wkTypers["hsetr"] = setTyped("weight"), setTyped("reps")
	wkPrompts["hsetw"] = func(string) (string, string) { return "Weight in kg", "" }
	wkPrompts["hsetr"] = func(string) (string, string) { return "Reps", "" }
	wkActions["hsetdel"] = func(c *console, arg string) {
		parts := strings.Split(arg, ":")
		if len(parts) != 3 {
			return
		}
		date := parts[0]
		xi, _ := strconv.Atoi(parts[1])
		si, _ := strconv.Atoi(parts[2])
		before := somaObj("history/" + date)
		patchSession(date, func(o obj) {
			eo := sessExercise(o, xi)
			ss, _ := eo["sets"].([]any)
			if si < len(ss) {
				eo["sets"] = append(ss[:si], ss[si+1:]...)
			}
		})
		c.offerUndo("Set deleted", func() { somaPut("history/"+date, before) })
	}

	// An exercise of a session.
	wkSheets["hex"] = func(k *wk, arg string) *wkSheet {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		o := somaObj("history/" + date)
		eo := sessExercise(o, xi)
		if eo == nil {
			return nil
		}
		exs, _ := o["exercises"].([]any)
		sh := &wkSheet{title: str(eo["name"]), sub: wkDate(date), rows: []wkRow{
			{id: "wk:move:" + exerciseKey(str(eo["name"])), label: "Its history", icon: "trending-up"},
			{id: "wk:addset:" + arg, label: "Add a set", icon: "plus"},
		}}
		if xi > 0 {
			sh.rows = append(sh.rows, wkRow{id: "wk:hexmove:" + arg + ":-1", label: "Move up", icon: "arrow-up"})
		}
		if xi < len(exs)-1 {
			sh.rows = append(sh.rows, wkRow{id: "wk:hexmove:" + arg + ":1", label: "Move down", icon: "arrow-down"})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:hexdel:" + arg, label: "Remove exercise", icon: "trash-2", danger: true})
		return sh
	}
	wkActions["hexmove"] = func(c *console, arg string) {
		parts := strings.Split(arg, ":")
		if len(parts) != 3 {
			return
		}
		xi, _ := strconv.Atoi(parts[1])
		d, _ := strconv.Atoi(parts[2])
		patchSession(parts[0], func(o obj) {
			exs, _ := o["exercises"].([]any)
			if j := xi + d; xi >= 0 && j >= 0 && j < len(exs) && xi < len(exs) {
				exs[xi], exs[j] = exs[j], exs[xi]
			}
		})
	}
	wkActions["hexdel"] = func(c *console, arg string) {
		date, xs, _ := strings.Cut(arg, ":")
		xi, _ := strconv.Atoi(xs)
		before := somaObj("history/" + date)
		patchSession(date, func(o obj) {
			exs, _ := o["exercises"].([]any)
			if xi < len(exs) {
				o["exercises"] = append(exs[:xi], exs[xi+1:]...)
			}
		})
		c.offerUndo("Exercise removed", func() { somaPut("history/"+date, before) })
	}

	// The session itself.
	wkSheets["sess"] = func(k *wk, date string) *wkSheet {
		o := somaObj("history/" + date)
		if o == nil {
			return nil
		}
		dur := str(o["durationFormatted"])
		if dur == "" {
			dur = "0:00"
		}
		return &wkSheet{title: str(o["split"]), sub: wkDate(date), rows: []wkRow{
			{id: "wk:type-open:srename:" + date, label: "Rename", icon: "pencil", detail: str(o["split"])},
			{kind: "stepper", label: "Time (min:sec)", detail: dur, minus: "wk:sdur:" + date + ":-5", plus: "wk:sdur:" + date + ":5"},
			{id: "wk:sdate:" + date, label: "Day", icon: "calendar", detail: wkDate(date)},
			{id: "wk:sdel:" + date, label: "Delete session", icon: "trash-2", danger: true},
		}}
	}
	wkActions["sdur"] = func(c *console, arg string) {
		date, d, _ := strings.Cut(arg, ":")
		n, _ := strconv.Atoi(d)
		patchSession(date, func(o obj) {
			m := (&somaSession{Duration: str(o["durationFormatted"])}).minutes()
			m = max(math.Round(m)+float64(n), 0)
			o["durationFormatted"] = fmt.Sprintf("%d:00", int(m))
			o["caloriesBurned"] = calories(m, num(o["totalVol"]), int(num(o["totalSets"])))
		})
		c.somaUI.keep = true
	}
	wkActions["sdel"] = func(c *console, date string) {
		before := somaObj("history/" + date)
		soma.put("history/"+date, "")
		c.wkGo("training", "")
		c.offerUndo("Session deleted", func() {
			if somaObj("history/"+date) == nil {
				somaPut("history/"+date, before)
			}
		})
	}
	wkActions["sdate"] = func(c *console, date string) { c.wkPickDate("sdate:"+date, date, "Move the session to") }
	wkDateRequired["sdate"] = true
	wkDateSetters["sdate"] = func(c *console, from, to string) {
		if to == "" || to == from {
			return
		}
		if somaObj("history/"+to) != nil {
			c.toastMsg(wkDate(to) + " already has a session")
			return
		}
		o := somaObj("history/" + from)
		at := msTime(o["timestamp"]).In(displayZone)
		d, _ := time.ParseInLocation("2006-01-02", to, displayZone)
		o["timestamp"] = float64(d.Add(time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute).UnixMilli())
		somaPut("history/"+to, o)
		soma.put("history/"+from, "")
		c.wkGo("training", "sess:"+to)
	}

	// A day of a movement: its sets, corrected where they were logged.
	wkSheets["lday"] = func(k *wk, arg string) *wkSheet {
		key, date, _ := strings.Cut(arg, "|")
		l := trainingLog()[key]
		if l == nil || len(l.Days[date]) == 0 {
			return nil
		}
		if xi, ok := l.Src[date]; ok { // a session's: open it there
			return &wkSheet{title: l.Name, sub: wkDate(date) + " · from a logged session", rows: []wkRow{
				{id: "wk:sess:" + date, label: "Open the session to change its sets", icon: "pencil"},
				{id: fmt.Sprintf("wk:hexdel:%s:%d", date, xi), label: "Remove it from that session", icon: "trash-2", danger: true},
			}}
		}
		sh := &wkSheet{title: l.Name, sub: wkDate(date) + " · from your sheet", done: "Done"}
		for i, s := range l.Days[date] {
			id := fmt.Sprintf("wk:ldayv:%s|%s|%d", key, date, i)
			sh.rows = append(sh.rows,
				wkRow{kind: "stepper", label: fmt.Sprintf("Set %d kg", i+1), detail: fmt.Sprintf("%g", math.Round(s.W*10)/10), minus: id + "|w|-2.5", plus: id + "|w|2.5"},
				wkRow{kind: "stepper", label: "     reps", detail: fmt.Sprintf("%g", s.Reps), minus: id + "|r|-1", plus: id + "|r|1"})
		}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:ldayv:" + arg + "|add|0", label: "Add a set", icon: "plus"},
			wkRow{id: "wk:ldayv:" + arg + "|pop|0", label: "Remove the last set", icon: "minus"},
			wkRow{id: "wk:ldaydel:" + arg, label: "Delete this day", icon: "trash-2", danger: true})
		return sh
	}
	wkActions["ldayv"] = func(c *console, arg string) {
		p := strings.Split(arg, "|")
		c.somaUI.keep = true
		if len(p) < 4 {
			return
		}
		key, date := p[0], p[1]
		l := trainingLog()[key]
		if l == nil {
			return
		}
		sets := append([]lset(nil), l.Days[date]...)
		switch {
		case p[2] == "add" && len(sets) > 0:
			sets = append(sets, sets[len(sets)-1])
		case p[2] == "pop" && len(sets) > 1:
			sets = sets[:len(sets)-1]
		case len(p) == 5:
			i, _ := strconv.Atoi(p[2])
			d, _ := strconv.ParseFloat(p[4], 64)
			if i < 0 || i >= len(sets) {
				return
			}
			if p[3] == "w" {
				sets[i].W = max(sets[i].W+d, 0)
				if sets[i].BW {
					sets[i].Added += d
				}
			} else {
				sets[i].Reps = max(sets[i].Reps+d, 0)
			}
		default:
			return
		}
		setOverride(key, date, sets)
	}
	wkActions["ldaydel"] = func(c *console, arg string) {
		key, date, _ := strings.Cut(arg, "|")
		before := somaObj("logOverrides/" + key)
		setOverride(key, date, nil)
		c.offerUndo("Day deleted", func() {
			if before == nil {
				soma.put("logOverrides/"+key, "")
			} else {
				somaPut("logOverrides/"+key, before)
			}
		})
	}
}

// addSet adds a set to a session's exercise: a copy of its last, else what was done last
// time, else an empty one to fill in.
func addSet(date string, xi int) {
	patchSession(date, func(o obj) {
		eo := sessExercise(o, xi)
		if eo == nil {
			return
		}
		ss, _ := eo["sets"].([]any)
		set := obj{"weight": 0, "reps": 0, "failure": 3, "done": true, "type": "normal"}
		if len(ss) > 0 {
			if last, ok := ss[len(ss)-1].(obj); ok {
				w := last["weight"]
				if w == nil {
					w = last["kg"]
				}
				set["weight"], set["reps"] = num(w), num(last["reps"])
			}
		} else if l := trainingLog()[exerciseKey(str(eo["name"]))]; l != nil {
			ds := l.dates()
			for i := len(ds) - 1; i >= 0; i-- {
				if ds[i] < date && len(l.Days[ds[i]]) > 0 {
					top := l.Days[ds[i]][0]
					w := top.W
					if top.BW {
						w = top.Added
					}
					set["weight"], set["reps"] = w, top.Reps
					break
				}
			}
		}
		eo["sets"] = append(ss, set)
	})
}
