package main

import (
	"fmt"
	"os"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
	"time"
)

// bigVault adds a year and more of Soma to the workstation's fixture, about what a real vault
// holds: time entries every working day, shifts, projects with long step lists, to-dos, habits
// with long histories, food and training every day.
func bigVault(t *testing.T) *console {
	c := workFixture(t)
	now := time.Now()
	ms := func(t time.Time) float64 { return float64(t.UnixMilli()) }
	var porder []string
	for i := range 20 {
		id := fmt.Sprintf("bp%d", i)
		var steps, log []any
		for s := range 15 {
			steps = append(steps, obj{"id": fmt.Sprintf("s%d", s), "label": fmt.Sprintf("Step %d of project %d", s, i), "done": s < 8,
				"work": []any{obj{"start": ms(now.AddDate(0, 0, -s-1)), "end": ms(now.AddDate(0, 0, -s-1).Add(time.Hour))}}})
		}
		for n := range 10 {
			log = append(log, obj{"id": fmt.Sprintf("n%d", n), "text": "A note on how it went, with enough words to be a real one", "at": ms(now.AddDate(0, 0, -n))})
		}
		somaPut("projects#"+id, obj{"id": id, "name": fmt.Sprintf("Project %d", i), "client": fmt.Sprintf("Client %d", i%4), "status": "active",
			"rate": 30 + i, "steps": steps, "log": log, "createdAt": ms(now.AddDate(0, -6, 0)), "touchedAt": ms(now)})
		porder = append(porder, id)
	}
	somaPut("projects@order", append([]string{"p1", "p2", "p3", "p4", "p5"}, porder...))
	var eorder, sorder, torder []string
	for d := 1; d < 400; d++ {
		day := dayStart(now).AddDate(0, 0, -d)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		for i := range 4 {
			id := fmt.Sprintf("be%d-%d", d, i)
			start := day.Add(time.Duration(8+i*2) * time.Hour)
			somaPut("timeEntries#"+id, obj{"id": id, "projectId": fmt.Sprintf("bp%d", (d+i)%20), "start": ms(start), "end": ms(start.Add(80 * time.Minute)),
				"note": "Detailing grid lines, level " + fmt.Sprint(i), "stepId": "s2"})
			eorder = append(eorder, id)
		}
		if d%2 == 0 {
			id := fmt.Sprintf("bs%d", d)
			somaPut("shifts#"+id, obj{"id": id, "client": "Client 1", "start": ms(day.Add(7 * time.Hour)), "end": ms(day.Add(17 * time.Hour)),
				"breaks":     []any{obj{"start": ms(day.Add(12 * time.Hour)), "end": ms(day.Add(12*time.Hour + 30*time.Minute))}},
				"activities": []any{obj{"text": "Site visit"}, obj{"text": "Drawings"}}})
			sorder = append(sorder, id)
		}
	}
	old := somaIDs("timeEntries")
	somaPut("timeEntries@order", append(old, eorder...))
	somaPut("shifts@order", append(somaIDs("shifts"), sorder...))
	for i := range 400 {
		id := fmt.Sprintf("bt%d", i)
		somaPut("todos#"+id, obj{"id": id, "text": "Something to do, number " + fmt.Sprint(i), "date": now.AddDate(0, 0, -i/3).Format("2006-01-02"), "done": i%3 != 0})
		torder = append(torder, id)
	}
	somaPut("todos@order", append(somaIDs("todos"), torder...))
	var horder []string
	for i := range 15 {
		id := fmt.Sprintf("bh%d", i)
		hist := obj{}
		for d := range 400 {
			if d%3 != 0 {
				hist[now.AddDate(0, 0, -d).Format("2006-01-02")] = true
			}
		}
		somaPut("habits#"+id, obj{"id": id, "name": "Habit " + fmt.Sprint(i), "color": "#5b8cff", "history": hist})
		horder = append(horder, id)
	}
	somaPut("habits@order", horder)
	for d := 28; d < 400; d++ {
		date := now.AddDate(0, 0, -d).Format("2006-01-02")
		var items []any
		for i := range 6 {
			items = append(items, obj{"name": "Food " + fmt.Sprint(i), "cals": 400, "p": 30, "c": 40, "f": 12, "meal": "Lunch", "eatenAt": ms(now.AddDate(0, 0, -d))})
		}
		somaPut("nutrition/"+date, obj{"items": items, "water": 2000, "sleep": obj{"hours": 7}, "bodyWeight": 80})
		somaPut("reading/"+date, 25)
		somaPut("history/"+date, obj{"split": "Pull B", "durationFormatted": "50:00", "totalSets": 18, "totalVol": 9000, "timestamp": ms(now.AddDate(0, 0, -d)),
			"exercises": []any{obj{"name": "Rows", "sets": []any{obj{"kg": 70, "reps": 10}, obj{"kg": 70, "reps": 10}, obj{"kg": 70, "reps": 9}}}}})
	}
	return c
}

// How long Time > Entries takes: drawn, an entry opened, changed, closed, deleted (the median
// of 9 times each). With MARGIN_SPEED=1 (MARGIN_PROFILE=<file> writes a CPU profile).
func TestTimeEntriesSpeed(t *testing.T) {
	if os.Getenv("MARGIN_SPEED") == "" {
		t.Skip("set MARGIN_SPEED=1")
	}
	c := bigVault(t)
	soma.sync() // everything sent: no sync running while it's timed
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeSoma)
	c.somaUI.work = "time"
	c.somaUI.workSel = map[string]string{"timewk:tab": "1", "timewk:chip": "2"}
	c.showPage()
	if f := os.Getenv("MARGIN_PROFILE"); f != "" {
		pf, _ := os.Create(f)
		pprof.StartCPUProfile(pf)
		defer pprof.StopCPUProfile()
	}
	// prep (untimed, nil for none) sets each time up.
	timedAfter := func(what string, prep, fn func(i int)) {
		var ds []time.Duration
		for i := range 9 {
			if prep != nil {
				prep(i)
			}
			start := time.Now()
			fn(i)
			ds = append(ds, time.Since(start))
		}
		slices.Sort(ds)
		t.Logf("%-30s %7.1f ms", what, float64(ds[len(ds)/2].Microseconds())/1000)
	}
	timed := func(what string, fn func(i int)) { timedAfter(what, nil, fn) }
	timed("Entries drawn (not kept)", func(int) { wkPageCache.drop(); c.showPage() })
	timed("Entries shown again (kept)", func(int) { c.showPage() })
	var entry string
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:sheet-open:entry:be") {
			entry = b.id
			break
		}
	}
	if entry == "" {
		t.Fatal("no entry on the page")
	}
	id := strings.TrimPrefix(entry, "wk:sheet-open:entry:")
	timed("entry opened, closed", func(int) {
		tapBtn(t, c, entry)
		if !strings.HasPrefix(c.somaUI.sheet, "entry:") {
			t.Fatalf("the entry's sheet didn't open: %q", c.somaUI.sheet)
		}
		tapBtn(t, c, "wk:sheet:close")
	})
	tapBtn(t, c, entry)
	timed("billable switched", func(i int) { tapBtn(t, c, fmt.Sprintf("wk:eset:%s:billable:%d", id, i%2)) })
	timed("start moved 15 min", func(i int) { tapBtn(t, c, fmt.Sprintf("wk:eset:%s:start:%d", id, []int{15, -15}[i%2])) })
	tapBtn(t, c, "wk:sheet:close")
	timedAfter("closed after a change", func(i int) {
		tapBtn(t, c, entry)
		tapBtn(t, c, fmt.Sprintf("wk:eset:%s:billable:%d", id, i%2))
		waitSync(t)
		c.wkFresh = true // the quiet moment after it (wkRefreshLater)
		c.showPage()
		c.wkFresh = false
	}, func(int) { tapBtn(t, c, "wk:sheet:close") })
	timed("entry deleted, undone", func(int) {
		tapBtn(t, c, entry)
		tapBtn(t, c, "wk:edel:"+id)
		tapBtn(t, c, "wk:undo")
	})
	for _, pg := range []string{"overview", "projects", "planner", "money", "reports", "worklog", "habits", "calendar", "training", "health"} {
		c.somaUI.work = pg
		c.showPage()
		timed("page "+pg, func(int) { wkPageCache.drop(); c.showPage() })
	}
}
