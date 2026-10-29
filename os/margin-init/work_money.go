package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"margin-init/ui"
)

// The Work tab's Money and Reports (MoneyPage.tsx, ReportsPage.tsx): the month's income,
// spending and what was kept, cash flow over six months, invoices (the billable time of each
// client's month, at the projects' rates), where the money went and the accounts; and a
// client's monthly report as a page.

// wkMonthMoney is a month's income and spending in the base currency.
func wkMonthMoney(month time.Time) (income, spend float64, byCat map[string]float64) {
	m := somaMoneyMonth(month.Format("2006-01"))
	return m.Income, m.Spend, m.ByCategory
}

type wkInvoice struct {
	No, Client, What, Status string
	Amount                   float64
	col                      color.RGBA
	month                    time.Time
}

// wkInvoices is a bill per client per month of billable time, newest first: this month's a
// draft, then due 30 days after the month ends, overdue after that, paid when an income from
// that client is logged after the month.
func wkInvoices(projects []wkProject, entries []wkEntry, now time.Time) []wkInvoice {
	byID := map[string]wkProject{}
	for _, p := range projects {
		byID[p.ID] = p
	}
	type key struct {
		client string
		month  time.Time
	}
	amt := map[key]float64{}
	what := map[key]map[string]float64{}
	for _, e := range entries {
		p := byID[e.ProjectID]
		if !e.Billable || p.Rate <= 0 || p.Client == "" {
			continue
		}
		s := e.Start.In(displayZone)
		k := key{p.Client, time.Date(s.Year(), s.Month(), 1, 0, 0, 0, 0, displayZone)}
		amt[k] += e.dur(now).Hours() * p.Rate
		if what[k] == nil {
			what[k] = map[string]float64{}
		}
		what[k][shortName(p.Name)] += e.dur(now).Hours()
	}
	paid := map[string]bool{}
	for _, l := range somaLedger() {
		if l.Kind == "income" {
			paid[strings.ToLower(l.Category)+"|"+l.Date[:min(7, len(l.Date))]] = true
		}
	}
	var keys []key
	for k := range amt {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if !keys[i].month.Equal(keys[j].month) {
			return keys[i].month.Before(keys[j].month)
		}
		return keys[i].client < keys[j].client
	})
	var out []wkInvoice
	for i, k := range keys {
		top, best := "", 0.0
		for n, h := range what[k] {
			if h > best {
				top, best = n, h
			}
		}
		inv := wkInvoice{No: fmt.Sprintf("INV-%04d", i+1), Client: k.client, What: top + ", " + k.month.Format("January"), Amount: amt[k], month: k.month}
		due := k.month.AddDate(0, 2, 0) // 30 days after the month's end
		days := int(math.Ceil(due.Sub(now).Hours() / 24))
		next := k.month.AddDate(0, 1, 0).Format("2006-01")
		switch {
		case k.month.Year() == now.Year() && k.month.Month() == now.Month():
			inv.Status, inv.col = "Draft", wkMuted
		case paid[strings.ToLower(k.client)+"|"+next] || paid[strings.ToLower(k.client)+"|"+due.Format("2006-01")]:
			inv.Status, inv.col = "Paid", wkGreen
		case days >= 0:
			inv.Status, inv.col = fmt.Sprintf("Due in %d d", days), wkAmber
		default:
			inv.Status, inv.col = fmt.Sprintf("Overdue %d d", -days), wkRed
		}
		out = append(out, inv)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type wkAccount struct {
	Name, Kind, Currency, Sub string
	Balance                   float64
	icon                      string
	col                       color.RGBA
}

// wkAccounts is Soma's money accounts with what the ledger moved since each was opened, and
// the first savings goal.
func wkAccounts() []wkAccount {
	accs, _ := jsonValue(soma.get("settings.moneyAccounts")).([]any)
	ledger := somaLedgerFull()
	var out []wkAccount
	for _, a := range accs {
		o, ok := a.(map[string]any)
		if !ok {
			continue
		}
		ac := wkAccount{Name: str(o["name"]), Kind: str(o["kind"]), Currency: str(o["currency"]), Balance: num(o["opening"])}
		for _, l := range ledger {
			if l.Account == str(o["id"]) && l.Date >= str(o["openingDate"]) {
				if l.Kind == "income" {
					ac.Balance += l.Amount
				} else {
					ac.Balance -= l.Amount
				}
			}
		}
		ac.icon, ac.col = "banknote", wkGreen
		switch ac.Kind {
		case "bank":
			ac.icon, ac.col, ac.Sub = "landmark", wkBlue, "Bank"
		case "card":
			ac.icon, ac.col, ac.Sub = "wallet", wkPurple, "Card"
		case "savings":
			ac.icon, ac.col, ac.Sub = "piggy-bank", wkLime, "Savings"
		default:
			ac.Sub = "Cash"
		}
		out = append(out, ac)
	}
	goals, _ := jsonValue(soma.get("settings.savingsGoals")).([]any)
	for _, g := range goals {
		o, ok := g.(map[string]any)
		if !ok {
			continue
		}
		saved := 0.0
		for _, l := range ledger {
			if l.Kind == "save" && l.Goal == str(o["id"]) {
				saved += l.Amount
			}
		}
		pct := 0.0
		if t := num(o["target"]); t > 0 {
			pct = saved / t * 100
		}
		out = append(out, wkAccount{Name: "Savings goal", Sub: fmt.Sprintf("%s · %.0f%%", str(o["name"]), pct), Balance: saved, icon: "piggy-bank", col: wkLime})
		break
	}
	return out
}

type ledgerLine struct {
	Date, Kind, Account, Goal string
	Amount                    float64
}

func somaLedgerFull() []ledgerLine {
	var out []ledgerLine
	for _, o := range somaList("ledger") {
		out = append(out, ledgerLine{Date: str(o["date"]), Kind: str(o["kind"]), Account: str(o["accountId"]), Goal: str(o["goalId"]),
			Amount: toBase(math.Abs(num(o["amount"])), str(o["currency"]))})
	}
	return out
}

func (k *wk) money() int {
	now := time.Now().In(displayZone)
	projects := wkProjects()
	entries := wkEntries()
	invs := wkInvoices(projects, entries, now)
	k.header([]string{wkUser() + " · Freelance", "Money"}, "Money", "Income, invoices and spending for your work", color.RGBA{})
	x := k.primary("wk:add", "plus", "Add", wkX1)
	k.square("wk:export", "download", x-13)
	k.tabs("wk:tab", []string{"Overview", "Invoices", "Spending", "Goals"}, []string{"", fmt.Sprint(len(invs))}, k.sel("tab", 0))
	rng := k.sel("chip", 0)
	k.chips("wk:chip", 308, []string{now.Format("January"), "Quarter", "Year"}, nil, rng)
	if rates, _ := jsonValue(soma.get("settings.moneyRates")).(map[string]any); len(rates) > 0 {
		for _, c := range []string{"EUR", "USD"} {
			if r := num(rates[c]); r > 0 {
				sym := map[string]string{"EUR": "€", "USD": "$"}[c]
				k.outline("wk:rates", "arrow-left-right", fmt.Sprintf("%s1 = %.0f DA", sym, r), wkX1, 308, 53)
				break
			}
		}
	}

	if t := k.sel("tab", 0); t > 0 {
		return k.moneyTab(t)
	}

	// The figures: this month (or quarter, or year) against the one before.
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	months := []int{1, 3, 12}[rng]
	from := month.AddDate(0, -(months - 1), 0)
	sum := func(from time.Time, n int) (in, out float64, cats map[string]float64) {
		cats = map[string]float64{}
		for i := range n {
			a, b, c := wkMonthMoney(from.AddDate(0, i, 0))
			in += a
			out += b
			for k, v := range c {
				cats[k] += v
			}
		}
		return
	}
	in, out, cats := sum(from, months)
	pin, pout, _ := sum(from.AddDate(0, -months, 0), months)
	label := now.Format("Jan")
	prev := month.AddDate(0, -1, 0).Format("Jan")
	if months > 1 {
		label, prev = []string{"", "", "", "Quarter"}[min(months, 3)], "last"
		if months == 12 {
			label = "Year"
		}
	}
	var sIn, sOut, sKept []float64
	for i := 5; i >= 0; i-- {
		a, b, _ := wkMonthMoney(month.AddDate(0, -i, 0))
		sIn, sOut, sKept = append(sIn, a), append(sOut, b), append(sKept, a-b)
	}
	cw := (wkX1 - wkX0 - 40) / 3
	k.kpi(image.Rect(wkX0, 386, wkX0+cw, 562), "arrow-down-left", "Income · "+label, wkMoney(in), "", pctChange(pin, in), in >= pin, "vs "+prev, sIn, wkLime, wkFG)
	k.kpi(image.Rect(wkX0+cw+20, 386, wkX0+2*cw+20, 562), "arrow-up-right", "Spending · "+label, wkMoney(out), "", pctChange(pout, out), out <= pout, "vs "+prev, sOut, wkRed, wkFG)
	kept := 0.0
	if in > 0 {
		kept = (in - out) / in * 100
	}
	k.kpi(image.Rect(wkX0+2*cw+40, 386, wkX1, 562), "piggy-bank", "Saved", wkMoney(in-out), "", pctChange(pin-pout, in-out), in-out >= pin-pout, fmt.Sprintf("%.0f%% kept", kept), sKept, wkGreen, wkFG)

	// Cash flow, six months.
	cf := image.Rect(wkX0, 588, wkX1, 928)
	k.card(cf)
	k.cardHead(wkX0+28, 630, "chart-column", "Cash flow", "· last 6 months")
	lx := 1136 - ui.TextWidth(wkFont("regular", 20), "Spending")
	k.text(wkFont("regular", 20), lx, 630, wkText2, "Spending")
	ui.RoundRect(k.img, k.R(lx-26, 615, lx-10, 631), 4, wkRed)
	lx -= 26 + 26 + ui.TextWidth(wkFont("regular", 20), "Income")
	k.text(wkFont("regular", 20), lx, 630, wkText2, "Income")
	ui.RoundRect(k.img, k.R(lx-26, 615, lx-10, 631), 4, wkLime)
	hi := 1.0
	for i := range sIn {
		hi = max(hi, sIn[i], sOut[i])
	}
	step := niceStep(hi / 4)
	top := step * 4
	base, ty := 921, 686
	for i := 0; i <= 4; i++ {
		y := base - i*(base-ty)/4
		k.text(wkFont("regular", 17), 197, y+6, rgb(0x4a4c55), shortMoney(step*float64(i)))
		ui.Fill(k.img, k.R(240, y, 1136, y+1), rgb(0x1a1b21))
	}
	for i := range 6 {
		gx := 277 + i*149
		cur := i == 5
		ic, oc := blend(wkCard, wkLime, 0.45), blend(wkCard, wkRed, 0.45)
		if cur {
			ic, oc = wkLime, wkRed
		}
		ih := int(sIn[i] / top * float64(base-ty))
		oh := int(sOut[i] / top * float64(base-ty))
		if ih > 0 {
			ui.RoundRect(k.img, k.R(gx, base-ih, gx+35, base), 6, ic)
		}
		if oh > 0 {
			ui.RoundRect(k.img, k.R(gx+42, base-oh, gx+77, base), 6, oc)
		}
	}

	// Invoices.
	open := 0.0
	for _, v := range invs {
		if v.Status != "Paid" {
			open += v.Amount
		}
	}
	shown := invs[:min(len(invs), 3)]
	ic := image.Rect(wkX0, 953, wkX1, 953+148+94*max(len(shown), 1))
	k.card(ic)
	e := k.cardHead(wkX0+28, 1005, "receipt", "Invoices", "")
	k.text(wkFont("regular", 20), e+14, 1005, wkFaint, wkMoney(open)+" open")
	k.outline("wk:invoice", "plus", "New invoice", 1136, 974, 50)
	ui.Fill(k.img, k.R(wkX0+2, 1046, wkX1-2, 1048), wkLine)
	hf := wkFont("semi", 17)
	k.text(hf, 187, 1079, wkFaint, "INVOICE")
	k.text(hf, 478, 1079, wkFaint, "CLIENT")
	k.textR(hf, 903, 1079, wkFaint, "AMOUNT")
	k.textR(hf, 1136, 1079, wkFaint, "STATUS")
	y := 1100
	for _, v := range shown {
		ui.Fill(k.img, k.R(wkX0+2, y, wkX1-2, y+2), wkLine)
		k.text(wkFont("bold", 22), 187, y+42, wkFG, v.No)
		k.text(wkFont("regular", 19), 187, y+71, wkFaint, clip(wkFont("regular", 19), v.What, 270))
		k.text(wkFont("regular", 22), 478, y+56, wkFG, clip(wkFont("regular", 22), v.Client, 300))
		k.textR(wkFont("bold", 22), 903, y+56, wkFG, wkMoney(v.Amount))
		k.badge(1136, y+48, v.Status, v.col)
		y += 94
	}
	if len(shown) == 0 {
		k.text(wkFont("regular", 20), 187, y+54, wkMuted, "No billable time yet: set a rate on a project.")
	}

	// Where it went, and the accounts.
	y = ic.Max.Y + 25
	k.card(image.Rect(wkX0, y, 686, y+358))
	k.cardHead(wkX0+28, y+43, "chart-pie", "Where it went", "")
	type cat struct {
		name string
		v    float64
	}
	var cs []cat
	for n, v := range cats {
		cs = append(cs, cat{n, v})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].v > cs[j].v })
	if len(cs) > 6 {
		other := 0.0
		for _, c := range cs[5:] {
			other += c.v
		}
		cs = append(cs[:5], cat{"Other", other})
	}
	catCols := []color.RGBA{wkBlue, wkPurple, wkGreen, wkAmber, rgb(0xf0566a), rgb(0x6b6e78)}
	var vals []float64
	for _, c := range cs {
		vals = append(vals, c.v)
	}
	k.donut(283, y+177, 86, 24, vals, catCols)
	k.textC(wkFont("bold", 30), 283, y+185, wkFG, wkMoney(out))
	k.textC(wkFont("regular", 18), 283, y+207, wkMuted, now.Format("January"))
	ly := y + 92
	for i, c := range cs {
		ui.RoundRect(k.img, k.R(401, ly-14, 417, ly+2), 4, catCols[i%len(catCols)])
		k.text(wkFont("regular", 20), 427, ly, wkText2, clip(wkFont("regular", 20), c.name, 170))
		k.textR(wkFont("semi", 20), 659, ly, wkFG, wkMoney(c.v))
		ly += 37
	}
	if len(cs) == 0 {
		k.text(wkFont("regular", 20), 401, y+130, wkMuted, "Nothing spent yet.")
	}
	accs := wkAccounts()
	k.card(image.Rect(706, y, wkX1, y+358))
	k.cardHead(734, y+43, "landmark", "Accounts", "")
	ui.Fill(k.img, k.R(708, y+73, wkX1-2, y+75), wkLine)
	ry := y + 75
	for i, a := range accs {
		if i == 3 {
			break
		}
		cy := ry + 46
		ui.RoundRect(k.img, k.R(733, cy-26, 785, cy+26), 12, blend(wkCard, a.col, 0.16))
		lucideIcon(k.img, a.icon, 759, k.Y(cy), 26, a.col)
		k.text(wkFont("semi", 22), 805, cy-4, wkFG, clip(wkFont("semi", 22), a.Name, 190))
		k.text(wkFont("regular", 19), 805, cy+24, wkFaint, a.Sub)
		v := wkMoney(a.Balance)
		if a.Currency != "" {
			v = fmtThousands(int(a.Balance)) + " " + map[string]string{"DZD": "DA", "EUR": "€", "USD": "$"}[a.Currency]
		}
		k.textR(wkFont("bold", 22), 1136, cy+8, wkFG, v)
		ry += 95
		if i < min(len(accs), 3)-1 {
			ui.Fill(k.img, k.R(708, ry, wkX1-2, ry+2), wkLine)
		}
	}
	if len(accs) == 0 {
		k.text(wkFont("regular", 20), 734, ry+54, wkMuted, "No accounts set up in Soma.")
	}
	return y + 358
}

// niceStep rounds a chart's step up to 1, 2 or 5 times a power of ten.
func niceStep(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return 10 * p
}

// shortMoney is an axis label: £4k.
func shortMoney(v float64) string {
	s := wkMoney(0)
	sym, suffix := "", ""
	if strings.HasPrefix(s, "0") {
		suffix = strings.TrimPrefix(s, "0")
	} else {
		sym = strings.TrimSuffix(s, "0")
	}
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%s%gM%s", sym, v/1e6, suffix)
	case v >= 1000:
		return fmt.Sprintf("%s%gk%s", sym, v/1000, suffix)
	}
	return fmt.Sprintf("%s%g%s", sym, v, suffix)
}

// --- reports ----------------------------------------------------------------------------------

// wkReportClients is the clients, the one with the most hours this month first.
func wkReportClients() []string {
	now := time.Now().In(displayZone)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	mh := wkHours(wkEntries(), month, month.AddDate(0, 1, 0), now)
	hours := map[string]float64{}
	for _, p := range wkProjects() {
		if p.Client != "" {
			hours[p.Client] += mh[p.ID].Hours()
		}
	}
	var clients []string
	for c := range hours {
		clients = append(clients, c)
	}
	sort.SliceStable(clients, func(i, j int) bool {
		if hours[clients[i]] != hours[clients[j]] {
			return hours[clients[i]] > hours[clients[j]]
		}
		return clients[i] < clients[j]
	})
	return clients
}

// spaced is caps with a little air between the letters, as the report's labels have.
func spaced(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r != ' ' {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// wkNoteOf is what the work for a client is, from its first project's note.
func wkNoteOf(projects []wkProject, client string) string {
	for _, p := range projects {
		if p.Client == client && p.Note != "" {
			return p.Note
		}
	}
	return ""
}
