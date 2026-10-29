package main

import (
	"os"
	"sort"
	"testing"
	"time"
)

// How long each workstation page takes to draw, on this machine (the tablet's Atom is
// roughly ten to fifteen times slower). MARGIN_SPEED=1 prints the table.
func TestWorkPageSpeed(t *testing.T) {
	if os.Getenv("MARGIN_SPEED") == "" {
		t.Skip("set MARGIN_SPEED=1")
	}
	c := workAt(t, "overview")
	type row struct {
		page string
		d    time.Duration
		h    int
	}
	var rows []row
	pages := []string{}
	for _, p := range wkPages {
		pages = append(pages, p.id)
	}
	for _, p := range wkPersonal {
		pages = append(pages, p.id)
	}
	pages = append(pages, "settings", "project")
	for _, pg := range pages {
		c.somaUI.work = pg
		if pg == "project" {
			c.somaUI.workProj = "p1"
		}
		c.showPage() // once to learn its height
		const n = 5
		start := time.Now()
		for range n {
			wkPageCache.drop() // drawn, not taken from the kept ones
			c.showPage()
		}
		rows = append(rows, row{pg, time.Since(start) / n, c.page.img.Rect.Dy()})
	}
	// Back and forth between two pages: the second time each is kept.
	c.somaUI.work = "overview"
	c.showPage()
	c.somaUI.work = "projects"
	c.showPage()
	start := time.Now()
	for i := range 10 {
		c.somaUI.work = []string{"overview", "projects"}[i%2]
		c.showPage()
	}
	t.Logf("switching to a kept page: %.1f ms", float64(time.Since(start).Microseconds())/10000)
	sort.Slice(rows, func(i, j int) bool { return rows[i].d > rows[j].d })
	for _, r := range rows {
		t.Logf("%-10s %6.1f ms  %5d rows", r.page, float64(r.d.Microseconds())/1000, r.h)
	}
}
