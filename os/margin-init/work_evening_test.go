package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEveningCheckIn(t *testing.T) {
	c := workAt(t, "evening")
	old := supplementsPath
	supplementsPath = filepath.Join(t.TempDir(), "supplements.json")
	t.Cleanup(func() { supplementsPath = old })
	c.showPage()
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:") && !wkHandled(b.id) {
			t.Errorf("dead button %s", b.id)
		}
	}
	tapBtn(t, c, "wk:evsup:Magnesium")
	if !slices.Contains(wkTaken(somaToday()), "Magnesium") {
		t.Errorf("magnesium not taken: %v", wkTaken(somaToday()))
	}
	tapBtn(t, c, "wk:type-open:evsupadd:")
	typeIn(t, c, "Ashwagandha")
	if !slices.Contains(wkSupplements(), "Ashwagandha") {
		t.Errorf("not added: %v", wkSupplements())
	}
	tapBtn(t, c, "wk:type-open:evj:well")
	typeIn(t, c, "Finished the scans")
	tapBtn(t, c, "wk:type-open:evj:one")
	typeIn(t, c, "Book the translator")
	if j := journalRead(somaToday()); j["well"] != "Finished the scans" || j["one"] != "Book the translator" {
		t.Errorf("journal %v", j)
	}
	tomorrow := somaDate(time.Now().AddDate(0, 0, 1))
	found := false
	for _, td := range somaTodos() {
		found = found || td.Date == tomorrow && td.Text == "Book the translator" && td.Priority == "high"
	}
	if !found {
		t.Errorf("tomorrow's one thing isn't a to-do")
	}
	shot(t, c, "evening")
}
