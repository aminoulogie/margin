package main

import (
	"fmt"
	"image"
	"regexp"
	"sort"
	"strings"
	"time"

	"margin-init/ui"
)

// The Soma tab's Money: the month's income and spending by category against budgets, held
// against a spending plan (spend-templates.ts), and what clients owe for the work log, with
// standby (standby.ts: the gaps between a client's shifts in a day) at half the rate.

type somaEntry struct {
	ID, Date, Kind, Category, Currency string
	Amount                             float64
}

func somaLedger() []somaEntry {
	var out []somaEntry
	for _, o := range somaList("ledger") {
		out = append(out, somaEntry{ID: str(o["id"]), Date: str(o["date"]), Kind: str(o["kind"]), Category: str(o["category"]),
			Currency: str(o["currency"]), Amount: num(o["amount"])})
	}
	return out
}

// toBase is an amount in dinars, by the rates in Soma's settings.
func toBase(amount float64, currency string) float64 {
	if currency == "" || currency == "DZD" {
		return amount
	}
	rates, _ := jsonValue(soma.get("settings.moneyRates")).(map[string]any)
	if r := num(rates[currency]); r > 0 {
		return amount * r
	}
	return amount
}

type moneyMonth struct {
	Income, Spend, Saved float64
	ByCategory           map[string]float64
}

func somaMoneyMonth(month string) moneyMonth {
	m := moneyMonth{ByCategory: map[string]float64{}}
	for _, e := range somaLedger() {
		if !strings.HasPrefix(e.Date, month) {
			continue
		}
		v := toBase(max(e.Amount, -e.Amount), e.Currency)
		switch e.Kind {
		case "income":
			m.Income += v
		case "save":
			m.Saved += v
		default:
			m.Spend += v
			m.ByCategory[e.Category] += v
		}
	}
	return m
}

// --- spending plans (spend-templates.ts) --------------------------------------------------------

type spendTemplate struct {
	Name, By string
	Shares   map[string]float64 // needs, wants, save, give
}

var spendTemplates = []spendTemplate{
	{"50/30/20", "Elizabeth Warren", map[string]float64{"needs": 0.5, "wants": 0.3, "save": 0.2}},
	{"70/20/10", "a rule for steady earners", map[string]float64{"needs": 0.5, "wants": 0.2, "save": 0.2, "give": 0.1}},
	{"Pay yourself first", "George S. Clason, David Bach", map[string]float64{"needs": 0.5, "wants": 0.3, "save": 0.2}},
	{"The 60% solution", "Richard Jenkins", map[string]float64{"needs": 0.6, "wants": 0.1, "save": 0.3}},
	{"80/20", "save a fifth, don't track the rest", map[string]float64{"needs": 0.55, "wants": 0.25, "save": 0.2}},
	{"Freelancer's buffer", "after Paul Jarvis", map[string]float64{"needs": 0.5, "wants": 0.15, "save": 0.3, "give": 0.05}},
}

var (
	needsRe = regexp.MustCompile(`(?i)rent|housing|home|mortgage|grocer|food|supermarket|util|electric|water|gas|internet|phone|mobile|transport|fuel|car|bus|taxi|health|pharma|medic|doctor|insur|school|educat|debt|loan|bill`)
	giveRe  = regexp.MustCompile(`(?i)gift|charity|donat|zakat|sadaq|give`)
)

func shareOf(category string) string {
	switch {
	case giveRe.MatchString(category):
		return "give"
	case needsRe.MatchString(category):
		return "needs"
	}
	return "wants"
}

// --- the work log, with standby (worklog.ts, standby.ts) -----------------------------------------

type clientMonth struct {
	Worked, Standby time.Duration
	Days            int
	Rate            float64
}

func (c clientMonth) owed() float64 {
	return (c.Worked.Hours() + c.Standby.Hours()*0.5) * c.Rate
}

type somaShift struct {
	ID, Client string
	Start, End time.Time // End zero while running
	Worked     time.Duration
}

func somaShifts() []somaShift {
	now := time.Now()
	var out []somaShift
	for _, o := range somaList("shifts") {
		s := somaShift{ID: str(o["id"]), Client: str(o["client"]), Start: time.UnixMilli(int64(num(o["start"])))}
		end := now
		if e := num(o["end"]); e > 0 {
			s.End = time.UnixMilli(int64(e))
			end = s.End
		}
		if end.Sub(s.Start) > 16*time.Hour {
			end = s.Start.Add(16 * time.Hour)
		}
		brk := time.Duration(0)
		if bs, ok := o["breaks"].([]any); ok {
			for _, b := range bs {
				bo, _ := b.(obj)
				from := time.UnixMilli(int64(num(bo["start"])))
				to := end
				if e := num(bo["end"]); e > 0 {
					to = time.UnixMilli(int64(e))
				}
				if from.Before(s.Start) {
					from = s.Start
				}
				if to.After(end) {
					to = end
				}
				if to.After(from) {
					brk += to.Sub(from)
				}
			}
		}
		s.Worked = max(0, end.Sub(s.Start)-brk)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

func somaClientsMonth(month string) map[string]*clientMonth {
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
	type key struct{ client, day string }
	days := map[key][]somaShift{}
	for _, s := range somaShifts() {
		d := somaDate(s.Start)
		if strings.HasPrefix(d, month) {
			days[key{s.Client, d}] = append(days[key{s.Client, d}], s)
		}
	}
	out := map[string]*clientMonth{}
	now := time.Now()
	for k, ss := range days {
		first, last := ss[0].Start, time.Time{}
		worked := time.Duration(0)
		for _, s := range ss {
			if s.Start.Before(first) {
				first = s.Start
			}
			end := s.End
			if end.IsZero() {
				end = now
			}
			if end.After(last) {
				last = end
			}
			worked += s.Worked
		}
		c := out[k.client]
		if c == nil {
			c = &clientMonth{Rate: num(rates[k.client])}
			out[k.client] = c
		}
		c.Worked += worked
		c.Standby += max(0, last.Sub(first)-worked)
		c.Days++
	}
	return out
}

// somaDeleteShift removes a shift (a record removed is removed on the PC too).
func somaDeleteShift(id string) {
	soma.put("shifts#"+id, "")
	var order []string
	for _, s := range somaList("shifts") {
		if str(s["id"]) != id {
			order = append(order, str(s["id"]))
		}
	}
	somaPut("shifts@order", order)
}

// --- the view -------------------------------------------------------------------------------------

func money(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String() + " DA"
}

func (c *console) somaMoney(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	su := &c.somaUI
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	month := time.Now().In(displayZone).Format("2006-01")
	m := somaMoneyMonth(month)

	// The month.
	y = somaSection(img, mx, y, "This Month", time.Now().In(displayZone).Format("January"))
	cw := (w - 2*mx - 3*16) / 4
	for i, k := range []struct {
		label string
		v     float64
		col   uint32
	}{{"Income", m.Income, 0x30d158}, {"Spent", m.Spend, 0xff453a}, {"Saved", m.Saved, 0x0a84ff}, {"Net", m.Income - m.Spend - m.Saved, 0xffffff}} {
		r := image.Rect(mx+i*(cw+16), y, mx+i*(cw+16)+cw, y+130)
		smCard(img, r, smSurface)
		apText(img, f.caption, r.Min.X+24, r.Min.Y+44, apSecondary, k.label)
		apText(img, f.headline, r.Min.X+24, r.Min.Y+96, rgb(k.col), clip(f.headline, money(k.v), cw-40))
	}
	y += 160

	// Spending by category, with its share and the budget.
	budgets, _ := jsonValue(soma.get("settings.categoryBudgets")).(map[string]any)
	type cat struct {
		name  string
		spent float64
	}
	var cats []cat
	for n, v := range m.ByCategory {
		cats = append(cats, cat{n, v})
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].spent > cats[j].spent })
	y = somaSection(img, mx, y, "Spending", "by category")
	if len(cats) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "Nothing spent this month yet.")
		y += 70
	}
	for _, ct := range cats {
		r := image.Rect(mx, y, w-mx, y+100)
		smCard(img, r, smSurface)
		pct := 0.0
		if m.Spend > 0 {
			pct = ct.spent / m.Spend * 100
		}
		apText(img, f.headline, r.Min.X+30, r.Min.Y+44, apLabel, clip(f.headline, ct.name, 400))
		apText(img, f.caption, r.Min.X+30, r.Min.Y+82, apSecondary, fmt.Sprintf("%.0f%% of spending  ·  %s", pct, shareOf(ct.name)))
		label := money(ct.spent)
		b := num(budgets[ct.name])
		if b > 0 {
			label += " / " + money(b)
		}
		apText(img, f.callout, r.Max.X-30-ui.TextWidth(f.callout, label), r.Min.Y+44, apLabel, label)
		if b > 0 {
			bar := image.Rect(r.Max.X-330, r.Min.Y+66, r.Max.X-30, r.Min.Y+76)
			ui.RoundRect(img, bar, 5, apCard2)
			col := rgb(0x30d158)
			if ct.spent > b {
				col = apRed
			} else if ct.spent > 0.8*b {
				col = rgb(0xff9f0a)
			}
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(8, int(float64(bar.Dx())*min(ct.spent/b, 1))), bar.Max.Y), 5, col)
		}
		y += 112
	}

	// The spending plan: tap to try the next.
	t := spendTemplates[su.plan%len(spendTemplates)]
	y += 10
	y = somaSection(img, mx, y, "Spending Plan", t.Name+"  ·  "+t.By)
	base := m.Income
	if base <= 0 {
		base = m.Spend + m.Saved
	}
	actual := map[string]float64{"save": m.Saved}
	for n, v := range m.ByCategory {
		actual[shareOf(n)] += v
	}
	rows := 0
	for _, sh := range []string{"needs", "wants", "save", "give"} {
		if t.Shares[sh] != 0 || actual[sh] != 0 {
			rows++
		}
	}
	pr := image.Rect(mx, y, w-mx, y+40+rows*80)
	smCard(img, pr, smSurface)
	ry := pr.Min.Y + 60
	for _, sh := range []string{"needs", "wants", "save", "give"} {
		target := t.Shares[sh]
		if target == 0 && actual[sh] == 0 {
			continue
		}
		got := 0.0
		if base > 0 {
			got = actual[sh] / base
		}
		apText(img, f.headline, pr.Min.X+30, ry, apLabel, strings.ToUpper(sh[:1])+sh[1:])
		bar := image.Rect(pr.Min.X+200, ry-24, pr.Max.X-260, ry-4)
		ui.RoundRect(img, bar, 10, apCard2)
		col := rgb(0x30d158)
		if (sh == "save" && got < target) || (sh != "save" && got > target+0.02) {
			col = rgb(0xff9f0a)
		}
		ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(10, int(float64(bar.Dx())*min(got, 1))), bar.Max.Y), 10, col)
		tx := bar.Min.X + int(float64(bar.Dx())*target)
		ui.Fill(img, image.Rect(tx-2, bar.Min.Y-8, tx+2, bar.Max.Y+8), apLabel)
		apText(img, f.callout, pr.Max.X-230, ry, apSecondary, fmt.Sprintf("%.0f%% / %.0f%%", got*100, target*100))
		ry += 80
	}
	btn("so:plan", pr)
	y = drawParagraphs(img, f.caption, "Your month against the plan (the white line). Tap it for another plan; Soma on the PC can set your budgets from one.", mx, pr.Max.Y+40, w-2*mx, 36, pr.Max.Y+200, apSecondary)

	// Clients: hours, standby, owed.
	clients := somaClientsMonth(month)
	var names []string
	total := 0.0
	for n, cm := range clients {
		names = append(names, n)
		total += cm.owed()
	}
	sort.Strings(names)
	y += 20
	y = somaSection(img, mx, y, "Clients", "owed this month: "+money(total))
	if len(names) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No shifts this month: log them in Soma's Work Log on the PC.")
		y += 70
	}
	for _, n := range names {
		cm := clients[n]
		r := image.Rect(mx, y, w-mx, y+110)
		smCard(img, r, smSurface)
		apText(img, f.headline, r.Min.X+30, r.Min.Y+46, apLabel, n)
		rate := "no rate set"
		if cm.Rate > 0 {
			rate = money(cm.Rate) + "/h"
		}
		apText(img, f.caption, r.Min.X+30, r.Min.Y+86, apSecondary, fmt.Sprintf("%.1f h worked  ·  %.1f h standby (half)  ·  %d day%s  ·  %s",
			cm.Worked.Hours(), cm.Standby.Hours(), cm.Days, map[bool]string{true: "", false: "s"}[cm.Days == 1], rate))
		o := money(cm.owed())
		apText(img, f.headline, r.Max.X-30-ui.TextWidth(f.headline, o), r.Min.Y+46, rgb(0x30d158), o)
		y += 122
	}

	// The month's shifts: deleting one asks twice.
	shifts := somaShifts()
	y += 20
	y = somaSection(img, mx, y, "Shifts", "tap × to delete (asks twice)")
	n := 0
	for _, s := range shifts {
		if !strings.HasPrefix(somaDate(s.Start), month) {
			continue
		}
		if n++; n > 12 {
			break
		}
		r := image.Rect(mx, y, w-mx, y+90)
		smCard(img, r, smSurface)
		end := "running"
		if !s.End.IsZero() {
			end = s.End.In(displayZone).Format("15:04")
		}
		apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, fmt.Sprintf("%s  %s–%s  %s", s.Start.In(displayZone).Format("Mon 2"), s.Start.In(displayZone).Format("15:04"), end, s.Client))
		apText(img, f.callout, r.Max.X-260, r.Min.Y+56, apSecondary, fmt.Sprintf("%.1f h", s.Worked.Hours()))
		xr := image.Rect(r.Max.X-120, r.Min.Y, r.Max.X, r.Max.Y)
		switch {
		case su.delShift == s.ID && su.delStep == 2:
			ui.RoundRect(img, image.Rect(r.Max.X-460, r.Min.Y+10, r.Max.X-10, r.Max.Y-10), 16, apRed)
			apTextCenter(img, f.headline, r.Max.X-235, (r.Min.Y+r.Max.Y)/2, rgb(0xffffff), "Sure? Tap to delete")
			xr = image.Rect(r.Max.X-460, r.Min.Y, r.Max.X, r.Max.Y)
		case su.delShift == s.ID:
			ui.RoundRect(img, image.Rect(r.Max.X-360, r.Min.Y+10, r.Max.X-10, r.Max.Y-10), 16, rgb(0xff9f0a))
			apTextCenter(img, f.headline, r.Max.X-185, (r.Min.Y+r.Max.Y)/2, rgb(0x000000), "Delete this shift?")
			xr = image.Rect(r.Max.X-360, r.Min.Y, r.Max.X, r.Max.Y)
		default:
			apTextCenter(img, f.title, (xr.Min.X+xr.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apSecondary, "×")
		}
		btn("so:delshift:"+s.ID, xr)
		y += 100
	}
	return y
}

// somaDelShiftTap: the first tap asks, the second asks again, the third deletes. Caller
// holds drawMu.
func (c *console) somaDelShiftTap(id string) {
	su := &c.somaUI
	if su.delShift != id {
		su.delShift, su.delStep = id, 1
		return
	}
	if su.delStep == 1 {
		su.delStep = 2
		return
	}
	somaDeleteShift(id)
	su.delShift, su.delStep = "", 0
	c.toastMsg("Shift deleted")
}
