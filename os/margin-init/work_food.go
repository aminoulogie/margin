package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"margin-init/ui"
)

// Nutrition, as Soma's Fuel tab has it (NutritionView.tsx and its cards): four pages, any day.
// Dashboard: the day's totals against its targets (a session's calories added to the target,
// as Soma does unless that's switched off), the meal timeline, what's logged and waiting to
// be eaten, water (with the water in food), hunger, and adding food from Soma's library, your
// own foods and what you eat most. Log: meal by meal, every item editable, and the day's
// micronutrients. Week: the last seven days. Weight: weigh in, and the trend.
//
// Food logged for today waits, grey, until it's marked eaten, which stamps when (addFood,
// confirmPlanned); eaten and timed, it's locked, as on the phone. Other days log straight in.

// --- the food library (food-library.json, fetched from the Soma repo once) ----------------------

var foodLibraryURL = "https://raw.githubusercontent.com/aminoulogie/kite-bay-otter-topaz/main/src/lib/food-library.json"

var foodLibrary struct {
	sync.Mutex
	foods []obj
	tried time.Time
}

func loadFoodLibrary() []obj {
	foodLibrary.Lock()
	defer foodLibrary.Unlock()
	if foodLibrary.foods != nil {
		return foodLibrary.foods
	}
	if b, err := os.ReadFile(somaDir + "/food-library.json"); err == nil {
		var f []obj
		if json.Unmarshal(b, &f) == nil && len(f) > 0 {
			foodLibrary.foods = f
			return f
		}
	}
	if time.Since(foodLibrary.tried) > time.Hour && soma.linked() {
		foodLibrary.tried = time.Now()
		go func() {
			resp, err := webClient().Get(foodLibraryURL)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			var f []obj
			if err != nil || resp.StatusCode != 200 || json.Unmarshal(b, &f) != nil || len(f) == 0 {
				return
			}
			os.MkdirAll(somaDir, 0o755)
			os.WriteFile(somaDir+"/food-library.json", b, 0o644)
			foodLibrary.Lock()
			foodLibrary.foods = f
			foodLibrary.Unlock()
		}()
	}
	return nil
}

// foodsAll is composeLibrary: Soma's foods, your own over them by name.
func foodsAll() []obj {
	by := map[string]obj{}
	var order []string
	add := func(f obj) {
		k := strings.ToLower(strings.TrimSpace(str(f["name"])))
		if k == "" {
			return
		}
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		by[k] = f
	}
	for _, f := range loadFoodLibrary() {
		add(f)
	}
	custom, _ := jsonValue(soma.get("customFoods")).([]any)
	for _, c := range custom {
		if f, ok := c.(map[string]any); ok {
			add(f)
		}
	}
	out := make([]obj, 0, len(order))
	for _, k := range order {
		out = append(out, by[k])
	}
	return out
}

// foldText is normalizeSearch: accents off, lower case.
func foldText(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimSpace(b.String())
}

// searchFoods is Soma's: exact, starts with, contains, then the group; most used first.
func searchFoods(q string, limit int) []obj {
	q = foldText(q)
	type hit struct {
		f    obj
		rank int
	}
	var hits []hit
	for _, f := range foodsAll() {
		name, group := foldText(str(f["name"])), foldText(str(f["group"]))
		rank := -1
		switch {
		case q == "":
			rank = 1
		case name == q:
			rank = 0
		case strings.HasPrefix(name, q):
			rank = 1
		case strings.Contains(name, q):
			rank = 2
		case strings.Contains(group, q):
			rank = 3
		}
		if rank >= 0 {
			hits = append(hits, hit{f, rank})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if ua, ub := num(a.f["usageCount"]), num(b.f["usageCount"]); ua != ub {
			return ua > ub
		}
		return str(a.f["name"]) < str(b.f["name"])
	})
	var out []obj
	for i, h := range hits {
		if i == limit {
			break
		}
		out = append(out, h.f)
	}
	return out
}

// recentFoods is food-recents.ts: what you eat, recent days counting more (half-life 14 days).
func recentFoods(today string, limit int) []obj {
	type rec struct {
		item  obj
		score float64
		last  string
	}
	by := map[string]*rec{}
	t, _ := time.Parse("2006-01-02", today)
	for _, k := range soma.keysWith("nutrition/") {
		d := strings.TrimPrefix(k, "nutrition/")
		dt, err := time.Parse("2006-01-02", d)
		if err != nil || d > today {
			continue
		}
		age := t.Sub(dt).Hours() / 24
		if age > 45 {
			continue
		}
		w := math.Pow(2, -age/14)
		items, _ := somaObj(k)["items"].([]any)
		for _, it := range items {
			f, _ := it.(obj)
			key := strings.ToLower(strings.TrimSpace(str(f["name"])))
			if key == "" {
				continue
			}
			r := by[key]
			if r == nil {
				r = &rec{f, 0, d}
				by[key] = r
			}
			r.score += w
			if d >= r.last {
				r.last, r.item = d, f
			}
		}
	}
	var list []*rec
	for _, r := range by {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].last > list[j].last
	})
	var out []obj
	for i, r := range list {
		if i == limit {
			break
		}
		out = append(out, r.item)
	}
	return out
}

// --- portions (PortionSheet.tsx) ------------------------------------------------------------------

var microKeys = []string{"sodium", "potassium", "calcium", "iron", "magnesium", "zinc", "vitA", "vitC", "vitD", "vitE", "vitB6", "vitB12", "folate"}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// basisOf is a food per 100 g (or ml).
func basisOf(f obj) obj {
	if b, ok := f["per100"].(obj); ok {
		return b
	}
	g := num(f["serving"])
	if g <= 0 {
		g = 100
	}
	per := func(k string) float64 { return num(f[k]) / g * 100 }
	return obj{"cals": per("cals"), "p": per("p"), "c": per("c"), "f": per("f"), "fiber": per("fiber")}
}

// scaleFood is scaleTo: the food at amount, every figure from its per-100 basis.
func scaleFood(f obj, amount float64, meal string) obj {
	b := basisOf(f)
	k := amount / 100
	out := obj{}
	for key, v := range f {
		out[key] = v
	}
	unit := str(f["unit"])
	if unit == "" {
		unit = "g"
	}
	out["per100"], out["serving"], out["unit"], out["meal"] = b, amount, unit, meal
	out["cals"] = math.Round(num(b["cals"]) * k)
	out["p"], out["c"], out["f"], out["fiber"] = round1(num(b["p"])*k), round1(num(b["c"])*k), round1(num(b["f"])*k), round1(num(b["fiber"])*k)
	factor := 1.0
	if s := num(f["serving"]); s > 0 {
		factor = amount / s
	}
	for _, m := range microKeys {
		if v := num(f[m]); v != 0 {
			out[m] = round1(v * factor)
		}
	}
	if unit == "ml" && num(f["waterPct"]) > 0 {
		out["waterMl"] = math.Round(amount * num(f["waterPct"]) / 100)
	} else {
		delete(out, "waterMl")
	}
	delete(out, "eatenAt")
	delete(out, "loggedAt")
	delete(out, "usageCount")
	return out
}

// --- the day ----------------------------------------------------------------------------------------

var defaultGoals = obj{"cals": 2400.0, "protein": 160.0, "carbs": 260.0, "fat": 70.0, "water": 3500.0, "fiber": 35.0,
	"calcium": 1000.0, "iron": 18.0, "magnesium": 400.0, "potassium": 3500.0, "sodium": 2300.0, "zinc": 11.0}

// resolvedGoals is resolveGoals: the defaults with Soma's own targets over them.
func resolvedGoals() obj {
	g := obj{}
	for k, v := range defaultGoals {
		g[k] = v
	}
	if c, ok := jsonValue(soma.get("settings.customGoals")).(map[string]any); ok {
		for k, v := range c {
			if num(v) > 0 {
				g[k] = num(v)
			}
		}
	}
	return g
}

// foodDay is a day's record, made (with today's targets) if there isn't one: ensureDay.
func foodDay(date string) obj {
	if o := somaObj("nutrition/" + date); o != nil {
		if _, ok := o["goals"].(obj); !ok {
			o["goals"] = resolvedGoals()
		}
		return o
	}
	return obj{"goals": resolvedGoals(), "water": 0, "items": []any{}}
}

func dayGoal(day obj, key string) float64 {
	g, _ := day["goals"].(obj)
	if v := num(g[key]); v > 0 {
		return v
	}
	return num(resolvedGoals()[key])
}

func foodList(day obj, list string) []obj {
	arr, _ := day[list].([]any)
	var out []obj
	for _, a := range arr {
		if f, ok := a.(obj); ok {
			out = append(out, f)
		}
	}
	return out
}

type macros struct{ cals, p, c, f, fiber float64 }

func totalsOf(items []obj) macros {
	var m macros
	for _, i := range items {
		m.cals += num(i["cals"])
		m.p += num(i["p"])
		m.c += num(i["c"])
		m.f += num(i["f"])
		m.fiber += num(i["fiber"])
	}
	return m
}

// dayWaterMl is totalWaterMl: what was drunk plus the water in the food.
func dayWaterMl(day obj) float64 {
	w := num(day["water"])
	for _, i := range foodList(day, "items") {
		w += num(i["waterMl"])
	}
	return math.Round(w)
}

// trainingBurn is sessionBurn's net: the calories a day's session cost above resting.
func trainingBurn(date string) float64 {
	o := somaObj("history/" + date)
	if o == nil {
		return 0
	}
	kg := 78.0
	if w := bodyweightOn(date); w > 0 {
		kg = w
	}
	sets := math.Floor(num(o["totalSets"]))
	if sets <= 0 {
		return 0
	}
	clock := (&somaSession{Duration: str(o["durationFormatted"])}).minutes()
	implied := max(10, sets*3)
	minutes := implied
	if clock > 0 {
		minutes = min(clock, implied*1.5)
	}
	sum, n := 0.0, 0.0
	exs, _ := o["exercises"].([]any)
	for _, e := range exs {
		eo, _ := e.(obj)
		ss, _ := eo["sets"].([]any)
		for _, x := range ss {
			so, _ := x.(obj)
			if d, ok := so["done"].(bool); ok && !d {
				continue
			}
			f := num(so["failure"])
			if f <= 0 {
				f = 3
			}
			sum += min(f, 5)
			n++
		}
	}
	avg := 3.0
	if n > 0 {
		avg = sum / n
	}
	met := 3.5 + (min(max(avg, 1), 5)-1)/4*(6-3.5)
	gross := met * minutes * 3.5 / 200 * kg
	rest := minutes * 3.5 / 200 * kg
	return max(0, math.Round(gross-rest))
}

// bodyweightOn is the latest weighing on or before date.
func bodyweightOn(date string) float64 {
	best, at := 0.0, ""
	for _, k := range soma.keysWith("nutrition/") {
		d := strings.TrimPrefix(k, "nutrition/")
		if d <= date && d > at {
			if w := num(somaObj(k)["bodyWeight"]); w > 0 {
				best, at = w, d
			}
		}
	}
	return best
}

func eatsBack() bool {
	v, ok := jsonValue(soma.get("settings.eatBackTraining")).(bool)
	return !ok || v
}

type mealTime struct {
	Label, Time string
	Share       float64
}

// mealTimes are Soma's meals and when (settings.mealTimes, else DEFAULT_MEAL_TIMES).
func mealTimes() []mealTime {
	out := []mealTime{{"Breakfast", "08:30", 25}, {"Lunch", "12:30", 25}, {"Snack", "16:00", 15}, {"Dinner", "19:30", 25}, {"Evening", "21:30", 10}}
	if arr, ok := jsonValue(soma.get("settings.mealTimes")).([]any); ok && len(arr) > 0 {
		out = nil
		for _, a := range arr {
			o, _ := a.(map[string]any)
			out = append(out, mealTime{str(o["label"]), str(o["time"]), num(o["share"])})
		}
	}
	return out
}

// mealNow is the meal whose time is nearest before now (the first, before any).
func mealNow() string {
	mt := mealTimes()
	now := time.Now().In(displayZone)
	m := now.Hour()*60 + now.Minute()
	best := mt[0].Label
	for _, x := range mt {
		var h, mm int
		fmt.Sscanf(x.Time, "%d:%d", &h, &mm)
		if h*60+mm-60 <= m {
			best = x.Label
		}
	}
	return best
}

// --- changing the day (store.ts) ----------------------------------------------------------------------

func patchFoodDay(date string, fn func(day obj)) {
	day := foodDay(date)
	fn(day)
	somaPut("nutrition/"+date, day)
}

// addFoodOn is addFood: today it waits to be marked eaten; another day, it's eaten.
func addFoodOn(date string, f obj) {
	patchFoodDay(date, func(day obj) {
		if date == somaToday() {
			f["loggedAt"] = nowMs()
			pl, _ := day["planned"].([]any)
			day["planned"] = append(pl, f)
		} else {
			it, _ := day["items"].([]any)
			day["items"] = append(it, f)
		}
	})
	// The food you reach for most: only your own foods carry the count.
	if custom, ok := jsonValue(soma.get("customFoods")).([]any); ok {
		name := strings.ToLower(strings.TrimSpace(str(f["name"])))
		for _, c := range custom {
			if o, ok := c.(map[string]any); ok && strings.ToLower(strings.TrimSpace(str(o["name"]))) == name {
				o["usageCount"] = num(o["usageCount"]) + 1
				somaPut("customFoods", custom)
				break
			}
		}
	}
}

// confirmPlanned moves planned food into the day, stamped with when if it's today.
func confirmPlanned(date string, i int) {
	patchFoodDay(date, func(day obj) {
		pl, _ := day["planned"].([]any)
		if i < 0 || i >= len(pl) {
			return
		}
		f, _ := pl[i].(obj)
		if date == somaToday() {
			f["eatenAt"] = nowMs()
		}
		it, _ := day["items"].([]any)
		day["items"] = append(it, f)
		day["planned"] = append(pl[:i:i], pl[i+1:]...)
	})
}

func confirmAll(date string) int {
	n := 0
	patchFoodDay(date, func(day obj) {
		pl, _ := day["planned"].([]any)
		it, _ := day["items"].([]any)
		for _, p := range pl {
			if f, ok := p.(obj); ok {
				if date == somaToday() {
					f["eatenAt"] = nowMs()
				}
				it = append(it, f)
				n++
			}
		}
		day["items"], day["planned"] = it, []any{}
	})
	return n
}

// unconfirmFood puts an item back to waiting (not once it's been eaten and timed).
func unconfirmFood(date string, i int) bool {
	ok := false
	patchFoodDay(date, func(day obj) {
		it, _ := day["items"].([]any)
		if i < 0 || i >= len(it) {
			return
		}
		f, _ := it[i].(obj)
		if num(f["eatenAt"]) > 0 {
			return
		}
		pl, _ := day["planned"].([]any)
		day["planned"] = append(pl, f)
		day["items"] = append(it[:i:i], it[i+1:]...)
		ok = true
	})
	return ok
}

func foodAt(date, list string, i int) obj {
	l := foodList(foodDay(date), list)
	if i < 0 || i >= len(l) {
		return nil
	}
	return l[i]
}

// setFoodAt replaces (nil: removes) an item of a day's list.
func setFoodAt(date, list string, i int, f obj) {
	patchFoodDay(date, func(day obj) {
		arr, _ := day[list].([]any)
		if i < 0 || i >= len(arr) {
			return
		}
		if f == nil {
			day[list] = append(arr[:i:i], arr[i+1:]...)
		} else {
			arr[i] = f
		}
	})
}

func insertFoodAt(date, list string, i int, f obj) {
	patchFoodDay(date, func(day obj) {
		arr, _ := day[list].([]any)
		i = min(max(i, 0), len(arr))
		day[list] = append(arr[:i:i], append([]any{f}, arr[i:]...)...)
	})
}

// addWaterOn is addWater: the day's water and its log entry.
func addWaterOn(date string, ml float64) {
	patchFoodDay(date, func(day obj) {
		before := num(day["water"])
		w := max(0, before+math.Round(ml))
		if w == before {
			return
		}
		day["water"] = w
		log, _ := day["waterLog"].([]any)
		day["waterLog"] = append(log, obj{"id": "w-" + somaNewID(), "at": nowMs(), "ml": w - before})
	})
}

type hungerEntry struct {
	Date, At, Note string
	Level          int
}

var hungerLabels = map[int]string{1: "Peckish", 2: "Hungry", 3: "Ravenous"}

func hungerAll() []obj {
	if arr, ok := jsonValue(soma.get("hunger")).([]any); ok {
		var out []obj
		for _, a := range arr {
			if o, ok := a.(map[string]any); ok {
				out = append(out, o)
			}
		}
		return out
	}
	return somaList("hunger")
}

func hungerOn(date string) []obj {
	var out []obj
	for _, h := range hungerAll() {
		if str(h["date"]) == date {
			out = append(out, h)
		}
	}
	return out
}

func putHunger(list []obj) {
	arr := make([]any, len(list))
	for i, o := range list {
		arr[i] = o
	}
	somaPut("hunger", arr)
}

// hungerPenalty is the day score's: none on a cut, some on maintenance, most on a bulk.
func hungerPenalty(entries []obj) float64 {
	phase, _ := jsonValue(soma.get("settings.phase")).(string)
	if phase == "cut" {
		return 0
	}
	total := 0.0
	for _, e := range entries {
		total += num(e["level"])
	}
	if total <= 0 {
		return 0
	}
	w := 0.4
	if phase == "bulk" {
		w = 1
	}
	return round1(min(10, math.Sqrt(total)*2.6*w))
}

// --- the page ------------------------------------------------------------------------------------------

func (k *wk) nutrition() int {
	su := &k.c.somaUI
	date := su.nutDate
	if date == "" {
		date = somaToday()
	}
	today := somaToday()
	t, _ := time.Parse("2006-01-02", date)
	title := t.Format("Monday 2 January")
	if date == today {
		title = "Today · " + t.Format("2 January")
	}
	k.header([]string{"Personal", "Nutrition"}, "Nutrition", title, color.RGBA{})
	x := k.primary("wk:sheet-open:addfood:", "plus", "Add food", wkX1)
	if date < today {
		x = k.square("wk:nutnav:1", "chevron-right", x-13)
	}
	x = k.square("wk:nutnav:-1", "chevron-left", x-13)
	if date != today {
		k.outline("wk:nutnav:0", "", "Today", x-13, 103, 63)
	}
	tab := k.sel("tab", 0)
	k.tabs("wk:tab", []string{"Dashboard", "Log", "Week", "Weight"}, nil, tab)
	k.swipe("nutday:", k.R(wkX0, 200, wkX1, 300))
	switch tab {
	case 1:
		return k.foodLog(date)
	case 2:
		return k.foodWeek(date)
	case 3:
		return k.weightPage(date)
	}
	return k.foodDash(date)
}

func (k *wk) foodDash(date string) int {
	day := foodDay(date)
	items := foodList(day, "items")
	planned := foodList(day, "planned")
	tot := totalsOf(items)
	burn := 0.0
	if eatsBack() {
		burn = trainingBurn(date)
	}
	goalC := dayGoal(day, "cals") + burn
	// The day's totals.
	y := 312
	cr := image.Rect(wkX0, y, wkX1, y+300)
	k.card(cr)
	k.text(wkFont("semi", 19), 188, y+48, wkMuted, "CALORIES")
	k.textR(wkFont("bold", 26), 1136, y+50, wkFG, fmt.Sprintf("%s / %s", fmtThousands(int(tot.cals)), fmtThousands(int(goalC))))
	k.hBar(k.R(188, y+72, 1136, y+90), tot.cals/max(goalC, 1), rgb(0xff375f))
	left := goalC - tot.cals
	note := fmt.Sprintf("%s kcal left", fmtThousands(int(max(left, 0))))
	if left < 0 {
		note = fmt.Sprintf("%s kcal over", fmtThousands(int(-left)))
	}
	if burn > 0 {
		note += fmt.Sprintf(" · +%.0f for today's training", burn)
	}
	if pt := totalsOf(planned); pt.cals > 0 {
		note += fmt.Sprintf(" · %s more waiting", fmtThousands(int(pt.cals)))
	}
	k.text(wkFont("regular", 18), 188, y+124, wkFaint, note)
	cw := (wkX1 - wkX0 - 56 - 30*3) / 4
	for i, m := range []struct {
		l    string
		v, g float64
		col  color.RGBA
	}{
		{"PROTEIN", tot.p, dayGoal(day, "protein"), rgb(0x9cf03a)}, {"CARBS", tot.c, dayGoal(day, "carbs"), wkBlue},
		{"FAT", tot.f, dayGoal(day, "fat"), rgb(0xff5a5a)}, {"FIBER", tot.fiber, dayGoal(day, "fiber"), wkAmber},
	} {
		x0 := 188 + i*(cw+30)
		k.text(wkFont("semi", 16), x0, y+176, wkMuted, m.l)
		e := k.text(wkFont("bold", 30), x0, y+216, m.col, fmt.Sprintf("%.0f", m.v))
		k.text(wkFont("medium", 18), e+4, y+216, wkMuted, fmt.Sprintf("/%.0fg", m.g))
		k.hBar(k.R(x0, y+236, x0+cw, y+244), m.v/max(m.g, 1), m.col)
	}
	y = cr.Max.Y + 20

	// Waiting to be eaten.
	if len(planned) > 0 {
		pc := image.Rect(wkX0, y, wkX1, y+90+80*len(planned))
		k.card(pc)
		k.cardHead(wkX0+28, y+46, "clock", "Waiting", "· tap ✓ once it's eaten")
		k.outline("wk:fconfirmall:"+date, "check", "All eaten", 1136, y+16, 50)
		ry := y + 80
		for i, f := range planned {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			k.iconBtn(fmt.Sprintf("wk:fconfirm:%s:%d", date, i), "check", 214, ry+40, 24, blend(wkCard, wkGreen, 0.25), wkGreen)
			k.text(wkFont("semi", 21), 256, ry+36, wkMuted, clip(wkFont("semi", 21), str(f["name"]), 560))
			k.text(wkFont("regular", 17), 256, ry+62, wkFaint, fmt.Sprintf("%s · %g %s", str(f["meal"]), num(f["serving"]), str(f["unit"])))
			k.textR(wkFont("bold", 21), 1100, ry+48, wkMuted, fmt.Sprintf("%.0f kcal", num(f["cals"])))
			k.icon("ellipsis", 1124, ry+40, 22, wkFaint)
			k.btn(fmt.Sprintf("wk:sheet-open:fitem:%s|planned|%d", date, i), k.R(250, ry, wkX1, ry+80))
			ry += 80
		}
		y = pc.Max.Y + 20
	}

	// The meal timeline: each meal's share of the day against what's in it.
	mt := mealTimes()
	tc := image.Rect(wkX0, y, wkX1, y+90+64*len(mt))
	k.card(tc)
	k.cardHead(wkX0+28, y+46, "calendar-clock", "Meal timeline", "· each meal's share of the day")
	ry := y + 76
	now := time.Now().In(displayZone)
	for _, m := range mt {
		var in float64
		for _, f := range items {
			if strings.EqualFold(str(f["meal"]), m.Label) {
				in += num(f["cals"])
			}
		}
		target := goalC * m.Share / 100
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("medium", 18), 188, ry+40, wkFaint, m.Time)
		k.text(wkFont("semi", 21), 270, ry+40, wkFG, m.Label)
		col := wkLime
		var h, mm int
		fmt.Sscanf(m.Time, "%d:%d", &h, &mm)
		if date == somaToday() && now.Hour()*60+now.Minute() > h*60+mm+90 && in < target*0.5 {
			col = wkAmber
		}
		k.hBar(k.R(500, ry+28, 900, ry+38), in/max(target, 1), col)
		k.textR(wkFont("medium", 19), 1136, ry+40, wkText2, fmt.Sprintf("%.0f / %.0f", in, target))
		ry += 64
	}
	y = tc.Max.Y + 20

	// Water, with the water in food.
	wc := image.Rect(wkX0, y, 652, y+260)
	k.card(wc)
	water, goalW := dayWaterMl(day), dayGoal(day, "water")
	k.cardHead(wkX0+28, y+46, "droplet", "Water", "")
	k.textR(wkFont("bold", 22), 624, y+46, rgb(0x4cc9f0), fmt.Sprintf("%.1f / %.1f L", water/1000, goalW/1000))
	k.hBar(k.R(188, y+72, 624, y+84), water/goalW, rgb(0x4cc9f0))
	if fw := water - num(day["water"]); fw > 0 {
		k.text(wkFont("regular", 17), 188, y+116, wkFaint, fmt.Sprintf("%.0f ml of it from food", fw))
	}
	for i, ml := range []int{250, 500, 750} {
		x0 := 188 + i*148
		k.boxBtn(fmt.Sprintf("wk:fwater:%s:%d", date, ml), x0, y+140, x0+136, y+196, "plus", fmt.Sprintf("%d", ml), color.RGBA{}, wkFG)
	}
	k.boxBtn(fmt.Sprintf("wk:fwater:%s:-250", date), 188, y+204, 624, y+246, "minus", "Take 250 ml off", color.RGBA{}, wkMuted)
	// Hunger.
	hc := image.Rect(672, y, wkX1, y+260)
	k.card(hc)
	hs := hungerOn(date)
	k.cardHead(700, y+46, "flame", "Hunger", fmt.Sprintf("· %d today", len(hs)))
	for lv := 1; lv <= 3; lv++ {
		x0 := 700 + (lv-1)*148
		k.boxBtn(fmt.Sprintf("wk:fhunger:%s:%d", date, lv), x0, y+76, x0+136, y+130, "", hungerLabels[lv], color.RGBA{}, []color.RGBA{wkFG, wkAmber, rgb(0xff6b57), wkRed}[lv])
	}
	ry = y + 146
	for i := len(hs) - 1; i >= 0 && ry < y+240; i-- {
		at, _ := time.Parse(time.RFC3339, str(hs[i]["at"]))
		k.text(wkFont("regular", 17), 700, ry+20, wkText2, at.In(displayZone).Format("15:04")+" · "+hungerLabels[int(num(hs[i]["level"]))])
		k.icon("x", 1110, ry+14, 18, wkFaint)
		k.btn("wk:fhungerdel:"+str(hs[i]["at"]), k.R(1080, ry-6, wkX1, ry+30))
		ry += 30
	}
	if p := hungerPenalty(hs); p > 0 {
		k.text(wkFont("regular", 16), 700, y+248, wkFaint, fmt.Sprintf("−%.1f points off the day score", p))
	}
	y = wc.Max.Y + 20

	// Adding food: your usual ones.
	rec := recentFoods(somaToday(), 12)
	ac := image.Rect(wkX0, y, wkX1, y+100+66*((len(rec)+2)/3))
	k.card(ac)
	k.cardHead(wkX0+28, y+46, "history", "What you eat most", "· tap to add one")
	k.outline("wk:type-open:fsearch:", "search", "Search foods", 1136, y+16, 50)
	for i, f := range rec {
		x0 := 188 + (i%3)*318
		yy := y + 80 + (i/3)*66
		r := k.R(x0, yy, x0+300, yy+54)
		ui.RoundRect(k.img, r, 14, rgb(0x1c1d24))
		apText(k.img, wkFont("medium", 18), r.Min.X+16, r.Min.Y+24, wkFG, clip(wkFont("medium", 18), str(f["name"]), 270))
		apText(k.img, wkFont("regular", 15), r.Min.X+16, r.Min.Y+46, wkFaint, fmt.Sprintf("%.0f kcal · %.0f g protein", num(f["cals"]), num(f["p"])))
		k.btn(fmt.Sprintf("wk:frecent:%d", i), r)
	}
	if len(rec) == 0 {
		k.emptyNote(188, y+110, "The foods you log show here. Search Soma's library above.")
	}
	return ac.Max.Y + 20
}

// foodLog is the day meal by meal, every item, and the micronutrients.
func (k *wk) foodLog(date string) int {
	day := foodDay(date)
	items := foodList(day, "items")
	y := 312
	meals := mealTimes()
	known := map[string]bool{}
	for _, m := range meals {
		known[strings.ToLower(m.Label)] = true
	}
	labels := []string{}
	for _, m := range meals {
		labels = append(labels, m.Label)
	}
	for _, f := range items { // meals the phone named that aren't in the list (Pre-workout...)
		if m := str(f["meal"]); m != "" && !known[strings.ToLower(m)] {
			known[strings.ToLower(m)] = true
			labels = append(labels, m)
		}
	}
	for _, meal := range labels {
		var in []int
		for i, f := range items {
			if strings.EqualFold(str(f["meal"]), meal) {
				in = append(in, i)
			}
		}
		var mt macros
		for _, i := range in {
			mt.cals += num(items[i]["cals"])
			mt.p += num(items[i]["p"])
		}
		cr := image.Rect(wkX0, y, wkX1, y+86+76*max(len(in), 1))
		k.card(cr)
		e := k.text(wkFont("semi", 23), 188, y+48, wkFG, meal)
		k.text(wkFont("regular", 18), e+14, y+48, wkFaint, fmt.Sprintf("%.0f kcal · %.0f g protein", mt.cals, mt.p))
		k.outline("wk:faddto:"+meal, "plus", "", 1136, y+16, 50)
		if len(in) == 0 {
			if prev := lastMealDate(date, meal); prev != "" {
				k.outline("wk:frepeat:"+meal+"|"+prev, "repeat", "As on "+dayLabel(prev), 1066, y+16, 50)
			}
		}
		ry := y + 80
		for _, i := range in {
			f := items[i]
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			k.text(wkFont("semi", 21), 188, ry+34, wkFG, clip(wkFont("semi", 21), str(f["name"]), 600))
			sub := fmt.Sprintf("%g %s · %.0f p · %.0f c · %.0f f", num(f["serving"]), str(f["unit"]), num(f["p"]), num(f["c"]), num(f["f"]))
			if at := num(f["eatenAt"]); at > 0 {
				sub = time.UnixMilli(int64(at)).In(displayZone).Format("15:04") + " · " + sub
			}
			k.text(wkFont("regular", 17), 188, ry+60, wkFaint, sub)
			k.textR(wkFont("bold", 21), 1100, ry+44, wkFG, fmt.Sprintf("%.0f kcal", num(f["cals"])))
			k.icon("ellipsis", 1124, ry+38, 22, wkFaint)
			k.btn(fmt.Sprintf("wk:sheet-open:fitem:%s|items|%d", date, i), k.R(wkX0, ry, wkX1, ry+76))
			ry += 76
		}
		if len(in) == 0 {
			k.emptyNote(188, ry+44, "Nothing yet.")
		}
		y = cr.Max.Y + 16
	}
	// The micronutrients.
	y += 8
	type micro struct {
		key, label, unit string
	}
	mic := []micro{{"fiber", "Fibre", "g"}, {"sodium", "Sodium", "mg"}, {"potassium", "Potassium", "mg"}, {"calcium", "Calcium", "mg"},
		{"iron", "Iron", "mg"}, {"magnesium", "Magnesium", "mg"}, {"zinc", "Zinc", "mg"}, {"vitC", "Vitamin C", "mg"}, {"vitD", "Vitamin D", "µg"}}
	mc := image.Rect(wkX0, y, wkX1, y+90+56*((len(mic)+1)/2))
	k.card(mc)
	k.cardHead(wkX0+28, y+46, "activity", "Micronutrients", "· what the logged foods carry")
	for i, m := range mic {
		v := 0.0
		for _, f := range items {
			v += num(f[m.key])
		}
		x0 := 188 + (i%2)*480
		yy := y + 80 + (i/2)*56
		k.text(wkFont("medium", 19), x0, yy+26, wkText2, m.label)
		g := dayGoal(day, m.key)
		if g > 0 {
			k.hBar(k.R(x0+170, yy+16, x0+330, yy+24), v/g, wkBlue)
		}
		lbl := fmt.Sprintf("%.0f %s", v, m.unit)
		if g > 0 {
			lbl = fmt.Sprintf("%.0f/%.0f", v, g)
		}
		k.textR(wkFont("semi", 18), x0+450, yy+26, wkFG, lbl)
	}
	return mc.Max.Y + 20
}

// lastMealDate is the last day before date with that meal logged.
func lastMealDate(date, meal string) string {
	best := ""
	for _, k := range soma.keysWith("nutrition/") {
		d := strings.TrimPrefix(k, "nutrition/")
		if d >= date || d <= best {
			continue
		}
		for _, f := range foodList(somaObj(k), "items") {
			if strings.EqualFold(str(f["meal"]), meal) {
				best = d
				break
			}
		}
	}
	return best
}

func (k *wk) foodWeek(date string) int {
	days := make([]string, 7)
	for i := range days {
		days[i] = wkAddDays(date, i-6)
	}
	y := 312
	var cals, prot []float64
	labels := make([]string, 7)
	var sc, sp float64
	logged := 0
	for i, d := range days {
		day := foodDay(d)
		t := totalsOf(foodList(day, "items"))
		cals, prot = append(cals, t.cals), append(prot, t.p)
		tt, _ := time.Parse("2006-01-02", d)
		labels[i] = tt.Format("Mon")
		if t.cals > 0 {
			sc += t.cals
			sp += t.p
			logged++
		}
	}
	box := k.row4(y)
	day := foodDay(date)
	avg := func(v float64) float64 { return v / math.Max(float64(logged), 1) }
	k.small(box(0), "flame", "Calories a day", fmtThousands(int(avg(sc))), "", fmt.Sprintf("goal %s", fmtThousands(int(dayGoal(day, "cals")))), wkFG)
	k.small(box(1), "target", "Protein a day", fmt.Sprintf("%.0f", avg(sp)), "g", fmt.Sprintf("goal %.0f g", dayGoal(day, "protein")), wkFG)
	k.small(box(2), "calendar", "Days logged", fmt.Sprint(logged), "of 7", "", wkFG)
	k.small(box(3), "activity", "Weight", fmt.Sprintf("%.1f", bodyweightOn(date)), "kg", "latest", wkFG)
	y += 184
	chart := func(title string, vals []float64, goal float64, col color.RGBA) {
		r := image.Rect(wkX0, y, wkX1, y+300)
		k.card(r)
		k.cardHead(wkX0+28, y+44, "chart-column", title, "")
		k.bars(image.Rect(wkX0+90, y+84, wkX1-28, y+250), vals, col, goal, labels, 1, "")
		y = r.Max.Y + 20
	}
	chart("Calories", cals, dayGoal(day, "cals"), rgb(0xff375f))
	chart("Protein", prot, dayGoal(day, "protein"), rgb(0x9cf03a))
	// Day by day.
	cr := image.Rect(wkX0, y, wkX1, y+90+70*len(days))
	k.card(cr)
	hf := wkFont("semi", 16)
	for i, c := range []struct {
		l string
		x int
	}{{"DAY", 188}, {"KCAL", 520}, {"PROTEIN", 680}, {"CARBS", 820}, {"FAT", 940}, {"WATER", 1136}} {
		if i == 0 {
			k.text(hf, c.x, y+50, wkFaint, c.l)
		} else {
			k.textR(hf, c.x, y+50, wkFaint, c.l)
		}
	}
	ry := y + 70
	for i := len(days) - 1; i >= 0; i-- {
		d := foodDay(days[i])
		t := totalsOf(foodList(d, "items"))
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		f := wkFont("regular", 21)
		k.text(wkFont("semi", 21), 188, ry+44, wkFG, wkDate(days[i]))
		k.textR(f, 520, ry+44, wkFG, fmtThousands(int(t.cals)))
		k.textR(f, 680, ry+44, wkFG, fmt.Sprintf("%.0f g", t.p))
		k.textR(f, 820, ry+44, wkFG, fmt.Sprintf("%.0f g", t.c))
		k.textR(f, 940, ry+44, wkFG, fmt.Sprintf("%.0f g", t.f))
		k.textR(f, 1136, ry+44, wkFG, fmt.Sprintf("%.1f L", dayWaterMl(d)/1000))
		k.btn("wk:nutday:"+days[i], k.R(wkX0, ry, wkX1, ry+70))
		ry += 70
	}
	return cr.Max.Y + 20
}

// weightPage is weighing in and the trend.
func (k *wk) weightPage(date string) int {
	day := foodDay(date)
	w := num(day["bodyWeight"])
	latest := bodyweightOn(date)
	y := 312
	cr := image.Rect(wkX0, y, wkX1, y+190)
	k.card(cr)
	k.text(wkFont("semi", 19), 188, y+48, wkMuted, "WEIGHT ON "+strings.ToUpper(wkDate(date)))
	shown := w
	if shown == 0 {
		shown = latest
	}
	vc := wkFG
	if w == 0 {
		vc = wkFaint
	}
	k.text(wkFont("bold", 54), 188, y+124, vc, fmt.Sprintf("%.1f", shown))
	k.text(wkFont("medium", 24), 188+ui.TextWidth(wkFont("bold", 54), fmt.Sprintf("%.1f", shown))+8, y+124, wkMuted, "kg")
	if w == 0 {
		k.text(wkFont("regular", 18), 188, y+162, wkFaint, "Not weighed this day: the last weighing is shown. Step it to log.")
	}
	for i, d := range []float64{-0.5, -0.1, 0.1, 0.5} {
		x0 := 640 + i*126
		k.boxBtn(fmt.Sprintf("wk:fweight:%s:%g", date, d), x0, y+70, x0+114, y+130, "", fmt.Sprintf("%+g", d), color.RGBA{}, wkFG)
	}
	k.outline("wk:type-open:fweigh:"+date, "pencil", "Type it", 1136, y+138, 44)
	y = cr.Max.Y + 20
	// The last 90 days.
	var ds []string
	var vs []float64
	for i := 89; i >= 0; i-- {
		d := wkAddDays(date, -i)
		if v := num(somaObj("nutrition/" + d)["bodyWeight"]); v > 0 {
			ds, vs = append(ds, d), append(vs, v)
		}
	}
	gc := image.Rect(wkX0, y, wkX1, y+380)
	k.card(gc)
	k.cardHead(wkX0+28, y+46, "trending-up", "90 days", "· each weighing, and the 7-day average")
	if len(vs) >= 2 {
		lo, hi := vs[0], vs[0]
		for _, v := range vs {
			lo, hi = min(lo, v), max(hi, v)
		}
		pad := max((hi-lo)*0.2, 0.3)
		lo, hi = lo-pad, hi+pad
		pr := k.R(260, y+90, 1110, y+320)
		t0, _ := time.Parse("2006-01-02", wkAddDays(date, -89))
		px := func(d string) int {
			t, _ := time.Parse("2006-01-02", d)
			return pr.Min.X + int(t.Sub(t0).Hours()/24/89*float64(pr.Dx()))
		}
		py := func(v float64) int { return pr.Max.Y - int((v-lo)/(hi-lo)*float64(pr.Dy())) }
		for i := 0; i <= 3; i++ {
			v := lo + (hi-lo)*float64(i)/3
			apTextRight(k.img, wkFont("regular", 16), pr.Min.X-12, py(v)+6, wkFaint, fmt.Sprintf("%.1f", v))
			ui.Fill(k.img, image.Rect(pr.Min.X, py(v), pr.Max.X, py(v)+1), rgb(0x1a1b21))
		}
		for i := range vs {
			ui.Circle(k.img, px(ds[i]), py(vs[i]), 5, blend(wkCard, wkBlue, 0.7))
		}
		// The 7-day average.
		var px0, py0 int
		for i := range vs {
			sum, n := 0.0, 0.0
			for j := range vs {
				dj, _ := time.Parse("2006-01-02", ds[j])
				di, _ := time.Parse("2006-01-02", ds[i])
				if d := di.Sub(dj).Hours() / 24; d >= 0 && d < 7 {
					sum += vs[j]
					n++
				}
			}
			x, yy := px(ds[i]), py(sum/n)
			if i > 0 {
				line(k.img, px0, py0, x, yy, 3, wkLime)
			}
			px0, py0 = x, yy
		}
		apText(k.img, wkFont("regular", 16), pr.Min.X, pr.Max.Y+30, wkFaint, dayLabel(wkAddDays(date, -89)))
		apTextRight(k.img, wkFont("regular", 16), pr.Max.X, pr.Max.Y+30, wkFaint, dayLabel(date))
		y = gc.Max.Y + 20
		first, last := vs[0], vs[len(vs)-1]
		t0d, _ := time.Parse("2006-01-02", ds[0])
		t1d, _ := time.Parse("2006-01-02", ds[len(ds)-1])
		weeks := max(t1d.Sub(t0d).Hours()/24/7, 1)
		box := k.row4(y)
		k.small(box(0), "activity", "Latest", fmt.Sprintf("%.1f", last), "kg", dayLabel(ds[len(ds)-1]), wkFG)
		k.small(box(1), "trending-up", "Change", fmt.Sprintf("%+.1f", last-first), "kg", fmt.Sprintf("since %s", dayLabel(ds[0])), wkFG)
		k.small(box(2), "calendar", "A week", fmt.Sprintf("%+.2f", (last-first)/weeks), "kg", "on average", wkFG)
		k.small(box(3), "list-checks", "Weighings", fmt.Sprint(len(vs)), "", "in 90 days", wkFG)
		return box(0).Max.Y + 20
	}
	k.emptyNote(188, y+160, "Weigh in a couple of times to see the trend.")
	return gc.Max.Y + 20
}

// --- sheets, actions and fields ------------------------------------------------------------------------

func (c *console) portionFor(f obj, mode string) {
	su := &c.somaUI
	amt := num(f["serving"])
	if amt <= 0 {
		amt = 100
	}
	meal := str(f["meal"])
	if meal == "" || strings.HasPrefix(mode, "add") {
		meal = su.foodMeal
		if meal == "" {
			meal = mealNow()
		}
	}
	su.portion, su.portionAmt, su.portionMeal, su.portionMode = f, amt, meal, mode
	c.openSheet("portion:")
}

func init() {
	wkActions["nutnav"] = func(c *console, arg string) {
		su := &c.somaUI
		d := su.nutDate
		if d == "" {
			d = somaToday()
		}
		switch arg {
		case "0":
			d = ""
		default:
			n, _ := strconv.Atoi(arg)
			d = wkAddDays(d, n)
			if d >= somaToday() {
				d = ""
			}
		}
		su.nutDate = d
	}
	wkSwipes["nutday"] = func(c *console, _ string, dir int) { wkActions["nutnav"](c, fmt.Sprint(dir)) }
	wkActions["nutday"] = func(c *console, date string) {
		c.wkGo("nutrition", "")
		c.somaUI.nutDate = date
		if date >= somaToday() {
			c.somaUI.nutDate = ""
		}
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel["nutritionwk:tab"] = "1"
	}
	day := func(c *console) string {
		if c.somaUI.nutDate != "" {
			return c.somaUI.nutDate
		}
		return somaToday()
	}

	// Adding.
	wkSheets["addfood"] = func(k *wk, _ string) *wkSheet {
		sh := &wkSheet{title: "Add food", sub: "To " + map[bool]string{true: "today (it waits until you mark it eaten)", false: wkDate(day(k.c))}[day(k.c) == somaToday()]}
		sh.rows = append(sh.rows, wkRow{id: "wk:type-open:fsearch:", label: "Search Soma's foods", icon: "search"},
			wkRow{id: "wk:type-open:fquick:", label: "Quick add", icon: "pencil", detail: "name kcal protein carbs fat"})
		for i, f := range recentFoods(somaToday(), 6) {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:frecent:%d", i), label: str(f["name"]), icon: "history", detail: fmt.Sprintf("%.0f kcal", num(f["cals"]))})
		}
		return sh
	}
	wkActions["faddto"] = func(c *console, meal string) {
		c.somaUI.foodMeal = meal
		c.openSheet("addfood:")
	}
	wkPrompts["fsearch"] = func(string) (string, string) {
		if loadFoodLibrary() == nil {
			return "A food (Soma's library is still downloading: your own foods for now)", ""
		}
		return "A food, e.g. chicken, lben, rice", ""
	}
	wkTypers["fsearch"] = func(c *console, _, text string) {
		c.somaUI.foodQuery = text
		c.openSheet("fresults:")
	}
	wkSheets["fresults"] = func(k *wk, _ string) *wkSheet {
		q := k.c.somaUI.foodQuery
		res := searchFoods(q, 30)
		sh := &wkSheet{title: "“" + q + "”", sub: fmt.Sprintf("%d foods · per serving", len(res))}
		for i, f := range res {
			d := fmt.Sprintf("%.0f kcal / %g %s", num(f["cals"]), num(f["serving"]), firstNonEmpty(str(f["unit"]), "g"))
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:fpick:%d", i), label: str(f["name"]), detail: d})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:type-open:fsearch:", label: "Search again", icon: "search"},
			wkRow{id: "wk:type-open:fquick:", label: "Not here: add it by hand", icon: "pencil"})
		return sh
	}
	wkActions["fpick"] = func(c *console, arg string) {
		i, _ := strconv.Atoi(arg)
		res := searchFoods(c.somaUI.foodQuery, 30)
		if i >= 0 && i < len(res) {
			f := obj{}
			for k, v := range res[i] {
				f[k] = v
			}
			c.portionFor(f, "add")
		}
	}
	wkActions["frecent"] = func(c *console, arg string) {
		i, _ := strconv.Atoi(arg)
		rec := recentFoods(somaToday(), 12)
		if i >= 0 && i < len(rec) {
			f := obj{}
			for k, v := range rec[i] {
				f[k] = v
			}
			c.portionFor(f, "add")
		}
	}
	wkPrompts["fquick"] = func(string) (string, string) { return "Name, kcal, protein, carbs, fat: Wrap 450 35 40 12", "" }
	wkTypers["fquick"] = func(c *console, _, text string) {
		f := strings.Fields(text)
		var nums []float64
		for len(f) > 0 && len(nums) < 4 {
			v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(f[len(f)-1]), "g"), "kcal"), 64)
			if err != nil {
				break
			}
			nums = append([]float64{v}, nums...)
			f = f[:len(f)-1]
		}
		name := strings.Join(f, " ")
		if name == "" || len(nums) == 0 || nums[0] <= 0 {
			c.toastMsg("Type the name, then its calories: Wrap 450 35 40 12")
			return
		}
		for len(nums) < 4 {
			nums = append(nums, 0)
		}
		meal := c.somaUI.foodMeal
		if meal == "" {
			meal = mealNow()
		}
		item := obj{"name": name, "serving": 1, "unit": "serving", "cals": nums[0], "p": nums[1], "c": nums[2], "f": nums[3], "fiber": 0,
			"sodium": 0, "potassium": 0, "calcium": 0, "iron": 0, "magnesium": 0, "zinc": 0, "meal": meal}
		addFoodOn(day(c), item)
		c.somaUI.foodMeal = ""
		c.toastMsg(name + " added")
	}

	// The portion.
	wkSheets["portion"] = func(k *wk, _ string) *wkSheet {
		su := &k.c.somaUI
		if su.portion == nil {
			return nil
		}
		f := scaleFood(su.portion, su.portionAmt, su.portionMeal)
		unit := firstNonEmpty(str(f["unit"]), "g")
		step := 25.0
		quick := []float64{50, 100, 150, 200, 250}
		switch unit {
		case "ml":
			step, quick = 50, []float64{200, 250, 330, 500, 1000}
		case "g":
		default:
			step, quick = 1, []float64{1, 2, 3}
		}
		var qo []wkOpt
		for _, q := range quick {
			qo = append(qo, wkOpt{id: fmt.Sprintf("wk:famt:=%g", q), label: fmt.Sprintf("%g", q), sel: su.portionAmt == q})
		}
		var mo []wkOpt
		for _, m := range mealTimes() {
			mo = append(mo, wkOpt{id: "wk:fmeal:" + m.Label, label: m.Label, sel: strings.EqualFold(m.Label, su.portionMeal)})
		}
		ok := "Add to " + su.portionMeal
		if su.portionMode != "add" {
			ok = "Save"
		}
		sh := &wkSheet{title: str(f["name"]), sub: fmt.Sprintf("%.0f kcal · %.1f g protein · %.1f carbs · %.1f fat", num(f["cals"]), num(f["p"]), num(f["c"]), num(f["f"])), rows: []wkRow{
			{kind: "stepper", label: "Amount (" + unit + ")", detail: fmt.Sprintf("%g", su.portionAmt), id: "wk:type-open:famount:", minus: fmt.Sprintf("wk:famt:%g", -step), plus: fmt.Sprintf("wk:famt:%g", step)},
			{kind: "seg", opts: qo},
			{kind: "seg", label: "Meal", opts: mo},
		}}
		if w := num(f["waterMl"]); w > 0 {
			sh.rows = append(sh.rows, wkRow{kind: "text", detail: fmt.Sprintf("%.0f ml of it counts as water.", w)})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:fportionok", label: ok, icon: "check", col: wkLime})
		return sh
	}
	wkActions["famt"] = func(c *console, arg string) {
		su := &c.somaUI
		if v, ok := strings.CutPrefix(arg, "="); ok {
			su.portionAmt, _ = strconv.ParseFloat(v, 64)
		} else {
			d, _ := strconv.ParseFloat(arg, 64)
			su.portionAmt = max(su.portionAmt+d, 1)
		}
		su.keep = true
	}
	wkPrompts["famount"] = func(string) (string, string) { return "How much", "" }
	wkTypers["famount"] = func(c *console, _, text string) {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64); err == nil && v > 0 {
			c.somaUI.portionAmt = v
		}
	}
	wkActions["fmeal"] = func(c *console, m string) {
		c.somaUI.portionMeal = m
		c.somaUI.keep = true
	}
	wkActions["fportionok"] = func(c *console, _ string) {
		su := &c.somaUI
		if su.portion == nil {
			return
		}
		f := scaleFood(su.portion, su.portionAmt, su.portionMeal)
		mode := su.portionMode
		d := day(c)
		switch {
		case mode == "add":
			addFoodOn(d, f)
			c.toastMsg(str(f["name"]) + " added")
		default: // edit:<list>:<i>
			p := strings.Split(mode, ":")
			if len(p) == 3 {
				i, _ := strconv.Atoi(p[2])
				if old := foodAt(d, p[1], i); old != nil {
					if v := old["loggedAt"]; v != nil {
						f["loggedAt"] = v
					}
					setFoodAt(d, p[1], i, f)
				}
			}
		}
		su.portion, su.foodMeal = nil, ""
		su.sheet, su.sheetStack = "", nil
		su.keep = true
	}

	// A logged or waiting item.
	wkSheets["fitem"] = func(k *wk, arg string) *wkSheet {
		p := strings.Split(arg, "|")
		if len(p) != 3 {
			return nil
		}
		d, list := p[0], p[1]
		i, _ := strconv.Atoi(p[2])
		f := foodAt(d, list, i)
		if f == nil {
			return nil
		}
		sh := &wkSheet{title: str(f["name"]), sub: fmt.Sprintf("%g %s · %.0f kcal · %.0f g protein · %s", num(f["serving"]), str(f["unit"]), num(f["cals"]), num(f["p"]), str(f["meal"]))}
		locked := num(f["eatenAt"]) > 0 && list == "items"
		var mo []wkOpt
		for _, m := range mealTimes() {
			mo = append(mo, wkOpt{id: fmt.Sprintf("wk:fmove:%s|%s|%d|%s", d, list, i, m.Label), label: m.Label, sel: strings.EqualFold(m.Label, str(f["meal"]))})
		}
		if list == "planned" {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:fconfirm:%s:%d", d, i), label: "Eaten", icon: "check", col: wkGreen})
		}
		if !locked {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:fedit:%s|%s|%d", d, list, i), label: "Change the amount", icon: "pencil"})
		}
		sh.rows = append(sh.rows, wkRow{kind: "seg", label: "Meal", opts: mo})
		if list == "items" && !locked && d == somaToday() {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:funconfirm:%s:%d", d, i), label: "Not eaten yet", icon: "rotate-ccw"})
		}
		sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:fagain:%s|%s|%d", d, list, i), label: "Have it again today", icon: "copy"})
		if locked {
			sh.rows = append(sh.rows, wkRow{kind: "text", detail: "Eaten at " + time.UnixMilli(int64(num(f["eatenAt"]))).In(displayZone).Format("15:04") + ": locked, as on the phone, so the meal's timing stays true."})
		} else {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:fdel:%s|%s|%d", d, list, i), label: map[string]string{"items": "Delete", "planned": "Remove from the plan"}[list], icon: "trash-2", danger: true})
		}
		return sh
	}
	parse := func(arg string) (string, string, int, bool) {
		p := strings.SplitN(arg, "|", 4)
		if len(p) < 3 {
			return "", "", 0, false
		}
		i, err := strconv.Atoi(p[2])
		return p[0], p[1], i, err == nil
	}
	wkActions["fconfirm"] = func(c *console, arg string) {
		d, is, _ := strings.Cut(arg, ":")
		i, _ := strconv.Atoi(is)
		confirmPlanned(d, i)
	}
	wkActions["fconfirmall"] = func(c *console, d string) {
		if n := confirmAll(d); n > 0 {
			c.toastMsg(fmt.Sprintf("%d marked eaten", n))
		}
	}
	wkActions["funconfirm"] = func(c *console, arg string) {
		d, is, _ := strings.Cut(arg, ":")
		i, _ := strconv.Atoi(is)
		unconfirmFood(d, i)
	}
	wkActions["fedit"] = func(c *console, arg string) {
		d, list, i, ok := parse(arg)
		if f := foodAt(d, list, i); ok && f != nil {
			c.portionFor(f, "edit:"+list+":"+strconv.Itoa(i))
		}
	}
	wkActions["fmove"] = func(c *console, arg string) {
		p := strings.SplitN(arg, "|", 4)
		d, list, i, ok := parse(arg)
		if !ok || len(p) != 4 {
			return
		}
		if f := foodAt(d, list, i); f != nil {
			f["meal"] = p[3]
			setFoodAt(d, list, i, f)
		}
		c.somaUI.keep = true
	}
	wkActions["fagain"] = func(c *console, arg string) {
		d, list, i, ok := parse(arg)
		f := foodAt(d, list, i)
		if !ok || f == nil {
			return
		}
		n := obj{}
		for k, v := range f {
			n[k] = v
		}
		delete(n, "eatenAt")
		delete(n, "loggedAt")
		addFoodOn(somaToday(), n)
		c.toastMsg("Added to today")
	}
	wkActions["fdel"] = func(c *console, arg string) {
		d, list, i, ok := parse(arg)
		f := foodAt(d, list, i)
		if !ok || f == nil || list == "items" && num(f["eatenAt"]) > 0 {
			return
		}
		setFoodAt(d, list, i, nil)
		c.offerUndo(str(f["name"])+" removed", func() { insertFoodAt(d, list, i, f) })
	}
	wkActions["frepeat"] = func(c *console, arg string) {
		meal, from, _ := strings.Cut(arg, "|")
		n := 0
		for _, f := range foodList(foodDay(from), "items") {
			if strings.EqualFold(str(f["meal"]), meal) {
				x := obj{}
				for k, v := range f {
					x[k] = v
				}
				delete(x, "eatenAt")
				delete(x, "loggedAt")
				addFoodOn(day(c), x)
				n++
			}
		}
		c.toastMsg(fmt.Sprintf("%s as on %s: %d items", meal, dayLabel(from), n))
	}

	// Water, hunger, weight.
	wkActions["fwater"] = func(c *console, arg string) {
		d, ml, _ := strings.Cut(arg, ":")
		v, _ := strconv.ParseFloat(ml, 64)
		addWaterOn(d, v)
	}
	wkActions["fhunger"] = func(c *console, arg string) {
		d, lv, _ := strings.Cut(arg, ":")
		n, _ := strconv.Atoi(lv)
		at := time.Now().UTC()
		if d != somaToday() {
			if t, err := time.ParseInLocation("2006-01-02", d, displayZone); err == nil {
				at = t.Add(time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute).UTC()
			}
		}
		putHunger(append(hungerAll(), obj{"date": d, "at": at.Format("2006-01-02T15:04:05.000Z"), "level": n}))
		c.toastMsg(hungerLabels[n] + " logged")
	}
	wkActions["fhungerdel"] = func(c *console, at string) {
		all := hungerAll()
		var out []obj
		var gone obj
		at0 := 0
		for i, h := range all {
			if str(h["at"]) == at && gone == nil {
				gone, at0 = h, i
				continue
			}
			out = append(out, h)
		}
		putHunger(out)
		if gone != nil {
			c.offerUndo("Hunger removed", func() {
				l := hungerAll()
				i := min(at0, len(l))
				putHunger(append(l[:i:i], append([]obj{gone}, l[i:]...)...))
			})
		}
	}
	wkActions["fweight"] = func(c *console, arg string) {
		d, dv, _ := strings.Cut(arg, ":")
		v, _ := strconv.ParseFloat(dv, 64)
		patchFoodDay(d, func(day obj) {
			w := num(day["bodyWeight"])
			if w == 0 {
				w = bodyweightOn(d)
			}
			if w == 0 {
				w = 75
			}
			day["bodyWeight"] = round1(w + v)
		})
	}
	wkPrompts["fweigh"] = func(string) (string, string) { return "Weight in kg", "" }
	wkTypers["fweigh"] = func(c *console, d, text string) {
		v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
		if err != nil || v < 20 || v > 400 {
			return
		}
		patchFoodDay(d, func(day obj) { day["bodyWeight"] = round1(v) })
	}
}
