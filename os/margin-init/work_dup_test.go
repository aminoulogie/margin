package main

import (
	"fmt"
	"testing"
)

// todosNamed are the to-dos with this text.
func todosNamed(text string) []somaTodo {
	var out []somaTodo
	for _, t := range somaTodos() {
		if t.Text == text {
			out = append(out, t)
		}
	}
	return out
}

// Duplicate a to-do onto the next 6 days at the same time; pick two and duplicate them; copy
// one and paste it at a time; undo.
func TestPlannerDuplicateCopyPaste(t *testing.T) {
	c := workAt(t, "planner")
	today := somaToday()
	somaPut("todos#d1", obj{"id": "d1", "text": "TCF practice", "done": false, "date": today, "scope": "day",
		"slot": obj{"date": today, "start": 19 * 60, "mins": 30}})
	somaPut("todos#d2", obj{"id": "d2", "text": "Stretch", "done": false, "date": today, "scope": "day",
		"slot": obj{"date": today, "start": 20 * 60, "mins": 15}})
	somaTodoOrder()
	c.showPage()

	// One to-do, 6 days on from tomorrow.
	tapBtn(t, c, "wk:sheet-open:task:d1")
	tapBtn(t, c, "wk:dupopen:d1")
	if c.somaUI.sheet != "dup:" {
		t.Fatalf("Duplicate… opened %q", c.somaUI.sheet)
	}
	for range 5 {
		tapBtn(t, c, "wk:dupn:1")
	}
	shot(t, c, "planner-duplicate")
	tapBtn(t, c, "wk:dupgo")
	got := map[string]bool{}
	for _, td := range todosNamed("TCF practice") {
		if td.SlotStart != 19*60 || td.SlotMins != 30 {
			t.Fatalf("a copy at %d for %d min", td.SlotStart, td.SlotMins)
		}
		got[td.SlotDate] = true
	}
	for i := 0; i <= 6; i++ {
		if !got[wkAddDays(today, i)] {
			t.Fatalf("no copy on %s (have %v)", wkAddDays(today, i), got)
		}
	}
	if len(got) != 7 {
		t.Fatalf("copies on %d days, want today + 6", len(got))
	}
	// Undo takes the 6 copies back.
	c.somaUI.undo.fn()
	c.somaUI.undo = nil
	if n := len(todosNamed("TCF practice")); n != 1 {
		t.Fatalf("after undo %d left, want 1", n)
	}

	// Pick both, duplicate to tomorrow (weekdays only, 1 day).
	tapBtn(t, c, "wk:selstart:")
	tapBtn(t, c, "wk:sheet-open:task:d1")
	tapBtn(t, c, "wk:sheet-open:task:d2")
	if len(c.somaUI.planSel) != 2 || c.somaUI.sheet != "" {
		t.Fatalf("picked %v, sheet %q", c.somaUI.planSel, c.somaUI.sheet)
	}
	shot(t, c, "planner-select")
	tapBtn(t, c, "wk:seldup")
	tapBtn(t, c, "wk:dupgo")
	if len(todosNamed("TCF practice")) != 2 || len(todosNamed("Stretch")) != 2 || c.somaUI.planSelOn {
		t.Fatalf("after duplicating the two: %d, %d, selecting %v", len(todosNamed("TCF practice")), len(todosNamed("Stretch")), c.somaUI.planSelOn)
	}

	// Copy one, paste at 07:00 on the calendar's first day.
	tapBtn(t, c, "wk:sheet-open:task:d2")
	tapBtn(t, c, "wk:tcopy:d2")
	day := c.page.cal.From
	c.somaUI.weekTop = 6*calPxH + 1 // 06:00 at the top
	c.showPage()
	tapBtn(t, c, fmt.Sprintf("wk:slotadd:%s:%d", day, 7*60))
	pasted := false
	for _, td := range todosNamed("Stretch") {
		pasted = pasted || (td.SlotDate == day && td.SlotStart == 7*60 && td.SlotMins == 15)
	}
	if !pasted {
		t.Fatalf("not pasted at 07:00 on %s: %+v", day, todosNamed("Stretch"))
	}
	if len(c.somaUI.planClip) != 1 {
		t.Fatal("the copy should stay ready for another paste")
	}
	tapBtn(t, c, "wk:clipclear")
	if len(c.somaUI.planClip) != 0 {
		t.Fatal("Cancel")
	}
}
