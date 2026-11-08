package main

import (
	"fmt"
	"os"
	"runtime/pprof"
	"slices"
	"sort"
	"testing"
	"time"
)

// seedSomaYear is a year of Soma as the user keeps it: four sessions a week of six
// exercises, and a food day every day.
func seedSomaYear(t *testing.T) {
	t.Helper()
	now := time.Now().In(displayZone)
	lifts := [][]any{
		{"Incline Dumbbell Press", "Chest", []any{"chest", "deltoids"}},
		{"Machine Chest Press", "Chest", []any{"chest", "triceps"}},
		{"Cable Lateral Raise", "Shoulders", []any{"deltoids"}},
		{"Cable Triceps Pushdown (Straight/V)", "Triceps", []any{"triceps_back"}},
		{"Lat Pulldown (Wide/Neutral)", "Back", []any{"upper_back", "biceps"}},
		{"Chest-Supported T-Bar Row", "Back", []any{"upper_back", "trapezius_back"}},
		{"Incline Dumbbell Curl", "Biceps", []any{"biceps"}},
		{"Hack Squat", "Legs", []any{"quadriceps", "gluteal"}},
		{"Romanian Deadlift (DB/Barbell)", "Legs", []any{"hamstring", "gluteal"}},
		{"Seated Leg Curl", "Legs", []any{"hamstring"}},
		{"Standing Machine Calf Raise", "Legs", []any{"calves"}},
		{"Hanging Knee Raises", "Abs", []any{"abs"}},
	}
	for i := range 365 {
		d := now.AddDate(0, 0, -i)
		date := somaDate(d)
		var items []any
		for j := range 8 {
			items = append(items, obj{"id": fmt.Sprintf("f%d-%d", i, j), "name": "Chicken rice bowl", "cals": 520, "p": 42, "c": 60, "f": 12})
		}
		somaPut("nutrition/"+date, obj{"items": items, "water": 2600, "bodyWeight": 81.2, "sleep": obj{"hours": 7.5}, "creatine": 5})
		if i == 0 || d.Weekday() == time.Tuesday || d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue // rest days
		}
		var exs []any
		muscles := obj{}
		for j := range 6 {
			l := lifts[(i+j)%len(lifts)]
			var sets []any
			for s := range 4 {
				sets = append(sets, obj{"weight": 40 + s*2, "reps": 10, "done": true, "type": "normal", "failure": 1})
			}
			ex := obj{"name": l[0], "muscle": l[1], "targetKeys": l[2], "sets": sets}
			if j == 0 {
				ex["wrap"] = obj{"feel": 1, "held": []any{"joint"}, "painAt": []any{"shoulder"}, "note": "Pinched at the bottom"}
				ex["straps"] = true
			}
			exs = append(exs, ex)
			muscles[str(l[2].([]any)[0])] = obj{"sets": 4}
		}
		somaPut("history/"+date, obj{"split": "Push", "durationFormatted": "1h 05m", "totalVol": 9000, "totalSets": 24,
			"timestamp": float64(d.Add(18 * time.Hour).UnixMilli()), "exercises": exs, "muscles": muscles})
	}
}

// How long the Personal pages Soma lives in take with a year of data, drawn fresh and
// after a tap, the median of nine (the tablet's Atom is roughly ten to fifteen times slower). MARGIN_SPEED=1
// prints the table; MARGIN_PROFILE=<file> writes a CPU profile of the whole run.
func TestSomaPagesSpeed(t *testing.T) {
	if os.Getenv("MARGIN_SPEED") == "" {
		t.Skip("set MARGIN_SPEED=1")
	}
	c := workAt(t, "evening")
	seedSomaYear(t)
	if c.somaUI.workSel == nil {
		c.somaUI.workSel = map[string]string{}
	}
	if p := os.Getenv("MARGIN_PROFILE"); p != "" {
		f, _ := os.Create(p)
		pprof.StartCPUProfile(f)
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}
	type row struct {
		page string
		d    time.Duration
		h    int
	}
	var rows []row
	time5 := func(name string, f func()) {
		f() // once to warm the decoders
		var ds []time.Duration
		for range 9 {
			start := time.Now()
			f()
			ds = append(ds, time.Since(start))
		}
		slices.Sort(ds)
		rows = append(rows, row{name, ds[len(ds)/2], c.page.img.Rect.Dy()}) // the median: WSL's timings are noisy
	}
	draw := func(page string, tab int) func() {
		return func() {
			c.somaUI.work, c.somaUI.workSub = page, ""
			if tab >= 0 {
				c.somaUI.workSel[page+"wk:tab"] = fmt.Sprint(tab)
			}
			wkPageCache.drop()
			c.showPage()
		}
	}
	for _, p := range []string{"evening", "nutrition", "health", "stats", "habits", "calendar", "mind"} {
		time5(p, draw(p, -1))
	}
	for i, tab := range []string{"workout", "library", "overview", "movements", "sessions"} {
		time5("training/"+tab, draw("training", i))
	}
	last := wkSessions()[0].Date
	time5("training/session", func() {
		c.somaUI.work, c.somaUI.workSub = "training", "sess:"+last
		wkPageCache.drop()
		c.showPage()
	})
	// Cold: the first draw after Margin starts, nothing decoded yet.
	for _, p := range []string{"evening", "training", "nutrition", "health"} {
		var ds []time.Duration
		for range 3 {
			somaDecoded.Lock()
			somaDecoded.st = nil
			somaDecoded.Unlock()
			somaLists.Lock()
			somaLists.st = nil
			somaLists.Unlock()
			trainingLogs.Lock()
			trainingLogs.log = nil
			trainingLogs.Unlock()
			c.somaUI.work, c.somaUI.workSub = p, ""
			c.somaUI.workSel[p+"wk:tab"] = "2"
			wkPageCache.drop()
			start := time.Now()
			c.showPage()
			ds = append(ds, time.Since(start))
		}
		slices.Sort(ds)
		rows = append(rows, row{p + " (cold)", ds[1], c.page.img.Rect.Dy()})
	}
	// A tap on the evening page: the record changes, the page is drawn again.
	c.somaUI.work, c.somaUI.workSub = "evening", ""
	c.showPage()
	time5("evening tap", func() {
		wkActions["bcrea"](c, "5")
		c.showPage()
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].d > rows[j].d })
	for _, r := range rows {
		t.Logf("%-18s %7.1f ms  %6d rows", r.page, float64(r.d.Microseconds())/1000, r.h)
	}
}
