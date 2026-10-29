package main

import (
	"image"
	"testing"
)

// composerPress touches the composer's button id where it's drawn.
func composerPress(t *testing.T, c *console, id string) {
	t.Helper()
	y0 := c.skb.y0 - composerH
	for _, b := range c.composerLayout(image.NewRGBA(image.Rect(0, y0, c.s.W, c.skb.y0)), y0) {
		if b.id == id {
			c.pageTouch(TouchPoint{Down: true, X: b.r.Min.X + 10, Y: b.r.Min.Y + 10})
			c.pageTouch(TouchPoint{Up: true, X: b.r.Min.X + 10, Y: b.r.Min.Y + 10})
			return
		}
	}
	t.Fatalf("no %q on the composer", id)
}

func TestTaskComposer(t *testing.T) {
	c := workAt(t, "planner")
	tapBtn(t, c, "wk:newtask")
	if !c.composing() {
		t.Fatal("a new task should open the composer")
	}
	c.somaKey([]byte("Buy rebar for level 3"))
	shot(t, c, "compose")
	composerPress(t, c, "day:"+wkAddDays(somaToday(), 1))
	composerPress(t, c, "time:on")
	composerPress(t, c, "start:15")
	composerPress(t, c, "mins")
	composerPress(t, c, "prio")
	shot(t, c, "compose-set")
	if !c.somaUI.typing {
		t.Fatal("tapping the composer's chips stopped the typing")
	}
	cm := c.somaUI.cmp
	c.somaKey([]byte("\r"))
	var got *somaTodo
	for _, td := range somaTodos() {
		if td.Text == "Buy rebar for level 3" {
			got = &td
		}
	}
	if got == nil || got.SlotDate != wkAddDays(somaToday(), 1) || got.SlotStart != cm.start || got.SlotMins != 90 || got.Priority != "high" {
		t.Fatalf("added %+v (composer %+v)", got, cm)
	}
	// From an empty time on the week: that time, already set.
	for _, b := range c.page.buttons {
		if len(b.id) > 11 && b.id[:11] == "wk:slotadd:" {
			tapBtn(t, c, b.id)
			break
		}
	}
	if !c.composing() || c.somaUI.cmp.start < 0 {
		t.Fatalf("slot composer %+v", c.somaUI.cmp)
	}
}

func TestHealthLogToday(t *testing.T) {
	c := workAt(t, "health")
	tapBtn(t, c, "wk:bsleep:0.5")
	tapBtn(t, c, "wk:bsleepq:4")
	tapBtn(t, c, "wk:bnap:20")
	tapBtn(t, c, "wk:bcrea:5")
	tapBtn(t, c, "wk:type-open:bmeas:")
	typeIn(t, c, "waist 82 chest 104")
	c.somaUI.typing, c.skb.visible = false, false
	c.showPage()
	d := foodDay(somaToday())
	sl, _ := d["sleep"].(obj)
	ms, _ := d["measurements"].(obj)
	naps, _ := d["naps"].([]any)
	if num(sl["quality"]) != 4 || num(d["creatine"]) < 5 || num(ms["waist"]) != 82 || num(ms["chest"]) != 104 || len(naps) != 1 {
		t.Fatalf("day %v", d)
	}
	c.setScroll(modeSoma, 99999)
	c.blitPage()
	shot(t, c, "health-log")
}
