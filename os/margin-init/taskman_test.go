package main

import (
	"testing"
	"time"
)

// The Task Manager reads this machine's processor, memory and processes, and draws them.
func TestTaskManager(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	c.setPane = "tasks"
	c.setMode(modeSettings)
	drawMu.Unlock()
	time.Sleep(2500 * time.Millisecond)
	drawMu.Lock()
	defer drawMu.Unlock()
	ts := &c.tasks
	if ts.cur == nil || ts.cur.memTotal == 0 || len(ts.cur.cpu) < 2 || len(ts.top(5)) == 0 {
		t.Fatalf("sample %+v", ts.cur)
	}
	if p := ts.pct(0); p < 0 || p > 100 {
		t.Fatalf("cpu %v", p)
	}
	c.showPage()
	shot(t, c, "settings-tasks")
	c.setMode(modeBooksHome) // the sampler stops
}
