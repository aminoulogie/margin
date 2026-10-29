package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Soma's data, read out of the synced records (somasync.go) and changed the way Soma's own
// store changes it (src/lib/store.ts), so the PC and the phone see the same thing:
//   habits#<id>, habits@order  a habit: name, colour, history{date: done}, steps, stepLog
//   todos#<id>, todos@order    a to-do: text, done, date, scope "day"|"week", due, slot
//   dayPlans/<date>            the day's time blocks: label, hours, colour, start
//   reading/<date>             minutes read
//   history/<date>             a training session: split, durationFormatted, totalSets...
//   nutrition/<date>           food items (cals, p, c, f), goals, water, bodyWeight

type obj = map[string]any

func somaDate(t time.Time) string { return t.In(displayZone).Format("2006-01-02") }
func somaToday() string           { return somaDate(time.Now()) }

// mondayOf is the Monday starting the week date falls in (todos.ts).
func mondayOf(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)).Format("2006-01-02")
}

func somaObj(key string) obj {
	var o obj
	if s := soma.get(key); s != "" {
		json.Unmarshal([]byte(s), &o)
	}
	return o
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func str(v any) string { s, _ := v.(string); return s }

// list is the items of an id'd array field, in its order (items the order doesn't name last).
func somaList(field string) []obj {
	var order []string
	json.Unmarshal([]byte(soma.get(field+"@order")), &order)
	seen := map[string]bool{}
	var out []obj
	for _, id := range order {
		if o := somaObj(field + "#" + id); o != nil && !seen[id] {
			out = append(out, o)
			seen[id] = true
		}
	}
	var rest []string
	for _, k := range soma.keysWith(field + "#") {
		if id := strings.TrimPrefix(k, field+"#"); !seen[id] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		if o := somaObj(k); o != nil {
			out = append(out, o)
			seen[strings.TrimPrefix(k, field+"#")] = true
		}
	}
	// Soma sends a list as one record when it can't split it (an item without an id, or two
	// with the same one: records.ts, idArray); its items are read from there.
	if whole := soma.get(field); strings.HasPrefix(strings.TrimSpace(whole), "[") {
		var items []any
		json.Unmarshal([]byte(whole), &items)
		for _, it := range items {
			o, _ := it.(obj)
			if o == nil {
				continue
			}
			if id := str(o["id"]); id == "" || !seen[id] {
				out = append(out, o)
				if id != "" {
					seen[id] = true
				}
			}
		}
	}
	return out
}

func somaPut(key string, o any) {
	b, _ := json.Marshal(o)
	soma.put(key, string(b))
}

// somaNewID is Soma's newId(): the time in base 36 and six random base-36 characters.
func somaNewID() string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(36))
		b[i] = digits[n.Int64()]
	}
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + string(b)
}

// --- habits ---------------------------------------------------------------------------------

type somaHabit struct {
	ID, Name, Color string
	o               obj
}

func somaHabits() []somaHabit {
	var out []somaHabit
	for _, o := range somaList("habits") {
		out = append(out, somaHabit{ID: str(o["id"]), Name: str(o["name"]), Color: str(o["color"]), o: o})
	}
	return out
}

func (h somaHabit) done(date string) bool {
	hist, _ := h.o["history"].(obj)
	b, _ := hist[date].(bool)
	return b
}

// ramp: a number that moves every day; ticked on the phone or the PC, where it's set.
func (h somaHabit) ramp() bool { return h.o["ramp"] != nil }

// somaToggleHabit is Soma's toggleHabit for today (setAll in habit-steps.ts).
func somaToggleHabit(id string) error {
	o := somaObj("habits#" + id)
	if o == nil {
		return fmt.Errorf("no such habit")
	}
	if o["ramp"] != nil {
		return fmt.Errorf("this habit counts an amount: log it in Soma on the phone or the PC")
	}
	date := somaToday()
	hist, _ := o["history"].(obj)
	if hist == nil {
		hist = obj{}
	}
	on, _ := hist[date].(bool)
	on = !on
	hist[date] = on
	o["history"] = hist
	if steps, ok := o["steps"].([]any); ok && len(steps) > 0 {
		log, _ := o["stepLog"].(obj)
		if log == nil {
			log = obj{}
		}
		if on {
			day := obj{}
			for _, s := range steps {
				if so, ok := s.(obj); ok {
					day[str(so["id"])] = max(num(so["target"]), 1)
				}
			}
			log[date] = day
		} else {
			delete(log, date)
		}
		o["stepLog"] = log
	}
	somaPut("habits#"+id, o)
	return nil
}

// --- to-dos ---------------------------------------------------------------------------------

type somaTodo struct {
	ID, Text, Date, Scope, Due string
	Priority                   string // "high", "low", or "" (Soma: TodoItem.priority)
	Done, Cleared              bool
	SlotDate                   string
	SlotStart, SlotMins        int
}

func somaTodos() []somaTodo {
	var out []somaTodo
	for _, o := range somaList("todos") {
		t := somaTodo{ID: str(o["id"]), Text: str(o["text"]), Date: str(o["date"]), Scope: str(o["scope"]), Due: str(o["due"]), Priority: str(o["priority"])}
		t.Done, _ = o["done"].(bool)
		t.Cleared, _ = o["cleared"].(bool)
		if t.Scope != "week" {
			t.Scope = "day"
		}
		if s, ok := o["slot"].(obj); ok {
			t.SlotDate, t.SlotStart, t.SlotMins = str(s["date"]), int(num(s["start"])), int(num(s["mins"]))
		}
		out = append(out, t)
	}
	return out
}

// active: on the list you'd see on date (todos.ts isActive).
func (t somaTodo) active(date string) bool {
	if t.Cleared {
		return false
	}
	if t.Scope == "week" {
		return mondayOf(t.Date) == mondayOf(date)
	}
	return t.Date == date
}

// on: belongs to the day date in the calendar (its day, its time slot, or its deadline).
func (t somaTodo) on(date string) bool {
	return (t.Scope == "day" && t.Date == date) || t.SlotDate == date || t.Due == date
}

func somaToggleTodo(id string) {
	o := somaObj("todos#" + id)
	if o == nil {
		return
	}
	d, _ := o["done"].(bool)
	o["done"] = !d
	somaPut("todos#"+id, o)
}

// somaAddTodo is Soma's addTodo (on date, which is today unless planned from the calendar).
func somaAddTodo(text, scope, date string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	id := somaNewID()
	somaPut("todos#"+id, obj{"id": id, "text": text, "done": false, "date": date, "scope": scope})
	var order []string
	for _, t := range somaList("todos") {
		order = append(order, str(t["id"]))
	}
	somaPut("todos@order", order)
}

// somaAddTodoAt is Soma's addTodoAt: a to-do put on a day at a time (start and length in
// minutes from midnight), on that day's list (setTodoSlot, with timeline.ts's cleanSlot).
func somaAddTodoAt(text, date string, start, mins int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	const day, step = 24 * 60, 15
	start = min(max((start+step/2)/step*step, 0), day-step)
	mins = min(max((mins+step/2)/step*step, step), day-start)
	id := somaNewID()
	somaPut("todos#"+id, obj{"id": id, "text": text, "done": false, "date": date, "scope": "day",
		"cleared": false, "slot": obj{"date": date, "start": start, "mins": mins}})
	var order []string
	for _, t := range somaList("todos") {
		order = append(order, str(t["id"]))
	}
	somaPut("todos@order", order)
}

// --- reading, plans, reports ----------------------------------------------------------------

func somaReading(date string) int { return int(num(jsonValue(soma.get("reading/" + date)))) }

func jsonValue(s string) any {
	var v any
	json.Unmarshal([]byte(s), &v)
	return v
}

func somaAddReading(date string, mins int) {
	n := min(max(somaReading(date)+mins, 0), 24*60)
	if n == 0 {
		soma.put("reading/"+date, "")
		return
	}
	soma.put("reading/"+date, strconv.Itoa(n))
}

type somaBlock struct {
	Label, Color string
	Start, Hours float64 // hours from midnight; the start of the day's ring
}

// somaPlan is the day's blocks with their clock times: one block may be pinned to a time
// (day-plan.ts: at most one carries start), and the rest follow it around the 24 hours.
func somaPlan(date string) []somaBlock {
	var raw []obj
	json.Unmarshal([]byte(soma.get("dayPlans/"+date)), &raw)
	if len(raw) == 0 {
		return nil
	}
	first := 0.0
	acc := 0.0
	for _, b := range raw {
		if s, ok := b["start"].(float64); ok {
			first = s - acc
			break
		}
		acc += num(b["hours"])
	}
	var out []somaBlock
	at := first
	for _, b := range raw {
		h := num(b["hours"])
		out = append(out, somaBlock{Label: str(b["label"]), Color: str(b["color"]), Start: wrap24(at), Hours: h})
		at += h
	}
	return out
}

func wrap24(h float64) float64 {
	for h < 0 {
		h += 24
	}
	for h >= 24 {
		h -= 24
	}
	return h
}

func clockOf(h float64) string {
	m := int(h*60+0.5) % (24 * 60)
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

type somaSession struct {
	Date, Split, Duration string
	Sets                  int
	Volume, Calories      float64
	Ended                 time.Time // when it was saved (Train's timestamp); zero if unknown
}

// minutes is how long it ran: "45:30" is 45.5 minutes (timeline.ts, durationMinutes).
func (s *somaSession) minutes() float64 {
	var m, sec int
	if n, _ := fmt.Sscanf(s.Duration, "%d:%d", &m, &sec); n == 2 && sec < 60 {
		return float64(m) + float64(sec)/60
	}
	return 0
}

func somaTraining(date string) *somaSession {
	o := somaObj("history/" + date)
	if o == nil {
		return nil
	}
	s := &somaSession{Date: date, Split: str(o["split"]), Duration: str(o["durationFormatted"]),
		Sets: int(num(o["totalSets"])), Volume: num(o["totalVol"]), Calories: num(o["caloriesBurned"])}
	if ts := num(o["timestamp"]); ts > 0 {
		s.Ended = time.UnixMilli(int64(ts)).In(displayZone)
	}
	return s
}

type somaFood struct {
	Date                       string
	Cals, P, C, F              float64
	GoalCals, GoalP, Water, Kg float64
	Logged                     bool
}

func somaNutrition(date string) somaFood {
	o := somaObj("nutrition/" + date)
	n := somaFood{Date: date}
	if o == nil {
		return n
	}
	items, _ := o["items"].([]any)
	for _, it := range items {
		if f, ok := it.(obj); ok {
			n.Cals += num(f["cals"])
			n.P += num(f["p"])
			n.C += num(f["c"])
			n.F += num(f["f"])
		}
	}
	n.Logged = len(items) > 0
	if g, ok := o["goals"].(obj); ok {
		n.GoalCals, n.GoalP = num(g["cals"]), num(g["protein"])
	}
	n.Water, n.Kg = num(o["water"]), num(o["bodyWeight"])
	return n
}

// days is the n days ending on date, oldest first.
func days(date string, n int) []string {
	t, _ := time.Parse("2006-01-02", date)
	out := make([]string, n)
	for i := range out {
		out[i] = t.AddDate(0, 0, i-n+1).Format("2006-01-02")
	}
	return out
}

// --- goals and projects (life-goals.ts, projects.ts) ----------------------------------------

// somaPeriod is Soma's periodOf: "2026", "2026-10", "2026-W44" (ISO week).
func somaPeriod(horizon string, t time.Time) string {
	t = t.In(displayZone)
	switch horizon {
	case "year":
		return strconv.Itoa(t.Year())
	case "month":
		return t.Format("2006-01")
	}
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

type somaGoal struct {
	ID, Title, Horizon, Unit, Color string
	Target, Progress                float64
	Done                            bool
}

// somaGoalsNow is this week's, month's and year's goals, in Soma's order.
func somaGoalsNow(horizon string) []somaGoal {
	period := somaPeriod(horizon, time.Now())
	var out []somaGoal
	for _, o := range somaList("goals") {
		if str(o["horizon"]) != horizon || str(o["period"]) != period {
			continue
		}
		g := somaGoal{ID: str(o["id"]), Title: str(o["title"]), Horizon: horizon, Unit: str(o["unit"]),
			Color: str(o["color"]), Target: num(o["target"]), Progress: num(o["progress"])}
		g.Done, _ = o["done"].(bool)
		out = append(out, g)
	}
	return out
}

// somaGoalTap ticks a plain goal, or logs delta towards a counted one (logProgress).
func somaGoalTap(id string, delta float64) {
	o := somaObj("goals#" + id)
	if o == nil {
		return
	}
	now := time.Now().UnixMilli()
	if t := num(o["target"]); t > 0 {
		p := max(0, num(o["progress"])+delta)
		was, _ := o["done"].(bool)
		done := p >= t
		o["progress"], o["done"] = p, done
		if done && !was {
			o["doneAt"] = now
		} else if !done {
			delete(o, "doneAt")
		}
	} else {
		d, _ := o["done"].(bool)
		o["done"] = !d
		if !d {
			o["doneAt"] = now
		} else {
			delete(o, "doneAt")
		}
	}
	somaPut("goals#"+id, o)
}

type somaStep struct {
	ID, Label string
	Done      bool
}

type somaProject struct {
	ID, Name, Client, Due, Color, Priority string
	Steps                                  []somaStep
}

func (p somaProject) done() int {
	n := 0
	for _, s := range p.Steps {
		if s.Done {
			n++
		}
	}
	return n
}

// somaProjects is the active projects: deadlines first, nearest first.
func somaProjects() []somaProject {
	var out []somaProject
	for _, o := range somaList("projects") {
		if st := str(o["status"]); st != "" && st != "active" {
			continue
		}
		p := somaProject{ID: str(o["id"]), Name: str(o["name"]), Client: str(o["client"]), Due: str(o["due"]),
			Color: str(o["color"]), Priority: str(o["priority"])}
		steps, _ := o["steps"].([]any)
		for _, s := range steps {
			if so, ok := s.(obj); ok {
				d, _ := so["done"].(bool)
				p.Steps = append(p.Steps, somaStep{ID: str(so["id"]), Label: str(so["label"]), Done: d})
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Due, out[j].Due
		if (a == "") != (b == "") {
			return a != ""
		}
		return a < b
	})
	return out
}

// somaStepTap ticks a project step (setStep).
func somaStepTap(projectID, stepID string) {
	o := somaObj("projects#" + projectID)
	if o == nil {
		return
	}
	now := time.Now().UnixMilli()
	steps, _ := o["steps"].([]any)
	for _, s := range steps {
		if so, ok := s.(obj); ok && str(so["id"]) == stepID {
			d, _ := so["done"].(bool)
			so["done"] = !d
			if !d {
				so["at"] = now
			} else {
				delete(so, "at")
			}
		}
	}
	o["touchedAt"] = now
	somaPut("projects#"+projectID, o)
}

// somaAddStep adds a step to a project (addStep).
func somaAddStep(projectID, label string) {
	label = strings.TrimSpace(label)
	o := somaObj("projects#" + projectID)
	if o == nil || label == "" {
		return
	}
	now := time.Now().UnixMilli()
	steps, _ := o["steps"].([]any)
	id := "ps-" + somaNewID()
	o["steps"] = append(steps, obj{"id": id, "label": label, "done": false})
	o["touchedAt"] = now
	somaPut("projects#"+projectID, o)
}

// --- notes (notes.ts): the Obsidian vault, mirrored by the PC --------------------------------

type somaNote struct {
	ID, Text string
	Mtime    int64
}

func (n somaNote) title() string {
	return strings.TrimSuffix(n.ID[strings.LastIndex(n.ID, "/")+1:], ".md")
}

func (n somaNote) folder() string {
	if i := strings.LastIndex(n.ID, "/"); i > 0 {
		return n.ID[:i]
	}
	return ""
}

// somaNotes is every note, the most recently changed first.
func somaNotes() []somaNote {
	var out []somaNote
	for _, o := range somaList("notes") {
		out = append(out, somaNote{ID: str(o["id"]), Text: str(o["text"]), Mtime: int64(num(o["mtime"]))})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out
}

func somaNoteByID(id string) *somaNote {
	o := somaObj("notes#" + id)
	if o == nil {
		return nil
	}
	return &somaNote{ID: id, Text: str(o["text"]), Mtime: int64(num(o["mtime"]))}
}

func somaPutNote(id, text string) {
	somaPut("notes#"+id, obj{"id": id, "text": text, "mtime": time.Now().UnixMilli()})
}

// somaNoteToggle ticks or unticks the checkbox on line i ("- [ ]" / "- [x]").
func somaNoteToggle(id string, i int) {
	n := somaNoteByID(id)
	if n == nil {
		return
	}
	lines := strings.Split(n.Text, "\n")
	if i < 0 || i >= len(lines) {
		return
	}
	l := lines[i]
	switch {
	case strings.Contains(l, "[ ] "):
		l = strings.Replace(l, "[ ] ", "[x] ", 1)
	case strings.Contains(l, "[x] "):
		l = strings.Replace(l, "[x] ", "[ ] ", 1)
	case strings.Contains(l, "[X] "):
		l = strings.Replace(l, "[X] ", "[ ] ", 1)
	default:
		return
	}
	lines[i] = l
	somaPutNote(id, strings.Join(lines, "\n"))
}

// somaNoteAppend adds a line at the end of a note (above nothing: SOMA's block is rewritten anyway).
func somaNoteAppend(id, line string) {
	line = strings.TrimRight(line, " ")
	n := somaNoteByID(id)
	if n == nil || strings.TrimSpace(line) == "" {
		return
	}
	text := strings.TrimRight(n.Text, "\n")
	somaPutNote(id, text+"\n"+line+"\n")
}

// somaNewNote makes a note in the Inbox folder, named after its first line.
func somaNewNote(title string) string {
	title = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|#^[]`, r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(title))
	if title == "" {
		return ""
	}
	id := "Inbox/" + title + ".md"
	if somaNoteByID(id) == nil {
		somaPutNote(id, "# "+title+"\n\n")
	}
	return id
}

// noteTask reads a checkbox line: whether it's one, ticked, and its text without the block id.
func noteTask(l string) (task, done bool, text string) {
	t := strings.TrimLeft(l, " \t")
	for _, p := range []string{"- [ ] ", "* [ ] ", "- [x] ", "- [X] ", "* [x] "} {
		if strings.HasPrefix(t, p) {
			text = t[len(p):]
			if i := strings.LastIndex(text, " ^s-"); i >= 0 {
				text = text[:i]
			}
			return true, p[3] != ' ', text
		}
	}
	return false, false, ""
}

// --- the day's rings (rings.ts): eaten, protein, water, burned, each against its goal --------

type somaRing struct {
	Label, Unit string
	Value, Goal float64
	From, To    uint32 // the ring's gradient, as Soma draws it
}

func somaRingsFor(date string) []somaRing {
	n := somaNutrition(date)
	goals := obj{}
	if o := somaObj("nutrition/" + date); o != nil {
		if g, ok := o["goals"].(obj); ok {
			goals = g
		}
	}
	goal := func(k string, def float64) float64 {
		if v := num(goals[k]); v > 0 {
			return v
		}
		return def
	}
	burnt := 0.0
	if s := somaTraining(date); s != nil {
		burnt = s.Calories
	}
	return []somaRing{
		{"Calories", "cal", n.Cals, goal("cals", 2400), 0xfa114f, 0xff4fa0},
		{"Protein", "g", n.P, goal("protein", 160), 0x7dff00, 0xd8ff2e},
		{"Water", "ml", n.Water, goal("water", 3500), 0x00d8ff, 0x3dffe8},
		{"Burned", "cal", burnt, 500, 0xff6a00, 0xffc000},
	}
}

// --- amounts (habit-ramp.ts): a habit that climbs, a little more each day -------------------

type somaRampT struct {
	Start, Target, Step float64
	From, Unit, Advance string
}

func (h somaHabit) rampOf() *somaRampT {
	r, ok := h.o["ramp"].(obj)
	if !ok {
		return nil
	}
	return &somaRampT{num(r["start"]), num(r["target"]), num(r["step"]), str(r["from"]), str(r["unit"]), str(r["advance"])}
}

func (r *somaRampT) build() bool { return r.Target >= r.Start }

func (r *somaRampT) meets(rung float64, v float64, ok bool) bool {
	if !ok {
		return false
	}
	if r.build() {
		return v >= rung-1e-6
	}
	return v <= rung+1e-6
}

func (r *somaRampT) rungAfter(n int) float64 {
	step := math.Abs(r.Step)
	var rung float64
	if r.build() {
		rung = min(r.Target, r.Start+step*float64(n))
	} else {
		rung = max(r.Target, r.Start-step*float64(n))
	}
	if r.Unit == "min" {
		rung = math.Round(rung*60) / 60
	}
	return rung
}

// rungOn is the day's amount to reach (rungOn).
func (r *somaRampT) rungOn(date string, log obj) float64 {
	from, err1 := time.Parse("2006-01-02", r.From)
	d, err2 := time.Parse("2006-01-02", date)
	if err1 != nil || err2 != nil {
		return r.Start
	}
	elapsed := int(math.Round(d.Sub(from).Hours() / 24))
	if elapsed <= 0 {
		return r.Start
	}
	if r.Advance == "calendar" {
		return r.rungAfter(elapsed)
	}
	earned := 0
	for i := 0; i < elapsed; i++ {
		v, ok := log[from.AddDate(0, 0, i).Format("2006-01-02")].(float64)
		if r.meets(r.rungAfter(earned), v, ok) {
			earned++
		}
	}
	return r.rungAfter(earned)
}

// somaAmount is a ramp habit's day: what's logged and what's needed.
func (h somaHabit) amount(date string) (logged, rung float64, unit string) {
	r := h.rampOf()
	if r == nil {
		return 0, 0, ""
	}
	log, _ := h.o["amountLog"].(obj)
	logged = num(log[date])
	return logged, r.rungOn(date, log), r.Unit
}

// somaLogAmount adds delta to today's amount (logAmount), and ticks the day when it's met.
func somaLogAmount(id string, delta float64) {
	o := somaObj("habits#" + id)
	if o == nil {
		return
	}
	h := somaHabit{ID: id, o: o}
	r := h.rampOf()
	if r == nil {
		return
	}
	date := somaToday()
	log, _ := o["amountLog"].(obj)
	if log == nil {
		log = obj{}
	}
	v := max(0, num(log[date])+delta)
	if r.Unit == "min" {
		v = math.Round(v*60) / 60
	} else {
		v = math.Round(v)
	}
	log[date] = v
	o["amountLog"] = log
	hist, _ := o["history"].(obj)
	if hist == nil {
		hist = obj{}
	}
	hist[date] = r.meets(r.rungOn(date, log), v, true)
	o["history"] = hist
	somaPut("habits#"+id, o)
}

// rampStep is how much one tap adds: a sensible amount for the unit.
func rampStep(unit string) float64 {
	switch unit {
	case "sec":
		return 10
	case "min":
		return 5
	case "page":
		return 5
	}
	return 1
}

// --- streaks: a run of days, one missed day allowed each week --------------------------------

// streak counts back from today (or yesterday, while today isn't done yet): a missed day is
// forgiven once per Monday-to-Sunday week.
func (h somaHabit) streak(today string) int {
	d, err := time.Parse("2006-01-02", today)
	if err != nil {
		return 0
	}
	if !h.done(today) {
		d = d.AddDate(0, 0, -1)
	}
	n := 0
	forgiven := map[string]bool{}
	for i := 0; i < 3650; i++ {
		day := d.Format("2006-01-02")
		if h.done(day) {
			n++
		} else {
			wk := mondayOf(day)
			if forgiven[wk] {
				break
			}
			forgiven[wk] = true
		}
		d = d.AddDate(0, 0, -1)
		if since := str(h.o["since"]); since != "" && d.Format("2006-01-02") < since {
			break
		}
		if i > 7 && n == 0 {
			break
		}
	}
	return n
}

// time is when a habit is done: "morning", "evening", or "" (any time). Kept in Soma's habit.
func (h somaHabit) when() string { return str(h.o["time"]) }

func somaHabitWhen(id string) {
	o := somaObj("habits#" + id)
	if o == nil {
		return
	}
	switch str(o["time"]) {
	case "":
		o["time"] = "morning"
	case "morning":
		o["time"] = "evening"
	default:
		delete(o, "time")
	}
	somaPut("habits#"+id, o)
}

// --- to-dos: priority, and the evening review ------------------------------------------------

func (t somaTodo) rank() int {
	switch t.Priority {
	case "high":
		return 0
	case "low":
		return 2
	}
	return 1
}

func somaTodoPriority(id string) {
	o := somaObj("todos#" + id)
	if o == nil {
		return
	}
	switch str(o["priority"]) {
	case "":
		o["priority"] = "high"
	case "high":
		o["priority"] = "low"
	default:
		delete(o, "priority")
	}
	somaPut("todos#"+id, o)
}

// somaMoveToTomorrow moves today's unfinished to-dos onto tomorrow (the evening review).
func somaMoveToTomorrow() int {
	today := somaToday()
	d, _ := time.Parse("2006-01-02", today)
	tomorrow := d.AddDate(0, 0, 1).Format("2006-01-02")
	n := 0
	for _, t := range somaTodos() {
		if t.Done || t.Scope != "day" || !t.active(today) {
			continue
		}
		o := somaObj("todos#" + t.ID)
		o["date"] = tomorrow
		if s, ok := o["slot"].(obj); ok && str(s["date"]) == today {
			s["date"] = tomorrow
		}
		somaPut("todos#"+t.ID, o)
		n++
	}
	return n
}

// somaMoveTodo puts a to-do at a new time on date (setTodoSlot), keeping its length.
func somaMoveTodo(id, date string, start int) {
	o := somaObj("todos#" + id)
	if o == nil {
		return
	}
	mins := 30
	if s, ok := o["slot"].(obj); ok && num(s["mins"]) > 0 {
		mins = int(num(s["mins"]))
	}
	start = min(max(start, 0), 24*60-15)
	mins = min(mins, 24*60-start)
	o["slot"] = obj{"date": date, "start": start, "mins": mins}
	o["scope"], o["date"], o["cleared"] = "day", date, false
	somaPut("todos#"+id, o)
}

// --- logging food, water and training from the tablet ----------------------------------------

// somaDay is the day's nutrition record, made the way Soma's emptyDay does if there's none.
func somaDay(date string) obj {
	if o := somaObj("nutrition/" + date); o != nil {
		return o
	}
	return obj{"goals": obj{"cals": 2400, "protein": 160, "carbs": 260, "fat": 70, "water": 3500}, "water": 0, "creatine": 0, "items": []any{}}
}

// parseFood reads "Chicken wrap 450 35": a name, then calories, then protein (both optional).
func parseFood(s string) (name string, cals, protein float64) {
	f := strings.Fields(s)
	var nums []float64
	for len(f) > 0 {
		v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(f[len(f)-1]), "g"), "kcal"), 64)
		if err != nil || len(nums) == 2 {
			break
		}
		nums = append([]float64{v}, nums...)
		f = f[:len(f)-1]
	}
	name = strings.Join(f, " ")
	if len(nums) > 0 {
		cals = nums[0]
	}
	if len(nums) > 1 {
		protein = nums[1]
	}
	return
}

// somaAddFood logs what was eaten now, as a FoodItem with the macros typed and the rest zero.
func somaAddFood(text string) bool {
	name, cals, p := parseFood(text)
	if name == "" || cals <= 0 {
		return false
	}
	date := somaToday()
	day := somaDay(date)
	items, _ := day["items"].([]any)
	now := time.Now().UnixMilli()
	meal := "Snacks"
	switch h := time.Now().In(displayZone).Hour(); {
	case h < 11:
		meal = "Breakfast"
	case h < 16:
		meal = "Lunch"
	case h >= 18:
		meal = "Dinner"
	}
	it := obj{"name": name, "serving": 1, "unit": "serving", "cals": cals, "p": p, "c": 0, "f": 0, "fiber": 0,
		"sodium": 0, "potassium": 0, "calcium": 0, "iron": 0, "magnesium": 0, "zinc": 0, "meal": meal,
		"loggedAt": now, "eatenAt": now}
	day["items"] = append(items, it)
	somaPut("nutrition/"+date, day)
	return true
}

func somaAddWater(ml float64) {
	date := somaToday()
	day := somaDay(date)
	day["water"] = max(0, num(day["water"])+ml)
	somaPut("nutrition/"+date, day)
}

// parseWorkout reads "Push 60": the split, then minutes.
func parseWorkout(s string) (split string, mins int) {
	f := strings.Fields(s)
	if len(f) > 1 {
		if v, err := strconv.Atoi(strings.TrimSuffix(f[len(f)-1], "min")); err == nil {
			mins, f = v, f[:len(f)-1]
		}
	}
	return strings.Join(f, " "), mins
}

// somaLogWorkout saves a session that just ended, without its sets (log those in Train).
func somaLogWorkout(text string) bool {
	split, mins := parseWorkout(text)
	if split == "" || mins <= 0 {
		return false
	}
	date := somaToday()
	somaPut("history/"+date, obj{"timestamp": time.Now().UnixMilli(), "split": split,
		"durationFormatted": fmt.Sprintf("%d:00", mins), "caloriesBurned": mins * 6, "totalVol": 0,
		"totalSets": 0, "axialVol": 0, "exercises": []any{}, "muscles": obj{}})
	return true
}

// somaMoveTodoTomorrow moves one to-do to tomorrow (and its time slot with it).
func somaMoveTodoTomorrow(id string) {
	o := somaObj("todos#" + id)
	if o == nil {
		return
	}
	d, _ := time.Parse("2006-01-02", somaToday())
	tomorrow := d.AddDate(0, 0, 1).Format("2006-01-02")
	o["date"], o["scope"], o["done"] = tomorrow, "day", false
	if s, ok := o["slot"].(obj); ok {
		s["date"] = tomorrow
	}
	somaPut("todos#"+id, o)
}

// somaDeleteTodo removes a to-do (removed on the PC and phone too).
func somaDeleteTodo(id string) {
	soma.put("todos#"+id, "")
	var order []string
	for _, t := range somaList("todos") {
		if str(t["id"]) != id {
			order = append(order, str(t["id"]))
		}
	}
	somaPut("todos@order", order)
}

// somaAddCreatine logs grams of creatine today (addCreatine).
func somaAddCreatine(g float64) {
	date := somaToday()
	day := somaDay(date)
	day["creatine"] = num(day["creatine"]) + g
	somaPut("nutrition/"+date, day)
}

// somaLogSleepHours logs last night as typed (logSleep): "7.5". Returns what to say.
func somaLogSleepHours(text string) string {
	h, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(text, ",", ".")), 64)
	if err != nil || h <= 0 || h > 16 {
		return ""
	}
	date := somaToday()
	day := somaDay(date)
	sl, _ := day["sleep"].(obj)
	if sl == nil {
		sl = obj{}
	}
	sl["hours"] = h
	day["sleep"] = sl
	somaPut("nutrition/"+date, day)
	return fmt.Sprintf("Slept %.1f h: logged", h)
}
