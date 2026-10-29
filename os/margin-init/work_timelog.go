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
	"time"

	"margin-init/ui"
)

// Time > Entries, laid out like the workstation's Entries list on the PC (TimePage.tsx): each day,
// each piece of work with its start, end, hours, type, what was done and whether it's billed.
// Work log shifts (worklog.ts) are listed with the time entries, so work logged on either PC page
// shows here. Between them, the empty time of a day is standby (waiting on the client), billed at
// half the rate of the work before it; one gap around midday is taken as lunch and not billed.
// Tapping a gap switches it between standby and lunch (kept in /data/condor/standby.json).

const wkStandbyShare = 0.5

type wkLogRow struct {
	start, end time.Time
	kind       string // "Entry", "Steps", "Work log"; "Standby" / "Lunch" for gaps
	col        color.RGBA
	title, sub string
	billable   bool
	rate       float64 // an hour
	tap        string  // button id ("" none)
	gap        bool
	gapKey     string
}

func (r wkLogRow) dur() time.Duration { return max(0, r.end.Sub(r.start)) }

// --- lunch / standby choices -------------------------------------------------------------------

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

// wkLogDays is every day with work on it, newest first, and its rows in time order with the gaps
// between them.
func wkLogDays(entries []wkEntry, byID map[string]wkProject, now time.Time) ([]string, map[string][]wkLogRow) {
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
	days := map[string][]wkLogRow{}
	hasBreak := map[string]bool{}
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
		days[d] = append(days[d], wkLogRow{start: e.Start, end: end, kind: kind, col: col, title: title, sub: sub,
			billable: e.Billable, rate: p.Rate, tap: "wk:sheet-open:entry:" + e.ID})
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
		end = minTime(end, start.Add(16*time.Hour))
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
		if bs, ok := o["breaks"].([]any); ok && len(bs) > 0 {
			hasBreak[d] = true
		}
		days[d] = append(days[d], wkLogRow{start: start, end: end, kind: "Work log", col: wkGreen, title: title,
			sub: client, billable: true, rate: num(rates[client])})
	}

	choice := standbyChoices()
	var keys []string
	for d, rows := range days {
		keys = append(keys, d)
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].start.Before(rows[j].start) })
		var out []wkLogRow
		var reach time.Time // the latest end so far (rows can overlap)
		var last wkLogRow
		lunchTaken := hasBreak[d] // a break logged in the work log is that day's lunch
		for i, r := range rows {
			if i > 0 && r.start.Sub(reach) >= 5*time.Minute {
				g := wkLogRow{start: reach, end: r.start, gap: true}
				g.gapKey = d + "|" + reach.In(displayZone).Format("15:04")
				// Billed at the rate of the work before it (the client being waited on).
				if last.billable {
					g.rate = last.rate
				} else if r.billable {
					g.rate = r.rate
				}
				lunch := false
				switch choice[g.gapKey] {
				case "lunch":
					lunch = true
				case "standby":
				default:
					h := reach.In(displayZone)
					mins := h.Hour()*60 + h.Minute()
					if !lunchTaken && mins >= 11*60+30 && mins <= 14*60+30 && g.dur() <= 90*time.Minute {
						lunch = true
					}
				}
				if lunch {
					lunchTaken = true
					g.kind, g.col, g.title = "Lunch", wkMuted, "Lunch"
				} else {
					g.kind, g.col, g.title, g.billable = "Standby", wkAmber, "Standby · waiting on the client", true
				}
				g.tap = "wk:standby:" + g.gapKey
				out = append(out, g)
			}
			out = append(out, r)
			if r.end.After(reach) {
				reach, last = r.end, r
			}
		}
		days[d] = out
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	return keys, days
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// wkDayFigures is a day's worked time (overlaps counted once), standby, and what it's all worth.
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
		s := r.start
		if s.Before(reach) {
			s = reach
		}
		if r.end.After(s) {
			worked += r.end.Sub(s)
			if r.billable {
				value += r.end.Sub(s).Hours() * r.rate
			}
			reach = r.end
		}
	}
	return
}

// --- the page -----------------------------------------------------------------------------------

func (k *wk) timeEntriesTab(entries []wkEntry, byID map[string]wkProject, now time.Time, y int) int {
	keys, days := wkLogDays(entries, byID, now)

	// This week: worked, its value, standby and its value.
	mon := somaDate(weekStart(now))
	var worked, standby time.Duration
	var value, sval float64
	for _, d := range keys {
		if d >= mon {
			w, s, v, sv := wkDayFigures(days[d])
			worked, standby, value, sval = worked+w, standby+s, value+v, sval+sv
		}
	}
	box := k.row4(y)
	k.small(box(0), "clock", "Worked", wkHM(worked), "", "this week", wkFG)
	k.small(box(1), "circle-dollar-sign", "Billable", wkMoney(value), "", "work at each rate", wkFG)
	k.small(box(2), "hourglass", "Standby", wkHM(standby), "", "gaps between work, not lunch", wkAmber)
	k.small(box(3), "circle-dollar-sign", "Standby value", wkMoney(sval), "", "at half the project rate", wkLime)
	y += 180
	k.text(wkFont("regular", 18), wkX0+8, y+24, wkFaint, clip(wkFont("regular", 18),
		"Tap a gap to switch it between standby and lunch.   Sync: "+soma.status(), wkX1-wkX0-16))
	y += 44

	shown := 14 * (1 + k.sel("more", 0)) // days
	for i, d := range keys {
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
		// Column heads, as on the PC.
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
	if len(keys) == 0 {
		k.emptyNote(wkX0+20, y+40, "No time logged yet.   Sync: "+soma.status())
		y += 80
	}
	return y
}

func (k *wk) logRow(r wkLogRow, ry int, now time.Time) {
	cy := ry + 44
	fg := wkFG
	if r.kind == "Lunch" {
		fg = wkMuted
	}
	end := r.end.In(displayZone).Format("15:04")
	if !r.gap && r.end.Equal(now) {
		end = "now"
	}
	k.text(wkFont("semi", 21), wkX0+24, cy+8, fg, r.start.In(displayZone).Format("15:04")+" – "+end)
	d := r.dur()
	k.textR(wkFont("bold", 21), wkX0+300, cy+8, fg, fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60))
	// Type, as a coloured tag.
	tf := wkFont("semi", 16)
	tw := ui.TextWidth(tf, r.kind) + 24
	tr := k.R(wkX0+324, cy-16, wkX0+324+tw, cy+16)
	ui.RoundRect(k.img, tr, 16, blend(wkCard, r.col, 0.18))
	apText(k.img, tf, tr.Min.X+12, k.Y(cy)+6, r.col, r.kind)
	// What was done.
	w := wkX1 - 200 - (wkX0 + 470)
	if r.gap {
		k.text(wkFont("semi", 20), wkX0+470, cy+8, fg, clip(wkFont("semi", 20), r.title, w))
	} else {
		k.text(wkFont("semi", 20), wkX0+470, cy-4, fg, clip(wkFont("semi", 20), r.title, w))
		k.text(wkFont("regular", 17), wkX0+470, cy+22, wkFaint, clip(wkFont("regular", 17), r.sub, w))
	}
	// Billable: the money, or why not.
	bill := "—"
	col := wkFaint
	switch {
	case r.kind == "Lunch":
		bill = "not billed"
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
		projects := wkProjects()
		byID := map[string]wkProject{}
		for _, p := range projects {
			byID[p.ID] = p
		}
		_, days := wkLogDays(wkEntries(), byID, now)
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
}
