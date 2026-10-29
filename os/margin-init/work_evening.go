package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"margin-init/ui"
)

// The evening check-in (Personal > Evening), for the twenty minutes before bed, like Soma's
// own on the phone (checkin.ts): last night's sleep, weight, creatine, the supplements taken
// today, the day's food against its goals, the habits and practice still open, and three
// lines of journal. Everything is written where Soma keeps it (nutrition/<date>: sleep,
// bodyWeight, creatine, and supplementsTaken, a list the phone keeps as it is; the journal
// is a note, "Journal/<date>.md", so it's on the phone too). Tomorrow's one thing also
// becomes a to-do for tomorrow.

var supplementsPath = filepath.Join(marginHome, "supplements.json")

// wkSupplements are what the user takes (their names, in order); creatine is its own line.
func wkSupplements() []string {
	var out []string
	if b, err := os.ReadFile(supplementsPath); err == nil {
		json.Unmarshal(b, &out)
	} else {
		out = []string{"Magnesium", "Vitamin D3", "Omega-3", "Zinc"}
	}
	return out
}

func wkSaveSupplements(list []string) {
	b, _ := json.Marshal(list)
	os.MkdirAll(filepath.Dir(supplementsPath), 0o755)
	os.WriteFile(supplementsPath, b, 0o644)
	soma.bump("supplements") // pages drawn before are stale
}

func wkTaken(date string) []string {
	var out []string
	if l, ok := somaRead("nutrition/" + date)["supplementsTaken"].([]any); ok {
		for _, v := range l {
			out = append(out, str(v))
		}
	}
	return out
}

// journal is the evening note's three parts.
var journalParts = []struct{ id, title, prompt string }{
	{"well", "What went well", "One thing that went well today"},
	{"drop", "What to drop", "Something to stop or do less"},
	{"one", "Tomorrow's one thing", "The one thing for tomorrow"},
}

func journalID(date string) string { return "Journal/" + date + ".md" }

func journalRead(date string) map[string]string {
	out := map[string]string{}
	n := somaNoteByID(journalID(date))
	if n == nil {
		return out
	}
	cur := ""
	for _, l := range strings.Split(n.Text, "\n") {
		if t, ok := strings.CutPrefix(l, "## "); ok {
			cur = ""
			for _, p := range journalParts {
				if p.title == strings.TrimSpace(t) {
					cur = p.id
				}
			}
			continue
		}
		if cur != "" && strings.TrimSpace(l) != "" {
			out[cur] = strings.TrimSpace(out[cur] + " " + strings.TrimSpace(l))
		}
	}
	return out
}

func journalWrite(date string, parts map[string]string) {
	t, _ := time.ParseInLocation("2006-01-02", date, displayZone)
	var b strings.Builder
	fmt.Fprintf(&b, "# Evening, %s\n", t.Format("Monday 2 January 2006"))
	for _, p := range journalParts {
		if v := strings.TrimSpace(parts[p.id]); v != "" {
			fmt.Fprintf(&b, "\n## %s\n%s\n", p.title, v)
		}
	}
	somaPutNote(journalID(date), b.String())
}

// evening draws the check-in.
func (k *wk) evening() int {
	now := time.Now().In(displayZone)
	today := somaDate(now)
	day := somaRead("nutrition/" + today)
	projects := wkProjects()
	var habitsLeft []somaHabit
	for _, h := range somaHabits() {
		if !h.done(today) && !h.ramp() {
			habitsLeft = append(habitsLeft, h)
		}
	}
	practice := wkPracticesOn(projects, today)
	pOpen := 0
	for _, r := range practice {
		if !r.pr.Done[today] {
			pOpen++
		}
	}
	sl, _ := day["sleep"].(obj)
	open := len(habitsLeft) + pOpen
	if num(sl["hours"]) == 0 {
		open++
	}
	if num(day["creatine"]) == 0 {
		open++
	}
	sub := "All done for today"
	if open > 0 {
		sub = fmt.Sprintf("%d %s still open today", open, map[bool]string{true: "thing", false: "things"}[open == 1])
	}
	k.header([]string{"Personal", "Evening"}, "Evening check-in", now.Format("Monday 2 January")+" · "+sub, color.RGBA{})
	y := 236

	// Body: sleep, weight, creatine.
	cr := image.Rect(wkX0, y, wkX1, y+92+3*96)
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "moon", "Body", "")
	ry := y + 92
	row := func(label, value string) int {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 22), 190, ry+56, wkFG, label)
		k.textC(wkFont("bold", 26), 885, ry+58, wkFG, value)
		cy := ry + 48
		ry += 96
		return cy
	}
	sleep := "not logged"
	if h := num(sl["hours"]); h > 0 {
		sleep = fmt.Sprintf("%g h", h)
	}
	cy := row("Sleep last night", sleep)
	k.iconBtn("wk:bsleep:-0.5", "minus", 770, cy, 26, rgb(0x20222a), wkFG)
	k.iconBtn("wk:bsleep:0.5", "plus", 1000, cy, 26, rgb(0x20222a), wkFG)
	w := "not logged"
	if v := num(day["bodyWeight"]); v > 0 {
		w = fmt.Sprintf("%.1f kg", v)
	}
	cy = row("Weight", w)
	k.iconBtn("wk:fweight:"+today+":-0.1", "minus", 770, cy, 26, rgb(0x20222a), wkFG)
	k.iconBtn("wk:fweight:"+today+":0.1", "plus", 1000, cy, 26, rgb(0x20222a), wkFG)
	cg := num(day["creatine"])
	cv := "not taken"
	if cg > 0 {
		cv = fmt.Sprintf("%g g", cg)
	}
	cy = row("Creatine", cv)
	if cg > 0 {
		k.iconBtn("wk:bcrea:0", "x", 770, cy, 26, rgb(0x20222a), wkFG)
	}
	k.iconBtn("wk:bcrea:5", "plus", 1000, cy, 26, wkLime, wkOnLime)
	y = cr.Max.Y + 24

	// Supplements taken today.
	sups, taken := wkSupplements(), wkTaken(today)
	cr = image.Rect(wkX0, y, wkX1, y+92+84*max(len(sups), 1))
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "pill", "Supplements", fmt.Sprintf("· %d of %d today", len(taken), len(sups)))
	x := k.outline("wk:type-open:evsupadd:", "plus", "Add", 1136, y+22, 50)
	if len(sups) > 0 {
		k.outline("wk:sheet-open:evsups:", "pencil", "Edit", x-12, y+22, 50)
	}
	ry = y + 92
	for _, s := range sups {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		on := slices.Contains(taken, s)
		k.checkbox(188, ry+42, on)
		k.struck(240, ry+50, s, on, 700)
		k.btn("wk:evsup:"+s, k.R(wkX0, ry, wkX1, ry+84))
		ry += 84
	}
	if len(sups) == 0 {
		k.emptyNote(190, ry+50, "Add what you take: magnesium, vitamin D, omega-3…")
	}
	y = cr.Max.Y + 24

	// Food today, against the goals.
	n := somaNutrition(today)
	cr = image.Rect(wkX0, y, wkX1, y+92+4*64)
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "utensils", "Food today", "")
	k.outline("wk:go:nutrition", "", "Open", 1136, y+22, 50)
	ry = y + 92
	goalOr := func(v, d float64) float64 {
		if v > 0 {
			return v
		}
		return d
	}
	goals, _ := day["goals"].(obj)
	for _, m := range []struct {
		l       string
		v, goal float64
		unit    string
	}{{"Calories", n.Cals, goalOr(n.GoalCals, 2400), "kcal"}, {"Protein", n.P, goalOr(n.GoalP, 160), "g"},
		{"Carbs", n.C, goalOr(num(goals["carbs"]), 260), "g"}, {"Fat", n.F, goalOr(num(goals["fat"]), 70), "g"}} {
		k.text(wkFont("semi", 20), 190, ry+40, wkFG, m.l)
		col := wkLime
		if m.v > m.goal*1.1 {
			col = wkAmber
		}
		k.hBar(k.R(420, ry+30, 900, ry+40), min(m.v/m.goal, 1), col)
		k.textR(wkFont("regular", 19), 1130, ry+42, wkText2, fmt.Sprintf("%.0f / %.0f %s", m.v, m.goal, m.unit))
		ry += 64
	}
	if pl, _ := day["planned"].([]any); len(pl) > 0 {
		k.text(wkFont("regular", 18), 190, cr.Max.Y-14, wkAmber, fmt.Sprintf("%d planned foods not logged as eaten", len(pl)))
	}
	y = cr.Max.Y + 24

	// Still open: habits, the projects' practice.
	if len(habitsLeft)+len(practice) > 0 {
		cr = image.Rect(wkX0, y, wkX1, y+92+84*len(habitsLeft)+84*len(practice))
		k.card(cr)
		k.cardHead(wkX0+28, y+53, "circle-check", "Still open", "")
		ry = y + 92
		for _, h := range habitsLeft {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			k.checkbox(188, ry+42, false)
			k.text(wkFont("semi", 22), 240, ry+50, wkFG, clip(wkFont("semi", 22), h.Name, 700))
			k.btn("wk:htick:"+h.ID, k.R(wkX0, ry, wkX1, ry+84))
			ry += 84
		}
		k.practiceRows(practice, today, ry, now, false)
		y = cr.Max.Y + 24
	}

	// The journal.
	j := journalRead(today)
	cr = image.Rect(wkX0, y, wkX1, y+92+3*96)
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "book-open", "Journal", "· saved to your notes")
	ry = y + 92
	for _, p := range journalParts {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 19), 190, ry+38, wkMuted, p.title)
		v, col := j[p.id], wkFG
		if v == "" {
			v, col = "Tap to write", wkFaint
		}
		k.text(wkFont("regular", 21), 190, ry+72, col, clip(wkFont("regular", 21), v, 900))
		k.icon("pencil", 1120, ry+48, 22, wkFaint)
		k.btn("wk:type-open:evj:"+p.id, k.R(wkX0, ry, wkX1, ry+96))
		ry += 96
	}
	return cr.Max.Y
}

func init() {
	wkActions["evsup"] = func(c *console, name string) {
		patchFoodDay(somaToday(), func(day obj) {
			var keep []any
			had := false
			l, _ := day["supplementsTaken"].([]any)
			for _, v := range l {
				if str(v) == name {
					had = true
					continue
				}
				keep = append(keep, v)
			}
			if !had {
				keep = append(keep, name)
			}
			day["supplementsTaken"] = keep
		})
	}
	wkPrompts["evsupadd"] = func(string) (string, string) { return "A supplement you take, e.g. Magnesium", "" }
	wkTypers["evsupadd"] = func(c *console, _, text string) {
		if text = strings.TrimSpace(text); text != "" && !slices.Contains(wkSupplements(), text) {
			wkSaveSupplements(append(wkSupplements(), text))
		}
	}
	wkSheets["evsups"] = func(k *wk, _ string) *wkSheet {
		sh := &wkSheet{title: "Your supplements", sub: "Tap one to take it off the list"}
		for _, s := range wkSupplements() {
			sh.rows = append(sh.rows, wkRow{id: "wk:evsupdel:" + s, label: s, icon: "trash-2", danger: true})
		}
		return sh
	}
	wkActions["evsupdel"] = func(c *console, name string) {
		wkSaveSupplements(slices.DeleteFunc(wkSupplements(), func(s string) bool { return s == name }))
		c.somaUI.keep = true
	}
	wkPrompts["evj"] = func(part string) (string, string) {
		for _, p := range journalParts {
			if p.id == part {
				return p.prompt, journalRead(somaToday())[part]
			}
		}
		return "", ""
	}
	wkTypers["evj"] = func(c *console, part, text string) {
		today := somaToday()
		j := journalRead(today)
		j[part] = strings.TrimSpace(text)
		journalWrite(today, j)
		if part == "one" && j[part] != "" { // and a to-do for tomorrow
			tomorrow := somaDate(time.Now().AddDate(0, 0, 1))
			for _, t := range somaTodos() {
				if t.Date == tomorrow && t.Text == j[part] {
					return
				}
			}
			somaAddTodo(j[part], "day", tomorrow)
			for _, t := range somaTodos() {
				if t.Date == tomorrow && t.Text == j[part] {
					somaPatch("todos", t.ID, func(o obj) { o["priority"] = "high" })
				}
			}
			c.toastMsg("Added to tomorrow")
		}
	}
}
