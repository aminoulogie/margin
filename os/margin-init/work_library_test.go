package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTrainingLibraryAndWorkout(t *testing.T) {
	if len(libExercises()) < 50 || len(libSplits()) == 0 {
		t.Fatalf("library %d exercises, %d splits", len(libExercises()), len(libSplits()))
	}
	n := 0
	for _, e := range libExercises() {
		if e.Img != "" {
			if exPhoto(e.Name, 90, 60) == nil {
				t.Errorf("%s: its photo %s doesn't load", e.Name, e.Img)
			}
			n++
		}
	}
	if n < 50 {
		t.Fatalf("only %d photos", n)
	}
	c := workAt(t, "training")
	today := somaToday()
	if somaObj("history/"+today) != nil { // the fixture trains on even days
		somaPut("history/"+today, nil)
		soma.mu.Lock()
		delete(soma.Recs, "history/"+today)
		soma.mu.Unlock()
		somaGen.Add(1)
		c.showPage()
	}
	shot(t, c, "train-workout-start")

	// The library: chips, a muscle, an exercise's page.
	tapBtn(t, c, "wk:tab:1")
	shot(t, c, "train-library")
	tapBtn(t, c, "wk:lib:2") // Back
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:libex:") {
			var i int
			fmt.Sscan(strings.TrimPrefix(b.id, "wk:libex:"), &i)
			if libExercises()[i].Muscle != "Back" {
				t.Fatalf("%s under Back", libExercises()[i].Name)
			}
		}
	}
	shot(t, c, "train-library-back")
	first := ""
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:libex:") {
			first = b.id
			break
		}
	}
	tapBtn(t, c, first)
	shot(t, c, "train-exercise")
	var idx int
	fmt.Sscan(strings.TrimPrefix(first, "wk:libex:"), &idx)
	tapBtn(t, c, fmt.Sprintf("wk:libadd:%d", idx))
	s := parseSession(today, somaObj("history/"+today))
	if len(s.Ex) != 1 || s.Ex[0].Name != libExercises()[idx].Name || len(s.Ex[0].Sets) != 3 || s.Ex[0].Sets[0].Done {
		t.Fatalf("workout after adding %+v", s)
	}

	// A split loaded on top, a set ticked (rest starts), weight changed, a set added.
	somaPut("history/"+today, nil)
	soma.mu.Lock()
	delete(soma.Recs, "history/"+today)
	soma.mu.Unlock()
	somaGen.Add(1)
	c.showPage()
	tapBtn(t, c, "wk:wsplit:0")
	s = parseSession(today, somaObj("history/"+today))
	if len(s.Ex) != len(libSplits()[0].Exercises) {
		t.Fatalf("split loaded %d exercises, want %d", len(s.Ex), len(libSplits()[0].Exercises))
	}
	tapBtn(t, c, "wk:wtick:"+today+":0:0")
	if !parseSession(today, somaObj("history/"+today)).Ex[0].Sets[0].Done || !c.somaUI.restEnd.After(time.Now()) {
		t.Fatal("ticking a set should mark it done and start the rest")
	}
	w0 := parseSession(today, somaObj("history/"+today)).Ex[0].Sets[1].W
	tapBtn(t, c, "wk:hsetv:"+today+":0:1:weight:2.5")
	if w := parseSession(today, somaObj("history/"+today)).Ex[0].Sets[1].W; w != w0+2.5 {
		t.Fatalf("weight %v, want %v", w, w0+2.5)
	}
	tapBtn(t, c, "wk:waddset:"+today+":0")
	if ss := parseSession(today, somaObj("history/"+today)).Ex[0].Sets; len(ss) != 4 || ss[3].Done {
		t.Fatalf("sets after adding %+v", ss)
	}
	shot(t, c, "train-workout")
	if !hasBtn(c, "wk:rest:30") {
		t.Fatal("no rest timer at the bottom")
	}
	tapBtn(t, c, "wk:rest:0")
	// Picking from the library adds and takes out.
	tapBtn(t, c, "wk:libpick:"+today)
	shot(t, c, "train-pick")
	before := len(parseSession(today, somaObj("history/"+today)).Ex)
	for i, e := range libExercises() {
		if e.Name == "Leg Press" {
			tapBtn(t, c, fmt.Sprintf("wk:libadd:%d", i))
		}
	}
	if after := len(parseSession(today, somaObj("history/"+today)).Ex); after != before-1 && after != before+1 {
		t.Fatalf("picking Leg Press: %d exercises, before %d", after, before)
	}
	tapBtn(t, c, "wk:libdone:")
	tapBtn(t, c, "wk:wfinish:"+today)
	if !strings.HasPrefix(c.somaUI.workSub, "sess:") {
		t.Fatalf("finishing should show the session, not %q", c.somaUI.workSub)
	}
}
