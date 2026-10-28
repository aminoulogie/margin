package main

import (
	"fmt"
	"testing"
	"time"
)

// Soma after a few years: thousands of to-dos and days. The Today page must still draw fast.
func TestSomaScale(t *testing.T) {
	c, _, _ := somaTestLink(t)
	soma.sync()
	d0 := time.Now()
	for i := 0; i < 3000; i++ {
		date := somaDate(d0.AddDate(0, 0, -i/3))
		soma.Recs[fmt.Sprintf("todos#old%d", i)] = somaRec{V: ptr(fmt.Sprintf(`{"id":"old%d","text":"Old task %d","done":true,"date":%q,"scope":"day"}`, i, i, date))}
	}
	for i := 0; i < 1000; i++ {
		date := somaDate(d0.AddDate(0, 0, -i))
		soma.Recs["nutrition/"+date] = somaRec{V: ptr(`{"items":[{"name":"x","cals":500,"p":30}],"water":1000}`)}
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeSoma)
	for _, v := range []string{"today", "calendar", "reports", "reports-year", "money", "goals"} {
		c.somaUI.view, c.somaUI.repRange = v, ""
		if v == "reports-year" {
			c.somaUI.view, c.somaUI.repRange = "reports", "year"
		}
		start := time.Now()
		c.somaPage()
		took := time.Since(start)
		t.Logf("%s: %v", v, took)
		if took > 1500*time.Millisecond {
			t.Errorf("%s took %v", v, took)
		}
	}
}

func ptr(s string) *string { return &s }
