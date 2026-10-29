package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// The other pages' tabs and buttons: Overview's results, clients and activity, Create, the
// bell and Customize; Time's entries, rates and invoices, adding time, breaks and switching;
// Money's invoices, spending and goals; the work log's shifts and clients; findings grouped;
// Nutrition's log and week; Mind's notes; Settings' accent colour.

// --- overview ---------------------------------------------------------------------------------------

func (k *wk) overviewTab(tab int) int {
	switch tab {
	case 1:
		return k.results()
	case 2:
		return k.clientsTab()
	}
	return k.activity()
}

// results is the last six months: hours, what they were worth, money in, projects finished.
func (k *wk) results() int {
	now := time.Now().In(displayZone)
	projects := wkProjects()
	entries := wkEntries()
	type mrow struct {
		m                time.Time
		hours, val, inc  float64
		finished, stepsN int
	}
	var rows []mrow
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	for i := 5; i >= 0; i-- {
		m := month.AddDate(0, -i, 0)
		r := mrow{m: m}
		v, _, all := wkValue(entries, projects, m, m.AddDate(0, 1, 0), now)
		r.val, r.hours = v, all.Hours()
		r.inc, _, _ = wkMonthMoney(m)
		for _, p := range projects {
			if p.Status == "done" && !p.Touched.Before(m) && p.Touched.Before(m.AddDate(0, 1, 0)) {
				r.finished++
			}
			for _, s := range p.Steps {
				if s.Done && !s.At.Before(m) && s.At.Before(m.AddDate(0, 1, 0)) {
					r.stepsN++
				}
			}
		}
		rows = append(rows, r)
	}
	cur, prev := rows[5], rows[4]
	box := k.row4(312)
	k.small(box(0), "clock", "Hours · "+now.Format("Jan"), fmt.Sprintf("%.0f", cur.hours), "h", fmt.Sprintf("%.0f h in %s", prev.hours, prev.m.Format("Jan")), wkFG)
	k.small(box(1), "pound-sterling", "Billable value", wkMoney(cur.val), "", "at your rates", wkLime)
	k.small(box(2), "arrow-down-left", "Money in", wkMoney(cur.inc), "", "logged in Money", wkFG)
	k.small(box(3), "list-checks", "Steps done", fmt.Sprint(cur.stepsN), "", fmt.Sprintf("%d projects finished", cur.finished), wkFG)
	y := 498
	cr := image.Rect(wkX0, y, wkX1, y+380)
	k.card(cr)
	k.cardHead(wkX0+28, y+46, "chart-column", "Value and money in", "· six months")
	hi := 1.0
	for _, r := range rows {
		hi = max(hi, r.val, r.inc)
	}
	base := y + 320
	for i, r := range rows {
		gx := 260 + i*145
		vh := int(r.val / hi * 220)
		ih := int(r.inc / hi * 220)
		if vh > 0 {
			ui.RoundRect(k.img, k.R(gx, base-vh, gx+40, base), 6, wkLime)
		}
		if ih > 0 {
			ui.RoundRect(k.img, k.R(gx+46, base-ih, gx+86, base), 6, wkBlue)
		}
		k.textC(wkFont("regular", 17), gx+43, base+30, wkFaint, r.m.Format("Jan"))
	}
	ui.RoundRect(k.img, k.R(820, y+30, 836, y+46), 4, wkLime)
	k.text(wkFont("regular", 18), 844, y+46, wkText2, "Value")
	ui.RoundRect(k.img, k.R(930, y+30, 946, y+46), 4, wkBlue)
	k.text(wkFont("regular", 18), 954, y+46, wkText2, "Money in")
	y = cr.Max.Y + 24
	tc := image.Rect(wkX0, y, wkX1, y+90+64*len(rows))
	k.card(tc)
	hf := wkFont("semi", 16)
	for i, h := range []string{"MONTH", "HOURS", "VALUE", "MONEY IN", "STEPS", "FINISHED"} {
		x := []int{188, 520, 700, 880, 1010, 1136}[i]
		if i == 0 {
			k.text(hf, x, y+50, wkFaint, h)
		} else {
			k.textR(hf, x, y+50, wkFaint, h)
		}
	}
	ry := y + 70
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		f := wkFont("regular", 21)
		k.text(wkFont("semi", 21), 188, ry+42, wkFG, r.m.Format("January 2006"))
		k.textR(f, 520, ry+42, wkFG, fmt.Sprintf("%.1f", r.hours))
		k.textR(f, 700, ry+42, wkFG, wkMoney(r.val))
		k.textR(f, 880, ry+42, wkFG, wkMoney(r.inc))
		k.textR(f, 1010, ry+42, wkFG, fmt.Sprint(r.stepsN))
		k.textR(f, 1136, ry+42, wkFG, fmt.Sprint(r.finished))
		ry += 64
	}
	return tc.Max.Y + 20
}

// clientsTab is each client: projects, hours and value this month and in all.
func (k *wk) clientsTab() int {
	now := time.Now().In(displayZone)
	projects := wkProjects()
	entries := wkEntries()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
	clients := wkClients()
	y := 312
	cr := image.Rect(wkX0, y, wkX1, y+20+120*max(len(clients), 1))
	k.card(cr)
	ry := y + 10
	for i, cl := range clients {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		var mine []wkProject
		active := 0
		for _, p := range projects {
			if p.Client == cl {
				mine = append(mine, p)
				if p.Status == "active" {
					active++
				}
			}
		}
		mv, _, mh := wkValue(entries, mine, month, month.AddDate(0, 1, 0), now)
		av, _, ah := wkValue(entries, mine, time.Time{}, now.AddDate(1, 0, 0), now)
		ui.RoundRect(k.img, k.R(188, ry+28, 240, ry+80), 14, blend(wkCard, wkBlue, 0.18))
		apTextCenter(k.img, wkFont("bold", 22), 214, k.Y(ry+54), wkBlue, wkInitials(cl))
		k.text(wkFont("semi", 23), 260, ry+50, wkFG, clip(wkFont("semi", 23), cl, 420))
		sub := fmt.Sprintf("%d projects · %d active", len(mine), active)
		if r := num(rates[cl]); r > 0 {
			sub += " · work log " + wkMoney(r) + "/h"
		}
		k.text(wkFont("regular", 18), 260, ry+82, wkFaint, sub)
		k.textR(wkFont("bold", 22), 880, ry+50, wkFG, fmt.Sprintf("%.1f h", mh.Hours()))
		k.textR(wkFont("regular", 17), 880, ry+80, wkFaint, wkMoney(mv)+" this month")
		k.textR(wkFont("bold", 22), 1100, ry+50, wkFG, fmt.Sprintf("%.0f h", ah.Hours()))
		k.textR(wkFont("regular", 17), 1100, ry+80, wkFaint, wkMoney(av)+" in all")
		k.icon("chevron-right", 1124, ry+60, 20, wkMuted)
		k.btn("wk:clientprojects:"+cl, k.R(wkX0, ry, wkX1, ry+120))
		ry += 120
	}
	if len(clients) == 0 {
		k.emptyNote(188, ry+56, "Clients come from the projects: set one on a project.")
	}
	return cr.Max.Y + 20
}

type wkEvent struct {
	at                  time.Time
	icon, title, detail string
	col                 color.RGBA
	id                  string
}

// activity is what happened lately, newest first: time, steps, findings, shifts, sessions.
func (k *wk) activity() int {
	now := time.Now()
	projects := wkProjects()
	var evs []wkEvent
	name := map[string]string{}
	for _, p := range projects {
		name[p.ID] = p.Name
		for _, s := range p.Steps {
			if s.Done && !s.At.IsZero() {
				evs = append(evs, wkEvent{s.At, "circle-check", "Step done: " + s.Label, p.Name, wkGreen, "wk:proj:" + p.ID})
			}
		}
		if !p.Created.IsZero() {
			evs = append(evs, wkEvent{p.Created, "folder-open", "Project started: " + p.Name, p.Client, wkBlue, "wk:proj:" + p.ID})
		}
		for _, n := range p.Log {
			evs = append(evs, wkEvent{n.At, "sticky-note", n.Text, p.Name, wkPurple, "wk:proj:" + p.ID})
		}
	}
	for _, e := range wkEntries() {
		d := e.dur(now)
		t := e.Note
		if t == "" {
			t = name[e.ProjectID]
		}
		evs = append(evs, wkEvent{e.Start, "timer", fmt.Sprintf("%d:%02d · %s", int(d.Hours()), int(d.Minutes())%60, t), name[e.ProjectID], wkLime, "wk:sheet-open:entry:" + e.ID})
	}
	for _, o := range somaList("findings") {
		evs = append(evs, wkEvent{msTime(o["createdAt"]), "shield-alert", "Finding: " + str(o["title"]), name[str(o["projectId"])], sevCol[str(o["severity"])], "wk:sheet-open:finding:" + str(o["id"])})
	}
	for _, s := range somaShifts() {
		evs = append(evs, wkEvent{s.Start, "briefcase", "Shift · " + s.Client, wkHM(s.Worked) + " worked", wkAmber, "wk:go:worklog"})
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].at.After(evs[j].at) })
	if len(evs) > 50 {
		evs = evs[:50]
	}
	y := 312
	cr := image.Rect(wkX0, y, wkX1, y+20+92*max(len(evs), 1))
	k.card(cr)
	ry := y + 10
	day := ""
	for i, e := range evs {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		cy := ry + 46
		col := e.col
		if col.A == 0 {
			col = wkText2
		}
		ui.RoundRect(k.img, k.R(188, cy-24, 236, cy+24), 12, blend(wkCard, col, 0.16))
		lucideIcon(k.img, e.icon, 212, k.Y(cy), 24, col)
		k.text(wkFont("semi", 21), 256, cy-4, wkFG, clip(wkFont("semi", 21), e.title, 700))
		k.text(wkFont("regular", 18), 256, cy+24, wkFaint, clip(wkFont("regular", 18), e.detail, 600))
		when := e.at.In(displayZone).Format("15:04")
		if d := somaDate(e.at); d != day {
			day = d
			when = dayLabel(d) + " · " + when
		}
		k.textR(wkFont("regular", 18), 1136, cy+8, wkMuted, when)
		k.btn(e.id, k.R(wkX0, ry, wkX1, ry+92))
		ry += 92
	}
	if len(evs) == 0 {
		k.emptyNote(188, ry+56, "Nothing yet.")
	}
	return cr.Max.Y + 20
}

func wkHM(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// --- time ---------------------------------------------------------------------------------------------

func (k *wk) timeTab(tab int) int {
	now := time.Now().In(displayZone)
	entries := wkEntries()
	projects := wkProjects()
	byID := map[string]wkProject{}
	for _, p := range projects {
		byID[p.ID] = p
	}
	y := 312
	switch tab {
	case 1: // every entry by day, as on the PC, with the standby between
		return k.timeEntriesTab(entries, byID, now, y)
	case 2: // rates
		cr := image.Rect(wkX0, y, wkX1, y+90+84*max(len(projects), 1))
		k.card(cr)
		k.cardHead(wkX0+28, y+48, "circle-dollar-sign", "Project rates", "· what an hour on each is worth")
		ry := y + 80
		for _, p := range projects {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			ui.RoundRect(k.img, k.R(188, ry+34, 204, ry+50), 4, wkColour(p, p.order))
			k.text(wkFont("semi", 21), 220, ry+48, wkFG, clip(wkFont("semi", 21), p.Name, 520))
			k.text(wkFont("regular", 17), 220, ry+74, wkFaint, p.Client)
			v := "Not set"
			if p.Rate > 0 {
				v = wkMoney(p.Rate) + " / h"
			}
			k.textR(wkFont("medium", 21), 1100, ry+52, wkText2, v)
			k.icon("pencil", 1124, ry+44, 20, wkFaint)
			k.btn("wk:type-open:prate:"+p.ID, k.R(wkX0, ry, wkX1, ry+84))
			ry += 84
		}
		return cr.Max.Y + 20
	}
	// invoiced: every bill, newest first
	invs := wkInvoices(projects, entries, now)
	return k.invoiceList(invs, y)
}

func (k *wk) invoiceList(invs []wkInvoice, y int) int {
	cr := image.Rect(wkX0, y, wkX1, y+90+94*max(len(invs), 1))
	k.card(cr)
	k.cardHead(wkX0+28, y+48, "receipt", "Invoices", "· billable time by client and month; paid once Money has income from them")
	ry := y + 80
	for _, v := range invs {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("bold", 22), 187, ry+42, wkFG, v.No)
		k.text(wkFont("regular", 19), 187, ry+71, wkFaint, clip(wkFont("regular", 19), v.What, 270))
		k.text(wkFont("regular", 22), 478, ry+56, wkFG, clip(wkFont("regular", 22), v.Client, 300))
		k.textR(wkFont("bold", 22), 903, ry+56, wkFG, wkMoney(v.Amount))
		k.badge(1136, ry+48, v.Status, v.col)
		if v.Status != "Paid" && v.Status != "Draft" {
			k.btn(fmt.Sprintf("wk:sheet-open:invpaid:%s|%s|%.0f", v.Client, v.month.Format("2006-01"), v.Amount), k.R(wkX0, ry, wkX1, ry+94))
		}
		ry += 94
	}
	if len(invs) == 0 {
		k.emptyNote(187, ry+54, "No billable time yet: set a rate on a project.")
	}
	return cr.Max.Y + 20
}

// --- money ----------------------------------------------------------------------------------------------

func (k *wk) moneyTab(tab int) int {
	now := time.Now().In(displayZone)
	y := 386
	switch tab {
	case 1:
		return k.invoiceList(wkInvoices(wkProjects(), wkEntries(), now), y)
	case 2: // spending: this month by category, then every entry
		month := now.Format("2006-01")
		mm := somaMoneyMonth(month)
		budgets, _ := jsonValue(soma.get("settings.categoryBudgets")).(map[string]any)
		type cat struct {
			n string
			v float64
		}
		var cs []cat
		for n, v := range mm.ByCategory {
			cs = append(cs, cat{n, v})
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].v > cs[j].v })
		cr := image.Rect(wkX0, y, wkX1, y+90+72*max(len(cs), 1))
		k.card(cr)
		k.cardHead(wkX0+28, y+48, "chart-pie", "Spent this month", "· "+wkMoney(mm.Spend))
		ry := y + 80
		for _, c := range cs {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			k.text(wkFont("semi", 21), 188, ry+44, wkFG, clip(wkFont("semi", 21), c.n, 320))
			b := num(budgets[c.n])
			frac, col := c.v/math.Max(mm.Spend, 1), wkBlue
			if b > 0 {
				frac = c.v / b
				if frac > 1 {
					col = wkRed
				}
			}
			k.hBar(k.R(540, ry+32, 900, ry+42), frac, col)
			v := wkMoney(c.v)
			if b > 0 {
				v += " of " + wkMoney(b)
			}
			k.textR(wkFont("semi", 20), 1136, ry+44, wkFG, v)
			ry += 72
		}
		y = cr.Max.Y + 24
		var list []obj
		for _, o := range somaList("ledger") {
			if strings.HasPrefix(str(o["date"]), month) {
				list = append(list, o)
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return str(list[i]["date"]) > str(list[j]["date"]) })
		lc := image.Rect(wkX0, y, wkX1, y+90+80*max(len(list), 1))
		k.card(lc)
		k.cardHead(wkX0+28, y+48, "list", "This month's entries", "· tap one to change it")
		ry = y + 80
		for _, o := range list {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			col := wkRed
			sign := "−"
			switch str(o["kind"]) {
			case "income":
				col, sign = wkGreen, "+"
			case "save":
				col, sign = wkBlue, "→"
			}
			k.text(wkFont("semi", 21), 188, ry+40, wkFG, clip(wkFont("semi", 21), str(o["category"]), 500))
			sub := wkDate(str(o["date"]))
			if n := str(o["note"]); n != "" {
				sub += " · " + n
			}
			k.text(wkFont("regular", 17), 188, ry+66, wkFaint, clip(wkFont("regular", 17), sub, 600))
			amt := fmtThousands(int(math.Abs(num(o["amount"]))))
			if c := str(o["currency"]); c != "" {
				amt += " " + c
			} else {
				amt = wkMoney(math.Abs(num(o["amount"])))
			}
			k.textR(wkFont("bold", 22), 1136, ry+50, col, sign+" "+amt)
			k.btn("wk:sheet-open:ledger:"+str(o["id"]), k.R(wkX0, ry, wkX1, ry+80))
			ry += 80
		}
		if len(list) == 0 {
			k.emptyNote(188, ry+50, "Nothing logged this month. Add puts money in or out.")
		}
		return lc.Max.Y + 20
	}
	// goals: savings goals, what's put towards each
	goals, _ := jsonValue(soma.get("settings.savingsGoals")).([]any)
	ledger := somaLedgerFull()
	cr := image.Rect(wkX0, y, wkX1, y+90+110*max(len(goals), 1))
	k.card(cr)
	k.cardHead(wkX0+28, y+48, "piggy-bank", "Savings goals", "")
	ry := y + 80
	for _, g := range goals {
		o, _ := g.(map[string]any)
		saved := 0.0
		for _, l := range ledger {
			if l.Kind == "save" && l.Goal == str(o["id"]) {
				saved += l.Amount
			}
		}
		t := num(o["target"])
		col := somaColour(str(o["color"]), wkLime)
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 22), 188, ry+44, wkFG, clip(wkFont("semi", 22), str(o["name"]), 500))
		k.hBar(k.R(188, ry+68, 900, ry+78), saved/math.Max(t, 1), col)
		k.textR(wkFont("bold", 22), 1136, ry+44, wkFG, wkMoney(saved))
		k.textR(wkFont("regular", 17), 1136, ry+78, wkFaint, "of "+wkMoney(t))
		k.btn("wk:type-open:savegoal:"+str(o["id"]), k.R(wkX0, ry, wkX1, ry+110))
		ry += 110
	}
	if len(goals) == 0 {
		k.emptyNote(188, ry+50, "No savings goals: they're set up in Soma's Money on the phone.")
	}
	k.text(wkFont("regular", 18), wkX0+4, cr.Max.Y+36, wkFaint, "Tap a goal to put money towards it.")
	return cr.Max.Y + 60
}

// wkAddLedger is addLedger: money in, out, or saved, in the base currency, today.
func wkAddLedger(kind string, amount float64, category, note, goal string) {
	id := somaNewID()
	o := obj{"id": id, "date": somaToday(), "kind": kind, "amount": math.Abs(amount), "category": category}
	if note != "" {
		o["note"] = note
	}
	if goal != "" {
		o["goalId"] = goal
	}
	somaAdd("ledger", id, o)
}

// parseMoney reads "450 Food lunch with Sam": the amount, the category, a note after it.
func parseMoney(s string) (float64, string, string, bool) {
	f := strings.Fields(strings.ReplaceAll(s, ",", "."))
	if len(f) == 0 {
		return 0, "", "", false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v <= 0 {
		return 0, "", "", false
	}
	cat, note := "Other", ""
	if len(f) > 1 {
		cat = wkTitle(f[1])
	}
	if len(f) > 2 {
		note = strings.Join(f[2:], " ")
	}
	return v, cat, note, true
}

// --- the work log -------------------------------------------------------------------------------------

func (k *wk) worklogTab(tab int, y int) int {
	shifts := somaShifts()
	sort.SliceStable(shifts, func(i, j int) bool { return shifts[i].Start.After(shifts[j].Start) })
	if tab == 1 {
		if len(shifts) > 80 {
			shifts = shifts[:80]
		}
		cr := image.Rect(wkX0, y, wkX1, y+20+90*max(len(shifts), 1))
		k.card(cr)
		ry := y + 10
		for i, s := range shifts {
			if i > 0 {
				ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			}
			end := "now"
			if !s.End.IsZero() {
				end = s.End.In(displayZone).Format("15:04")
			}
			k.text(wkFont("semi", 22), 188, ry+42, wkFG, clip(wkFont("semi", 22), s.Client, 500))
			k.text(wkFont("regular", 18), 188, ry+70, wkFaint, s.Start.In(displayZone).Format("Mon 2 Jan · 15:04")+" – "+end)
			k.textR(wkFont("bold", 22), 1100, ry+54, wkFG, wkHM(s.Worked))
			k.icon("ellipsis", 1124, ry+46, 22, wkFaint)
			k.btn("wk:sheet-open:shift:"+s.ID, k.R(wkX0, ry, wkX1, ry+90))
			ry += 90
		}
		if len(shifts) == 0 {
			k.emptyNote(188, ry+50, "No shifts yet.")
		}
		return cr.Max.Y + 20
	}
	// clients: all time, and their rates
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
	tot := map[string]time.Duration{}
	n := map[string]int{}
	for _, s := range shifts {
		tot[s.Client] += s.Worked
		n[s.Client]++
	}
	var cls []string
	for c := range tot {
		cls = append(cls, c)
	}
	for c := range rates {
		if _, ok := tot[c]; !ok {
			cls = append(cls, c)
		}
	}
	sort.Strings(cls)
	cr := image.Rect(wkX0, y, wkX1, y+20+96*max(len(cls), 1))
	k.card(cr)
	ry := y + 10
	for i, c := range cls {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		k.text(wkFont("semi", 22), 188, ry+44, wkFG, clip(wkFont("semi", 22), c, 500))
		k.text(wkFont("regular", 18), 188, ry+72, wkFaint, fmt.Sprintf("%d shifts · %s worked", n[c], wkHM(tot[c])))
		r := num(rates[c])
		v := "no rate"
		if r > 0 {
			v = wkMoney(r) + "/h · " + wkMoney(tot[c].Hours()*r)
		}
		k.textR(wkFont("medium", 20), 1100, ry+56, wkText2, v)
		k.icon("pencil", 1124, ry+48, 20, wkFaint)
		k.btn("so:type:set:rate:"+c, k.R(wkX0, ry, wkX1, ry+96))
		ry += 96
	}
	if len(cls) == 0 {
		k.emptyNote(188, ry+50, "Clients appear once you log a shift.")
	}
	return cr.Max.Y + 20
}

// --- findings ---------------------------------------------------------------------------------------

// findingGroups is the list by project (tab 1) or by severity (tab 2), a heading for each.
func (k *wk) findingGroups(list []wkFinding, name map[string]string, tab, y int) int {
	key := func(f wkFinding) string {
		if tab == 1 {
			if n := name[f.ProjectID]; n != "" {
				return n
			}
			return "No project"
		}
		return wkTitle(f.Severity)
	}
	var order []string
	groups := map[string][]wkFinding{}
	for _, f := range list {
		g := key(f)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], f)
	}
	if tab == 1 {
		sort.Strings(order)
	}
	for _, g := range order {
		k.text(wkFont("semi", 19), wkX0+8, y+28, wkMuted, strings.ToUpper(g)+fmt.Sprintf("  ·  %d", len(groups[g])))
		y = k.findingList(groups[g], name, y+44) + 20
	}
	if len(order) == 0 {
		return k.findingList(nil, name, y)
	}
	return y
}

// --- mind: the notes ------------------------------------------------------------------------------------

func (k *wk) notesTab() int {
	notes := somaNotes()
	q := strings.ToLower(k.c.somaUI.noteQuery)
	y := 312
	k.boxBtn("wk:type-open:newnote:", wkX0, y, 560, y+64, "plus", "New note", wkLime, wkOnLime)
	lbl := "Search notes"
	if q != "" {
		lbl = "“" + k.c.somaUI.noteQuery + "” ×"
	}
	k.boxBtn("wk:notesearch", 580, y, wkX1, y+64, "search", clip(wkFont("semi", 22), lbl, 400), color.RGBA{}, wkFG)
	y += 90
	var list []somaNote
	for _, n := range notes {
		if q == "" || strings.Contains(strings.ToLower(n.ID+" "+n.Text), q) {
			list = append(list, n)
		}
	}
	if len(list) > 80 {
		list = list[:80]
	}
	cr := image.Rect(wkX0, y, wkX1, y+20+96*max(len(list), 1))
	k.card(cr)
	ry := y + 10
	for i, n := range list {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		k.text(wkFont("semi", 22), 188, ry+40, wkFG, clip(wkFont("semi", 22), n.title(), 700))
		sub := strings.Join(strings.Fields(n.Text), " ")
		if f := n.folder(); f != "" {
			sub = f + " · " + sub
		}
		k.text(wkFont("regular", 18), 188, ry+70, wkFaint, clip(wkFont("regular", 18), sub, 820))
		if n.Mtime > 0 {
			k.textR(wkFont("regular", 17), 1136, ry+40, wkFaint, time.UnixMilli(n.Mtime).In(displayZone).Format("2 Jan"))
		}
		k.btn("wk:note:"+n.ID, k.R(wkX0, ry, wkX1, ry+96))
		ry += 96
	}
	if len(list) == 0 {
		k.emptyNote(188, ry+50, "No notes here. The vault fills in once the PC's Soma has it linked.")
	}
	return cr.Max.Y + 20
}

// notePage is a note: its lines, checkboxes to tick, a line or a task to add.
func (k *wk) notePage(id string) int {
	n := somaNoteByID(id)
	if n == nil {
		k.c.somaUI.workSub = ""
		return k.mindPage()
	}
	k.header([]string{"Mind", "Notes ›"}, clip(wkFont("bold", 42), n.title(), 760), n.folder(), color.RGBA{})
	k.square("wk:back", "chevron-left", wkX1)
	y := 240
	lines := strings.Split(n.Text, "\n")
	f := wkFont("regular", 22)
	type ln struct {
		i            int
		text         string
		task, done   bool
		head, bullet bool
	}
	var shown []ln
	for i, l := range lines {
		task, done, text := noteTask(l)
		t := strings.TrimSpace(l)
		switch {
		case task:
			shown = append(shown, ln{i: i, text: text, task: true, done: done})
		case strings.HasPrefix(t, "#"):
			shown = append(shown, ln{i: i, text: strings.TrimLeft(t, "# "), head: true})
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			shown = append(shown, ln{i: i, text: t[2:], bullet: true})
		default:
			shown = append(shown, ln{i: i, text: t})
		}
	}
	h := 60
	for _, l := range shown {
		h += max(len(wkWrap(f, l.text, 860, 6)), 1) * 34
	}
	cr := image.Rect(wkX0, y, wkX1, y+h+20)
	k.card(cr)
	ry := y + 40
	for _, l := range shown {
		x := 188
		face, col := f, wkText2
		switch {
		case l.head:
			face, col = wkFont("bold", 26), wkFG
		case l.task:
			k.checkbox(188, ry+6, l.done)
			k.btn(fmt.Sprintf("wk:notetick:%d", l.i), k.R(wkX0, ry-20, wkX1, ry+30))
			x = 240
			if l.done {
				col = wkFaint
			} else {
				col = wkFG
			}
		case l.bullet:
			ui.Circle(k.img, 196, k.Y(ry+2), 4, wkMuted)
			x = 216
		}
		wl := wkWrap(face, l.text, 900-(x-188), 6)
		if len(wl) == 0 {
			wl = []string{""}
		}
		for _, w := range wl {
			k.text(face, x, ry+14, col, w)
			ry += 34
		}
	}
	y = cr.Max.Y + 20
	k.boxBtn("wk:type-open:notetask:"+id, wkX0, y, 640, y+64, "list-checks", "Add a task", wkLime, wkOnLime)
	k.boxBtn("wk:type-open:noteline:"+id, 660, y, wkX1, y+64, "plus", "Add a line", color.RGBA{}, wkFG)
	return y + 84
}

// --- settings: the accent -------------------------------------------------------------------------

func (k *wk) accentCard(y int) int {
	k.text(wkFont("semi", 18), wkX0+8, y+20, wkFaint, "APPEARANCE")
	y += 36
	cr := image.Rect(wkX0, y, wkX1, y+290)
	k.card(cr)
	k.text(wkFont("semi", 21), 188, y+48, wkFG, "Accent colour")
	k.text(wkFont("regular", 17), 188, y+76, wkFaint, "Soma's main colour, here and on the phone and the PC")
	cur := wkAccentHex()
	for i, a := range wkAccents {
		cx := 230 + (i%6)*150
		cy := y + 140 + (i/6)*96
		col := rgb(a.hex)
		hex := fmt.Sprintf("#%06x", a.hex)
		if hex == cur {
			ui.Circle(k.img, cx, k.Y(cy), 40, wkFG)
			ui.Circle(k.img, cx, k.Y(cy), 36, wkCard)
		}
		ui.Circle(k.img, cx, k.Y(cy), 32, col)
		if hex == cur {
			lucideIcon(k.img, "check", cx, k.Y(cy), 28, accentInk(col))
		}
		k.textC(wkFont("regular", 15), cx+70, cy+6, wkMuted, "")
		k.btn("wk:accent:"+hex, k.R(cx-46, cy-46, cx+46, cy+46))
		_ = a.name
	}
	return cr.Max.Y + 24
}

// --- actions ----------------------------------------------------------------------------------------

func init() {
	// Overview.
	wkActions["clientprojects"] = func(c *console, cl string) {
		c.somaUI.pf.client = cl
		c.wkGo("projects", "")
	}
	wkActions["create"] = func(c *console, _ string) { c.openSheet("create:") }
	wkSheets["create"] = func(k *wk, _ string) *wkSheet {
		return &wkSheet{title: "Create", rows: []wkRow{
			{id: "wk:newproject", label: "Project", icon: "folder-kanban", col: wkBlue},
			{id: "wk:create-todo", label: "To-do for today", icon: "list-checks", col: wkGreen},
			{id: "wk:sheet-open:switchproj:", label: "Start a timer", icon: "play", col: wkLime},
			{id: "wk:sheet-open:newentry:", label: "Time already spent", icon: "clock", col: wkLime},
			{id: "wk:type-open:finding:", label: "Finding", icon: "shield-alert", col: wkRed},
			{id: "so:type:clockin", label: "Clock in (work log)", icon: "briefcase", col: wkAmber},
			{id: "wk:logsession", label: "Training session", icon: "dumbbell", col: wkPurple},
			{id: "wk:type-open:newhabit:", label: "Habit", icon: "circle-check", col: rgb(0x34e0a1)},
			{id: "wk:type-open:food:" + somaToday(), label: "Food", icon: "utensils", col: rgb(0xff375f)},
			{id: "wk:type-open:money:spend", label: "Money spent", icon: "wallet", col: wkRed},
		}}
	}
	wkActions["create-todo"] = func(c *console, _ string) {
		c.wkType("newtask:"+somaToday(), "A to-do for today", "")
	}
	wkPrompts["finding"] = func(string) (string, string) { return "What was found (on your newest active project)", "" }
	wkTypers["finding"] = func(c *console, _, text string) { wkAddFinding(text) }
	wkActions["bell"] = func(c *console, _ string) { c.openSheet("attn:") }
	wkActions["attn"] = func(c *console, _ string) { c.openSheet("attn:") }
	wkSheets["attn"] = func(k *wk, _ string) *wkSheet {
		items := wkAttention(wkProjects(), somaTodos(), time.Now().In(displayZone))
		sh := &wkSheet{title: "Needs attention", sub: fmt.Sprintf("%d things late, stuck or serious", len(items))}
		for _, it := range items {
			id := ""
			if it.projectID != "" {
				id = "wk:proj:" + it.projectID
			}
			sh.rows = append(sh.rows, wkRow{id: id, label: it.title, icon: it.icon, col: it.col, detail: it.badge})
		}
		if len(items) == 0 {
			sh.rows = append(sh.rows, wkRow{kind: "text", detail: "Nothing late, nothing stuck."})
		}
		return sh
	}
	wkActions["customize"] = func(c *console, _ string) { c.openSheet("customize:") }
	wkSheets["customize"] = func(k *wk, _ string) *wkSheet {
		h := k.c.cfg.SomaHidden
		row := func(id, label, icon string) wkRow {
			d := "Shown"
			if h[id] {
				d = "Hidden"
			}
			return wkRow{id: "wk:ovtoggle:" + id, label: label, icon: icon, detail: d, sel: !h[id]}
		}
		return &wkSheet{title: "Customize the Overview", sub: "On this tablet", rows: []wkRow{
			row("rings", "Today's rings", "target"), row("chart", "Hours by project", "chart-column"), row("attention", "Needs attention and deadlines", "triangle-alert"),
		}}
	}
	wkActions["ovtoggle"] = func(c *console, id string) {
		if c.cfg.SomaHidden == nil {
			c.cfg.SomaHidden = map[string]bool{}
		}
		c.cfg.SomaHidden[id] = !c.cfg.SomaHidden[id]
		c.cfg.save()
		c.somaUI.keep = true
	}

	// Time.
	wkSheets["switchproj"] = func(k *wk, _ string) *wkSheet {
		run, running := wkRunning(wkEntries())
		sh := &wkSheet{title: "Start the timer on"}
		for _, p := range wkProjects() {
			if p.Status == "done" {
				continue
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:start:" + p.ID, label: p.Name, col: wkColour(p, p.order), detail: p.Client, sel: running && run.ProjectID == p.ID})
		}
		return sh
	}
	wkSheets["newentry"] = func(k *wk, _ string) *wkSheet {
		sh := &wkSheet{title: "Add time to"}
		for _, p := range wkProjects() {
			if p.Status == "done" {
				continue
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:sheet-open:addtime:" + p.ID, label: p.Name, col: wkColour(p, p.order), detail: p.Client})
		}
		return sh
	}
	wkActions["entry"] = func(c *console, _ string) { c.openSheet("newentry:") }
	wkActions["break"] = func(c *console, _ string) {
		if run, ok := wkRunning(wkEntries()); ok {
			c.somaUI.breakProj = run.ProjectID
			wkStopTimer()
			c.toastMsg("On a break: Resume starts the timer again")
			return
		}
		if p := c.somaUI.breakProj; p != "" {
			c.somaUI.breakProj = ""
			wkStartTimer(p)
			c.toastMsg("Back to work")
			return
		}
		c.openSheet("switchproj:")
	}
	wkActions["cal"] = func(c *console, _ string) {
		c.wkPickDate("tsweek:", somaToday(), "Show the timesheet for the week of")
	}
	wkDateRequired["tsweek"] = true
	wkDateSetters["tsweek"] = func(c *console, _, date string) {
		d, err := time.ParseInLocation("2006-01-02", date, displayZone)
		if err != nil {
			return
		}
		weeks := int(math.Round(weekStart(d).Sub(weekStart(time.Now())).Hours() / 24 / 7))
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel["timewk:week"] = fmt.Sprint(weeks)
		c.somaUI.workSel["timewk:tab"] = "0"
	}
	wkSheets["invpaid"] = func(k *wk, arg string) *wkSheet {
		p := strings.SplitN(arg, "|", 3)
		if len(p) != 3 {
			return nil
		}
		v, _ := strconv.ParseFloat(p[2], 64)
		return &wkSheet{title: p[0] + " paid?", sub: "Logs the money as income from them, which marks the invoice paid", rows: []wkRow{
			{id: "wk:invpaid:" + arg, label: "Yes: log " + wkMoney(v) + " in", icon: "circle-check", col: wkGreen},
		}}
	}
	wkActions["invpaid"] = func(c *console, arg string) {
		p := strings.SplitN(arg, "|", 3)
		if len(p) != 3 {
			return
		}
		v, _ := strconv.ParseFloat(p[2], 64)
		wkAddLedger("income", v, p[0], "Invoice "+p[1], "")
		c.toastMsg("Logged as paid")
	}

	// Money.
	wkActions["add"] = func(c *console, _ string) { c.openSheet("addmoney:") }
	wkSheets["addmoney"] = func(k *wk, _ string) *wkSheet {
		return &wkSheet{title: "Add money", rows: []wkRow{
			{id: "wk:type-open:money:spend", label: "Spent", icon: "arrow-up-right", col: wkRed},
			{id: "wk:type-open:money:income", label: "Came in", icon: "arrow-down-left", col: wkGreen},
		}}
	}
	wkPrompts["money"] = func(kind string) (string, string) {
		if kind == "income" {
			return "Amount and who from, e.g. 1200 Northbank October", ""
		}
		return "Amount and what for, e.g. 450 Food lunch", ""
	}
	wkTypers["money"] = func(c *console, kind, text string) {
		v, cat, note, ok := parseMoney(text)
		if !ok {
			c.toastMsg("Start with the amount: 450 Food")
			return
		}
		wkAddLedger(kind, v, cat, note, "")
		c.toastMsg(wkMoney(v) + " " + map[string]string{"income": "in", "spend": "out"}[kind])
	}
	wkPrompts["savegoal"] = func(string) (string, string) { return "How much to put towards it", "" }
	wkTypers["savegoal"] = func(c *console, id, text string) {
		v, _, _, ok := parseMoney(text)
		if !ok {
			return
		}
		name := "Savings"
		goals, _ := jsonValue(soma.get("settings.savingsGoals")).([]any)
		for _, g := range goals {
			if o, _ := g.(map[string]any); str(o["id"]) == id {
				name = str(o["name"])
			}
		}
		wkAddLedger("save", v, name, "", id)
	}
	wkSheets["ledger"] = func(k *wk, id string) *wkSheet {
		o := somaObj("ledger#" + id)
		if o == nil {
			return nil
		}
		return &wkSheet{title: str(o["category"]), sub: wkDate(str(o["date"])) + " · " + str(o["kind"]), rows: []wkRow{
			{id: "wk:type-open:ledamt:" + id, label: "Amount", icon: "pencil", detail: fmt.Sprintf("%g", num(o["amount"]))},
			{id: "wk:type-open:ledcat:" + id, label: "Category", icon: "tag", detail: str(o["category"])},
			{id: "wk:type-open:lednote:" + id, label: "Note", icon: "sticky-note", detail: str(o["note"])},
			{id: "wk:leddate:" + id, label: "Day", icon: "calendar", detail: wkDate(str(o["date"]))},
			{id: "wk:leddel:" + id, label: "Delete", icon: "trash-2", danger: true},
		}}
	}
	wkPrompts["ledamt"] = func(id string) (string, string) {
		return "Amount", fmt.Sprintf("%g", num(somaObj("ledger#" + id)["amount"]))
	}
	wkTypers["ledamt"] = func(c *console, id, text string) {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64); err == nil && v > 0 {
			somaPatch("ledger", id, func(o obj) { o["amount"] = v })
		}
	}
	wkPrompts["ledcat"] = func(id string) (string, string) { return "Category", str(somaObj("ledger#" + id)["category"]) }
	wkTypers["ledcat"] = func(c *console, id, text string) {
		if text != "" {
			somaPatch("ledger", id, func(o obj) { o["category"] = text })
		}
	}
	wkPrompts["lednote"] = func(id string) (string, string) { return "Note", str(somaObj("ledger#" + id)["note"]) }
	wkTypers["lednote"] = func(c *console, id, text string) {
		somaPatch("ledger", id, func(o obj) {
			if text == "" {
				delete(o, "note")
			} else {
				o["note"] = text
			}
		})
	}
	wkActions["leddate"] = func(c *console, id string) {
		c.wkPickDate("leddate:"+id, str(somaObj("ledger#" + id)["date"]), "Which day")
	}
	wkDateRequired["leddate"] = true
	wkDateSetters["leddate"] = func(c *console, id, date string) {
		if date != "" {
			somaPatch("ledger", id, func(o obj) { o["date"] = date })
		}
	}
	wkActions["leddel"] = func(c *console, id string) {
		o, at := somaRemove("ledger", id)
		if o != nil {
			c.offerUndo("Entry deleted", func() { somaRestore("ledger", o, at) })
		}
	}
	wkActions["invoice"] = func(c *console, _ string) {
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel[c.somaUI.work+"wk:tab"] = "1"
		c.somaUI.toTop = true
	}
	wkActions["export"] = func(c *console, _ string) {
		var b strings.Builder
		b.WriteString("| Date | Kind | Amount | Category | Note |\n|---|---|---|---|---|\n")
		list := somaList("ledger")
		sort.SliceStable(list, func(i, j int) bool { return str(list[i]["date"]) > str(list[j]["date"]) })
		for _, o := range list {
			fmt.Fprintf(&b, "| %s | %s | %g %s | %s | %s |\n", str(o["date"]), str(o["kind"]), num(o["amount"]), str(o["currency"]), str(o["category"]), str(o["note"]))
		}
		id := "Inbox/Money " + somaToday() + ".md"
		somaPutNote(id, "# Money, "+somaToday()+"\n\n"+b.String())
		c.toastMsg("Saved to your notes: " + strings.TrimPrefix(id, "Inbox/"))
	}
	wkPrompts["fx"] = func(cur string) (string, string) { return "Dinars for one " + cur, "" }
	wkActions["rates"] = func(c *console, _ string) { c.wkType("fx:EUR", "Dinars for one euro", "") }
	wkTypers["fx"] = func(c *console, cur, text string) {
		v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
		if err != nil || v <= 0 {
			return
		}
		r, _ := jsonValue(soma.get("settings.moneyRates")).(map[string]any)
		if r == nil {
			r = map[string]any{}
		}
		r[cur] = v
		somaPut("settings.moneyRates", r)
	}

	// The work log: a shift.
	wkSheets["shift"] = func(k *wk, id string) *wkSheet {
		o := somaObj("shifts#" + id)
		if o == nil {
			return nil
		}
		end := "still on"
		if num(o["end"]) > 0 {
			end = msTime(o["end"]).In(displayZone).Format("15:04")
		}
		sh := &wkSheet{title: str(o["client"]), sub: msTime(o["start"]).In(displayZone).Format("Monday 2 January"), rows: []wkRow{
			{id: "wk:type-open:shclient:" + id, label: "Client", icon: "building-2", detail: str(o["client"])},
			{kind: "stepper", label: "Started", detail: msTime(o["start"]).In(displayZone).Format("15:04"), minus: "wk:shset:" + id + ":start:-15", plus: "wk:shset:" + id + ":start:15"},
		}}
		if num(o["end"]) > 0 {
			sh.rows = append(sh.rows, wkRow{kind: "stepper", label: "Ended", detail: end, minus: "wk:shset:" + id + ":end:-15", plus: "wk:shset:" + id + ":end:15"})
		} else {
			sh.rows = append(sh.rows, wkRow{id: "wk:clockout", label: "Clock out now", icon: "square", col: wkRed})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:shdel:" + id, label: "Delete shift", icon: "trash-2", danger: true})
		return sh
	}
	wkPrompts["shclient"] = func(id string) (string, string) { return "Client", str(somaObj("shifts#" + id)["client"]) }
	wkTypers["shclient"] = func(c *console, id, text string) {
		if text != "" {
			somaPatch("shifts", id, func(o obj) { o["client"] = text })
		}
	}
	wkActions["shset"] = func(c *console, arg string) {
		p := strings.SplitN(arg, ":", 3)
		if len(p) != 3 {
			return
		}
		d, _ := strconv.Atoi(p[2])
		somaPatch("shifts", p[0], func(o obj) {
			t := num(o[p[1]]) + float64(d)*60000
			if p[1] == "start" && num(o["end"]) > 0 {
				t = min(t, num(o["end"])-60000)
			}
			if p[1] == "end" {
				t = min(max(t, num(o["start"])+60000), nowMs())
			}
			o[p[1]] = t
		})
		c.somaUI.keep = true
	}
	wkActions["shdel"] = func(c *console, id string) {
		o, at := somaRemove("shifts", id)
		if o != nil {
			c.offerUndo("Shift deleted", func() { somaRestore("shifts", o, at) })
		}
	}

	// Mind: reading and the notes.
	wkActions["read"] = func(c *console, m string) {
		n, _ := strconv.Atoi(m)
		somaAddReading(somaToday(), n)
	}
	wkActions["note"] = func(c *console, id string) { c.wkGo("mind", "note:"+id) }
	wkActions["notetick"] = func(c *console, i string) {
		n, _ := strconv.Atoi(i)
		somaNoteToggle(strings.TrimPrefix(c.somaUI.workSub, "note:"), n)
	}
	wkActions["notesearch"] = func(c *console, _ string) {
		if c.somaUI.noteQuery != "" {
			c.somaUI.noteQuery = ""
			return
		}
		c.wkType("notesearch:", "Find a note", "")
	}
	wkTypers["notesearch"] = func(c *console, _, text string) { c.somaUI.noteQuery = text }
	wkPrompts["newnote"] = func(string) (string, string) { return "The note's title", "" }
	wkTypers["newnote"] = func(c *console, _, text string) {
		if id := somaNewNote(text); id != "" {
			c.wkGo("mind", "note:"+id)
		}
	}
	wkPrompts["notetask"] = func(string) (string, string) { return "A task", "" }
	wkTypers["notetask"] = func(c *console, id, text string) {
		if text != "" {
			somaNoteAppend(id, "- [ ] "+text)
		}
	}
	wkPrompts["noteline"] = func(string) (string, string) { return "A line", "" }
	wkTypers["noteline"] = func(c *console, id, text string) { somaNoteAppend(id, text) }
}

func init() {
	wkSheets["readlog"] = func(k *wk, _ string) *wkSheet {
		sh := &wkSheet{title: "Log reading", sub: fmt.Sprintf("Today so far: %d minutes", somaReading(somaToday()))}
		for _, m := range []int{5, 10, 15, 20, 30, 45, 60} {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:read:%d", m), label: fmt.Sprintf("+%d minutes", m), icon: "plus"})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:read:-5", label: "Take 5 minutes off", icon: "minus"})
		return sh
	}
}

func init() {
	// The timesheet's week as a table in the notes (a spreadsheet's worth, readable anywhere).
	wkActions["excel"] = func(c *console, arg string) {
		off, _ := strconv.Atoi(arg)
		now := time.Now().In(displayZone)
		mon := weekStart(now).AddDate(0, 0, 7*off)
		entries := wkEntries()
		var b strings.Builder
		b.WriteString("| Project | Mon | Tue | Wed | Thu | Fri | Sat | Sun | Total |\n|---|---|---|---|---|---|---|---|---|\n")
		grand := 0.0
		for _, p := range wkProjects() {
			row, tot := "| "+p.Name+" |", 0.0
			for i := range 7 {
				d := mon.AddDate(0, 0, i)
				h := wkHours(entries, d, d.AddDate(0, 0, 1), now)[p.ID].Hours()
				tot += h
				row += fmt.Sprintf(" %.1f |", h)
			}
			if tot > 0 {
				b.WriteString(row + fmt.Sprintf(" %.1f |\n", tot))
				grand += tot
			}
		}
		fmt.Fprintf(&b, "\nTotal: %.1f h\n", grand)
		id := "Reports/Timesheet " + mon.Format("2006-01-02") + ".md"
		somaPutNote(id, "# Timesheet, week of "+mon.Format("2 Jan 2006")+"\n\n"+b.String())
		c.toastMsg("Timesheet saved to your notes")
	}
}
