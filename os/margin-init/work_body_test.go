package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func deadButtons(t *testing.T, c *console) {
	t.Helper()
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:") && !wkHandled(b.id) {
			t.Errorf("dead button %s on %s/%s", b.id, c.somaUI.work, c.somaUI.workSub)
		}
	}
}

func seedSession(date string, exs ...obj) {
	var l []any
	for _, e := range exs {
		l = append(l, e)
	}
	somaPut("history/"+date, obj{"split": "Pull", "exercises": l, "timestamp": float64(time.Now().UnixMilli())})
}

func liftDone(name string, keys []any, sets int) obj {
	var ss []any
	for range sets {
		ss = append(ss, obj{"weight": 40.0, "reps": 10.0, "done": true, "type": "normal"})
	}
	ss = append(ss, obj{"weight": 20.0, "reps": 10.0, "done": true, "type": "warmup"}) // never counted
	return obj{"name": name, "targetKeys": keys, "sets": ss}
}

func TestEveningOutsideAndAches(t *testing.T) {
	c := workAt(t, "evening")
	today := somaToday()
	seedSession(today, liftDone("Lat Pulldown (Wide/Neutral)", []any{"upper_back", "biceps"}, 3))
	c.showPage()
	deadButtons(t, c)

	tapBtn(t, c, "wk:evact:"+today+":climbing")
	acts := func(date string) []obj { return dayList(somaRead("nutrition/"+date), "activities") }
	if a := acts(today); len(a) != 1 || str(a[0]["id"]) != "climbing" || num(a[0]["hours"]) != 2 || num(a[0]["effort"]) != 2 {
		t.Fatalf("activities %v", a)
	}
	tapBtn(t, c, "wk:evacth:"+today+":0:0.5")
	tapBtn(t, c, "wk:evacte:"+today+":0:2")
	if a := acts(today)[0]; num(a["hours"]) != 2.5 || num(a["effort"]) != 3 {
		t.Errorf("climb now %v, want 2.5 h, hard", a)
	}
	for want := range []int{1, 2, 3, 0} {
		tapBtn(t, c, "wk:evache:"+today+":knee")
		if got := acheLevel(somaRead("nutrition/"+today), "knee"); got != (want+1)%4 {
			t.Errorf("knee after %d taps: %d", want+1, got)
		}
	}
	tapBtn(t, c, "wk:evache:"+today+":lower_back")
	if a := dayList(somaRead("nutrition/"+today), "aches"); len(a) != 1 || str(a[0]["where"]) != "lower_back" || num(a[0]["level"]) != 1 {
		t.Errorf("aches %v", a)
	}

	// Yesterday's, from the toggle.
	yesterday := wkAddDays(today, -1)
	tapBtn(t, c, "wk:seg:evday:1")
	tapBtn(t, c, "wk:evact:"+yesterday+":running")
	if a := acts(yesterday); len(a) != 1 || str(a[0]["id"]) != "running" {
		t.Errorf("yesterday's activities %v", a)
	}
	tapBtn(t, c, "wk:evactx:"+yesterday+":0")
	if a := acts(yesterday); len(a) != 0 {
		t.Errorf("not removed: %v", a)
	}
	tapBtn(t, c, "wk:seg:evday:0")

	// How today's exercise went.
	tapBtn(t, c, "wk:sheet-open:hwrap:"+today+":0")
	for _, f := range []string{"feel:1", "held:joint", "pain:shoulder", "straps:1"} {
		tapBtn(t, c, "wk:hwrap:"+today+":0:"+f)
		if c.somaUI.sheet == "" {
			t.Fatalf("the sheet closed after %s", f)
		}
	}
	deadButtons(t, c)
	shot(t, c, "evening-wrap")
	tapBtn(t, c, "wk:type-open:hwrapnote:"+today+":0")
	typeIn(t, c, "Pinched at the top")
	eo := sessExercise(somaRead("history/"+today), 0)
	w, _ := eo["wrap"].(obj)
	straps, _ := eo["straps"].(bool)
	if got := wrapSummary(w, straps); got != "Off · Joint pain (Shoulder) · straps" || str(w["note"]) != "Pinched at the top" {
		t.Errorf("wrap %q, note %q", got, str(w["note"]))
	}
	if num(w["at"]) == 0 {
		t.Errorf("no at on the wrap")
	}
	// Joint unheld: where it hurt goes with it.
	if hasBtn(c, "wk:hwrap:"+today+":0:held:joint") {
		tapBtn(t, c, "wk:hwrap:"+today+":0:held:joint")
		w, _ = sessExercise(somaRead("history/"+today), 0)["wrap"].(obj)
		if len(wrapStrings(w, "painAt")) != 0 {
			t.Errorf("painAt kept: %v", w)
		}
	}
	c.somaUI.sheet = ""
	c.showPage()
	shot(t, c, "evening-body")
}

func TestWrapSummary(t *testing.T) {
	for _, tc := range []struct {
		w      obj
		straps bool
		want   string
	}{
		{nil, false, ""},
		{nil, true, "straps"},
		{obj{"feel": 3.0}, false, "Strong"},
		{obj{"feel": 2.0, "held": []any{"muscle", "carried"}}, false, "OK · Target muscle · Tired from before"},
		{obj{"held": []any{"joint"}, "painAt": []any{"elbow", "lower_back"}}, true, "Joint pain (Elbow, Lower back) · straps"},
	} {
		if got := wrapSummary(tc.w, tc.straps); got != tc.want {
			t.Errorf("wrapSummary(%v, %v) = %q, want %q", tc.w, tc.straps, got, tc.want)
		}
	}
	if !wrapEmpty(obj{"at": 5.0}) || wrapEmpty(obj{"note": "x"}) {
		t.Errorf("wrapEmpty")
	}
}

func TestWeekCoverage(t *testing.T) {
	c := workAt(t, "training")
	now := time.Now().In(displayZone)
	today := somaToday()
	seedSession(today,
		liftDone("Cable Triceps Pushdown (Straight/V)", []any{"triceps_back"}, 4),
		liftDone("Lat Pulldown (Wide/Neutral)", []any{"upper_back", "biceps"}, 4),
		liftDone("Seated Calf Raise", []any{"calves"}, 3))
	lastWeek := somaDate(weekStart(now).AddDate(0, 0, -3))
	seedSession(lastWeek, liftDone("Hack Squat", []any{"quadriceps", "gluteal"}, 4))

	cov := map[string]groupCov{}
	for _, g := range weekCoverage(somaDate(weekStart(now)), today) {
		cov[g.group] = g
	}
	if g := cov["triceps"]; g.sets != 4 || !slices.Contains(g.missing, "Long head") || !slices.Contains(g.hit, "Lateral / medial head") {
		t.Errorf("triceps after pushdowns only: %+v", g)
	}
	if g := cov["back"]; g.sets != 4 || !slices.Contains(g.missing, "Mid-back (rows)") {
		t.Errorf("back: %+v", g)
	}
	if g := cov["biceps"]; g.sets != 0 { // the pulldown's biceps key doesn't credit them
		t.Errorf("biceps from a pulldown: %+v", g)
	}
	if g := cov["calves"]; g.sets != 3 || !slices.Contains(g.missing, "Gastrocnemius") {
		t.Errorf("a seated calf raise is soleus, not gastrocnemius: %+v", g)
	}
	if g := cov["quads"]; g.sets != 0 {
		t.Errorf("last week's squat counted this week: %+v", g)
	}
	if v := liftVariants("Standing Machine Calf Raise", "", []string{"calves"}); !slices.Contains(v, [2]string{"calves", "gastroc"}) {
		t.Errorf("standing calf raise: %v", v)
	}
	if v := liftVariants("Overhead Cable Triceps Extension", "", []string{"triceps_back"}); !slices.Contains(v, [2]string{"triceps", "long"}) {
		t.Errorf("overhead extension: %v", v)
	}

	// On Training's overview, and how it went on a session.
	if c.somaUI.workSel == nil {
		c.somaUI.workSel = map[string]string{}
	}
	c.somaUI.workSel["trainingwk:tab"] = "2"
	c.showPage()
	deadButtons(t, c)
	shot(t, c, "training-coverage")
	c.somaUI.workSub = "sess:" + today
	c.showPage()
	deadButtons(t, c)
	tapBtn(t, c, "wk:sheet-open:hwrap:"+today+":0")
	tapBtn(t, c, "wk:hwrap:"+today+":0:feel:3")
	c.somaUI.sheet = ""
	c.showPage()
	tapBtn(t, c, "wk:sheet-open:hex:"+today+":0")
	if !hasBtn(c, "wk:sheet-open:hwrap:"+today+":0") {
		t.Errorf("the exercise's sheet has no How it went")
	}
	if s := wkSessions(); len(s) == 0 || len(s[0].Ex) == 0 || num(s[0].Ex[0].Wrap["feel"]) != 3 {
		t.Errorf("parsed wrap: %+v", s)
	}
	shot(t, c, "training-session-wrap")
}
