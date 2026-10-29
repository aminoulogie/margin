package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"margin-init/ui"
)

// Time > Entries, laid out like the workstation's Entries list on the PC (TimePage.tsx): each day,
// each piece of work with its start, end, hours, type, what was done and whether it's billed.
//
// Work is the time entries; a work log shift (worklog.ts) counts as work only when no time entry
// falls inside it. A shift with entries inside is the time on duty for
// the client: its empty parts are waiting. So a day runs from its first start to its last end
// (entries and shifts), and every part of it that isn't work is either lunch (a break logged in
// the work log, one gap around midday, or a gap tapped as lunch) or **standby**: waiting on the
// client, billed at half the rate of the work before it. Standby is added wherever billable value
// is counted (wkValue, the invoices, the reports).

const wkStandbyShare = 0.5

type wkLogRow struct {
	start, end time.Time
	kind       string // "Entry", "Steps", "Work log", "On duty"; "Standby" / "Lunch" for gaps
	col        color.RGBA
	title, sub string
	billable   bool
	rate       float64 // an hour
	project    string  // the project it's billed to ("" none)
	client     string
	tap        string // button id ("" none)
	gap        bool
	gapKey     string
}

func (r wkLogRow) dur() time.Duration { return max(0, r.end.Sub(r.start)) }

// --- lunch / standby choices, and the custom range ----------------------------------------------

var standbyPath = filepath.Join(marginHome, "standby.json")

func standbyChoices() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(standbyPath); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func setStandbyChoice(key, v string) {
	m := standbyChoices()
	m[key] = v
	b, _ := json.Marshal(m)
	os.MkdirAll(filepath.Dir(standbyPath), 0o755)
	os.WriteFile(standbyPath, b, 0o644)
	somaGen.Add(1) // pages drawn before this are stale
}

// --- rows ---------------------------------------------------------------------------------------

type wkSpan struct{ a, b time.Time }

// cut is span s minus the spans in out (sorted or not), as the pieces left.
func cut(s wkSpan, out []wkSpan) []wkSpan {
	pieces := []wkSpan{s}
	for _, o := range out {
		var next []wkSpan
		for _, p := range pieces {
			if !o.b.After(p.a) || !o.a.Before(p.b) {
				next = append(next, p)
				continue
			}
			if o.a.After(p.a) {
				next = append(next, wkSpan{p.a, o.a})
			}
			if o.b.Before(p.b) {
				next = append(next, wkSpan{o.b, p.b})
			}
		}
		pieces = next
	}
	return pieces
}

// wkLogDays is every day with work on it, newest first, and its rows in time order with the
// standby and lunch between them.
func wkLogDays(entries []wkEntry, projects []wkProject, now time.Time) ([]string, map[string][]wkLogRow) {
	byID := map[string]wkProject{}
	byClient := map[string]string{} // client → the project its standby is billed to
	sorted := append([]wkProject(nil), projects...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, p := range sorted {
		byID[p.ID] = p
		if _, ok := byClient[p.Client]; !ok && p.Client != "" {
			byClient[p.Client] = p.ID
		}
	}
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)

	work := map[string][]wkLogRow{}   // entries
	shifts := map[string][]wkLogRow{} // work log shifts
	breaks := map[string][]wkSpan{}
	for _, e := range entries {
		end := e.Start.Add(e.dur(now))
		p := byID[e.ProjectID]
		kind, col := "Entry", wkBlue
		if e.FromSteps {
			kind, col = "Steps", wkPurple
		}
		name := p.Name
		if name == "" {
			name = "Deleted project"
		}
		sub := name
		for _, s := range p.Steps {
			if s.ID == e.StepID && s.Label != "" {
				sub += " · " + s.Label
			}
		}
		title := e.Note
		if title == "" {
			title = name
		}
		d := somaDate(e.Start)
		work[d] = append(work[d], wkLogRow{start: e.Start, end: end, kind: kind, col: col, title: title, sub: sub,
			billable: e.Billable, rate: p.Rate, project: e.ProjectID, client: p.Client, tap: "wk:sheet-open:entry:" + e.ID})
	}
	for _, o := range somaList("shifts") {
		start := msTime(o["start"])
		if start.IsZero() {
			continue
		}
		end := msTime(o["end"])
		if end.IsZero() || end.After(now) {
			end = now
		}
		if end.Sub(start) > 16*time.Hour {
			end = start.Add(16 * time.Hour)
		}
		var done []string
		if as, ok := o["activities"].([]any); ok {
			for _, a := range as {
				if ao, _ := a.(obj); ao != nil && strings.TrimSpace(str(ao["text"])) != "" {
					done = append(done, strings.TrimSpace(str(ao["text"])))
				}
			}
		}
		client := str(o["client"])
		title := strings.Join(done, " · ")
		if title == "" {
			title = client
		}
		d := somaDate(start)
		if bs, ok := o["breaks"].([]any); ok {
			for _, b := range bs {
				bo, _ := b.(obj)
				ba, bb := msTime(bo["start"]), msTime(bo["end"])
				if bb.IsZero() {
					bb = end
				}
				if ba.Before(start) {
					ba = start
				}
				if bb.After(end) {
					bb = end
				}
				if bb.After(ba) {
					breaks[d] = append(breaks[d], wkSpan{ba, bb})
				}
			}
		}
		shifts[d] = append(shifts[d], wkLogRow{start: start, end: end, kind: "Work log", col: wkGreen, title: title,
			sub: client, billable: true, rate: num(rates[client]), project: byClient[client], client: client})
	}

	choice := standbyChoices()
	all := map[string]bool{}
	for d := range work {
		all[d] = true
	}
	for d := range shifts {
		all[d] = true
	}
	var keys []string
	out := map[string][]wkLogRow{}
	for d := range all {
		keys = append(keys, d)
		var rows, counted []wkLogRow
		rows = append(rows, work[d]...)
		counted = append(counted, work[d]...)
		for _, s := range shifts[d] {
			inside := false
			for _, w := range work[d] {
				if w.start.Before(s.end) && w.end.After(s.start) {
					inside = true
				}
			}
			if inside {
				// On duty for the client: its empty parts are standby, not work.
				s.kind, s.col, s.billable = "On duty", wkMuted, false
				s.sub = s.client + " · on duty, the work is in the entries"
				rows = append(rows, s)
				continue
			}
			rows = append(rows, s)
			for _, p := range cut(wkSpan{s.start, s.end}, breaks[d]) {
				w := s
				w.start, w.end = p.a, p.b
				counted = append(counted, w)
			}
		}
		for _, b := range breaks[d] {
			rows = append(rows, wkLogRow{start: b.a, end: b.b, kind: "Lunch", col: wkMuted, title: "Break (work log)", gap: true})
		}
		sort.SliceStable(counted, func(i, j int) bool { return counted[i].start.Before(counted[j].start) })

		// The day's window: first start to last end, of work and of time on duty.
		var first, last time.Time
		for _, r := range rows {
			if r.gap {
				continue
			}
			if first.IsZero() || r.start.Before(first) {
				first = r.start
			}
			if r.end.After(last) {
				last = r.end
			}
		}
		// Gaps = the window minus work minus logged breaks.
		var busy []wkSpan
		for _, w := range counted {
			busy = append(busy, wkSpan{w.start, w.end})
		}
		busy = append(busy, breaks[d]...)
		lunchTaken := len(breaks[d]) > 0
		for _, g := range cut(wkSpan{first, last}, busy) {
			if g.b.Sub(g.a) < 5*time.Minute {
				continue
			}
			row := wkLogRow{start: g.a, end: g.b, gap: true}
			row.gapKey = d + "|" + g.a.In(displayZone).Format("15:04")
			// Billed at the rate of the work before it (the client being waited on), else after.
			var before, after *wkLogRow
			for i := range counted {
				if !counted[i].end.After(g.a) {
					before = &counted[i]
				} else if after == nil && !counted[i].start.Before(g.b) {
					after = &counted[i]
				}
			}
			for _, w := range []*wkLogRow{before, after} {
				if w != nil && w.billable && row.rate == 0 && row.project == "" {
					row.rate, row.project, row.client = w.rate, w.project, w.client
				}
			}
			lunch := false
			switch choice[row.gapKey] {
			case "lunch":
				lunch = true
			case "standby":
			default:
				h := g.a.In(displayZone)
				mins := h.Hour()*60 + h.Minute()
				if !lunchTaken && mins >= 11*60+30 && mins <= 14*60+30 && g.b.Sub(g.a) <= 90*time.Minute {
					lunch = true
				}
			}
			if lunch {
				lunchTaken = true
				row.kind, row.col, row.title = "Lunch", wkMuted, "Lunch"
			} else {
				row.kind, row.col, row.title, row.billable = "Standby", wkAmber, "Standby · waiting on the client", true
			}
			row.tap = "wk:standby:" + row.gapKey
			rows = append(rows, row)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if !rows[i].start.Equal(rows[j].start) {
				return rows[i].start.Before(rows[j].start)
			}
			return rows[i].kind == "On duty" // the duty window above what's in it
		})
		out[d] = rows
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	return keys, out
}

// wkDayFigures is a day's worked time (overlaps counted once), standby, and what they're worth.
func wkDayFigures(rows []wkLogRow) (worked, standby time.Duration, value, standbyValue float64) {
	var reach time.Time
	for _, r := range rows {
		if r.gap {
			if r.kind == "Standby" {
				standby += r.dur()
				standbyValue += r.dur().Hours() * r.rate * wkStandbyShare
			}
			continue
		}
		if r.kind == "On duty" {
			continue
		}
		s := r.start
		if s.Before(reach) {
			s = reach
		}
		if r.end.After(s) {
			d := r.end.Sub(s)
			worked += d
			if r.billable {
				value += d.Hours() * r.rate
			}
			reach = r.end
		}
	}
	// A work log shift split by its breaks is listed whole; take its breaks back out.
	for _, r := range rows {
		if r.gap && r.kind == "Lunch" && r.gapKey == "" {
			for _, w := range rows {
				if w.kind == "Work log" && !r.start.Before(w.start) && !r.end.After(w.end) {
					worked -= r.dur()
					value -= r.dur().Hours() * w.rate
					break
				}
			}
		}
	}
	return
}

// --- standby everywhere else: cached segments -------------------------------------------------------

type wkStandbySeg struct {
	start, end time.Time
	project    string
	client     string
	rate       float64
}

var standbyCache struct {
	sync.Mutex
	key  string
	segs []wkStandbySeg
}

// wkStandby is every piece of standby there is, worked out again only when the records change.
func wkStandby(now time.Time) []wkStandbySeg {
	key := fmt.Sprintf("%d|%s", somaGen.Load(), now.Format("2006-01-02 15:04"))
	standbyCache.Lock()
	defer standbyCache.Unlock()
	if standbyCache.key == key {
		return standbyCache.segs
	}
	keys, days := wkLogDays(wkEntries(), wkProjects(), now)
	var segs []wkStandbySeg
	for _, d := range keys {
		for _, r := range days[d] {
			if r.gap && r.kind == "Standby" {
				segs = append(segs, wkStandbySeg{r.start, r.end, r.project, r.client, r.rate})
			}
		}
	}
	standbyCache.key, standbyCache.segs = key, segs
	return segs
}

// wkStandbyIn is the standby billed to these projects that starts in [from, to), and its value.
func wkStandbyIn(projects []wkProject, from, to, now time.Time) (time.Duration, float64) {
	ids := map[string]bool{}
	for _, p := range projects {
		ids[p.ID] = true
	}
	var d time.Duration
	var v float64
	for _, s := range wkStandby(now) {
		if ids[s.project] && !s.start.Before(from) && s.start.Before(to) {
			d += s.end.Sub(s.start)
			v += s.end.Sub(s.start).Hours() * s.rate * wkStandbyShare
		}
	}
	return d, v
}

// --- the page -----------------------------------------------------------------------------------

// wkEntryRange is the period chosen on Time > Entries: this week, this month, all, or custom.
func (k *wk) wkEntryRange(now time.Time) (from, to time.Time, label string) {
	switch k.sel("chip", 0) {
	case 1:
		m := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
		return m, m.AddDate(0, 1, 0), now.Format("January")
	case 2:
		return time.Time{}, now.AddDate(10, 0, 0), "all time"
	case 3:
		c := standbyChoices()
		f, err1 := time.ParseInLocation("2006-01-02", c["_from"], displayZone)
		t, err2 := time.ParseInLocation("2006-01-02", c["_to"], displayZone)
		if err1 != nil {
			f = weekStart(now).AddDate(0, 0, -7)
		}
		if err2 != nil {
			t = dayStart(now)
		}
		if t.Before(f) {
			f, t = t, f
		}
		return f, t.AddDate(0, 0, 1), f.Format("2 Jan") + " – " + t.Format("2 Jan 2006")
	}
	w := weekStart(now)
	return w, w.AddDate(0, 0, 7), "this week"
}

func (k *wk) timeEntriesTab(entries []wkEntry, projects []wkProject, now time.Time, y int) int {
	keys, days := wkLogDays(entries, projects, now)
	from, to, label := k.wkEntryRange(now)
	fromD, toD := somaDate(from), somaDate(to)
	if from.IsZero() {
		fromD = ""
	}
	var shownKeys []string
	for _, d := range keys {
		if d >= fromD && d < toD {
			shownKeys = append(shownKeys, d)
		}
	}

	// The period: week, month, all, or two dates.
	k.chips("wk:chip", y, []string{"Week", "Month", "All", "Custom"}, nil, k.sel("chip", 0))
	if k.sel("chip", 0) == 3 {
		c := standbyChoices()
		f, t := c["_from"], c["_to"]
		if f == "" {
			f = somaDate(from)
		}
		if t == "" {
			t = somaDate(to.AddDate(0, 0, -1))
		}
		k.boxBtn("wk:srange:from", 660, y, 900, y+53, "calendar", "From "+wkDate(f), color.RGBA{}, wkFG)
		k.boxBtn("wk:srange:to", 920, y, wkX1, y+53, "calendar", "To "+wkDate(t), color.RGBA{}, wkFG)
	}
	y += 73

	var worked, standby time.Duration
	var value, sval float64
	for _, d := range shownKeys {
		w, s, v, sv := wkDayFigures(days[d])
		worked, standby, value, sval = worked+w, standby+s, value+v, sval+sv
	}
	box := k.row4(y)
	k.small(box(0), "clock", "Worked", wkHM(worked), "", label, wkFG)
	k.small(box(1), "circle-dollar-sign", "Billable", wkMoney(value), "", "with standby "+wkMoney(value+sval), wkFG)
	k.small(box(2), "hourglass", "Standby", wkHM(standby), "", "waiting on the client, not lunch", wkAmber)
	k.small(box(3), "circle-dollar-sign", "Standby value", wkMoney(sval), "", "at half the project rate", wkLime)
	y += 180
	k.text(wkFont("regular", 18), wkX0+8, y+24, wkFaint, clip(wkFont("regular", 18),
		"Tap a gap to switch it between standby and lunch.   Sync: "+soma.status(), wkX1-wkX0-16))
	y += 44

	shown := 14 * (1 + k.sel("more", 0)) // days
	for i, d := range shownKeys {
		if i == shown {
			k.boxBtn(fmt.Sprintf("wk:more:%d", k.sel("more", 0)+1), wkX0, y, wkX1, y+64, "chevron-down", "Show more", color.RGBA{}, wkFG)
			y += 84
			break
		}
		rows := days[d]
		w, s, v, sv := wkDayFigures(rows)
		k.text(wkFont("semi", 19), wkX0+8, y+32, wkMuted, strings.ToUpper(wkDate(d)))
		tot := wkHM(w) + " worked"
		if s > 0 {
			tot += " · " + wkHM(s) + " standby"
		}
		if v+sv > 0 {
			tot += " · " + wkMoney(v+sv)
		}
		k.textR(wkFont("semi", 19), wkX1-8, y+32, wkMuted, tot)
		y += 48
		cr := image.Rect(wkX0, y, wkX1, y+44+86*len(rows))
		k.card(cr)
		hf := wkFont("medium", 16)
		k.text(hf, wkX0+24, y+30, wkFaint, "START – END")
		k.textR(hf, wkX0+300, y+30, wkFaint, "HOURS")
		k.text(hf, wkX0+324, y+30, wkFaint, "TYPE")
		k.text(hf, wkX0+470, y+30, wkFaint, "WHAT WAS DONE")
		k.textR(hf, wkX1-24, y+30, wkFaint, "BILLABLE")
		ry := y + 44
		for _, r := range rows {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			k.logRow(r, ry, now)
			ry += 86
		}
		y = cr.Max.Y + 20
	}
	if len(shownKeys) == 0 {
		k.emptyNote(wkX0+20, y+40, "No time logged in "+label+".   Sync: "+soma.status())
		y += 80
	}
	return y
}

func (k *wk) logRow(r wkLogRow, ry int, now time.Time) {
	cy := ry + 44
	fg := wkFG
	if r.kind == "Lunch" || r.kind == "On duty" {
		fg = wkMuted
	}
	end := r.end.In(displayZone).Format("15:04")
	if !r.gap && r.end.Equal(now) {
		end = "now"
	}
	k.text(wkFont("semi", 21), wkX0+24, cy+8, fg, r.start.In(displayZone).Format("15:04")+" – "+end)
	d := r.dur()
	k.textR(wkFont("bold", 21), wkX0+300, cy+8, fg, fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60))
	tf := wkFont("semi", 16)
	tw := ui.TextWidth(tf, r.kind) + 24
	tr := k.R(wkX0+324, cy-16, wkX0+324+tw, cy+16)
	ui.RoundRect(k.img, tr, 16, blend(wkCard, r.col, 0.18))
	apText(k.img, tf, tr.Min.X+12, k.Y(cy)+6, r.col, r.kind)
	w := wkX1 - 200 - (wkX0 + 470)
	if r.gap {
		k.text(wkFont("semi", 20), wkX0+470, cy+8, fg, clip(wkFont("semi", 20), r.title, w))
	} else {
		k.text(wkFont("semi", 20), wkX0+470, cy-4, fg, clip(wkFont("semi", 20), r.title, w))
		k.text(wkFont("regular", 17), wkX0+470, cy+22, wkFaint, clip(wkFont("regular", 17), r.sub, w))
	}
	bill := "—"
	col := wkFaint
	switch {
	case r.kind == "Lunch":
		bill = "not billed"
	case r.kind == "On duty":
		bill = "see entries"
	case r.kind == "Standby" && r.rate > 0:
		bill, col = "50% · "+wkMoney(d.Hours()*r.rate*wkStandbyShare), wkAmber
	case r.kind == "Standby":
		bill, col = "50% · no rate", wkAmber
	case !r.billable:
		bill = "internal"
	case r.rate > 0:
		bill, col = wkMoney(d.Hours()*r.rate), wkGreen
	default:
		bill, col = "yes · no rate", wkGreen
	}
	k.textR(wkFont("semi", 19), wkX1-24, cy+8, col, bill)
	if r.tap != "" {
		k.btn(r.tap, k.R(wkX0, ry, wkX1, ry+86))
	}
}

func init() {
	// wk:standby:<date|HH:MM> switches a gap between standby and lunch.
	wkActions["standby"] = func(c *console, key string) {
		now := time.Now().In(displayZone)
		_, days := wkLogDays(wkEntries(), wkProjects(), now)
		d, _, _ := strings.Cut(key, "|")
		next := "lunch"
		for _, r := range days[d] {
			if r.gap && r.gapKey == key && r.kind == "Lunch" {
				next = "standby"
			}
		}
		setStandbyChoice(key, next)
		if next == "lunch" {
			c.toastMsg("Marked as lunch: not billed")
		} else {
			c.toastMsg("Marked as standby: billed at 50%")
		}
		c.somaUI.keep = true
	}
	// wk:srange:from|to picks a day for the custom period.
	wkActions["srange"] = func(c *console, which string) {
		cur := standbyChoices()["_"+which]
		title := "Show from"
		if which == "to" {
			title = "Show up to"
		}
		c.wkPickDate("srange:"+which, cur, title)
	}
	wkDateSetters["srange"] = func(c *console, which, date string) {
		setStandbyChoice("_"+which, date)
		c.somaUI.keep = true
	}
}
