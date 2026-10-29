package main

import "testing"

func TestHabitsHeatAndSteps(t *testing.T) {
	c := workAt(t, "habits")
	somaPut("habits#teeth", obj{"id": "teeth", "name": "Teeth routine", "color": "#5b8cff", "history": obj{},
		"steps": []any{obj{"id": "a", "name": "Brush 1", "target": 1}, obj{"id": "b", "name": "Brush 2", "target": 1},
			obj{"id": "c", "name": "Brush 3", "target": 1}, obj{"id": "d", "name": "Floss", "target": 1},
			obj{"id": "e", "name": "Mouthwash rinse", "target": 1}, obj{"id": "f", "name": "Tongue scrub", "target": 2}}})
	c.showPage()
	if !hasBtn(c, "wk:hstep:teeth:f") || !hasBtn(c, "wk:htick:teeth") {
		t.Fatal("every step and Done should be on the row")
	}
	shot(t, c, "habits-heat")
	tapBtn(t, c, "wk:htick:teeth") // Done: the whole checklist
	h := somaHabit{ID: "teeth", o: somaObj("habits#teeth")}
	for _, s := range h.steps() {
		if h.stepCount(somaToday(), s.ID) < s.Target {
			t.Fatalf("Done left %s at %d", s.Name, h.stepCount(somaToday(), s.ID))
		}
	}
	shot(t, c, "habits-done")
}
