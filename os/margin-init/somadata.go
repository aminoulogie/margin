package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
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
	Done, Cleared              bool
	SlotDate                   string
	SlotStart, SlotMins        int
}

func somaTodos() []somaTodo {
	var out []somaTodo
	for _, o := range somaList("todos") {
		t := somaTodo{ID: str(o["id"]), Text: str(o["text"]), Date: str(o["date"]), Scope: str(o["scope"]), Due: str(o["due"])}
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
