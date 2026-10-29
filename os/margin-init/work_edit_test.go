package main

import (
	"image"
	"strings"
	"testing"
	"time"
)

// The workstation as a finger uses it: each test finds a button on the page as drawn, taps
// its middle through the touch path (so a sheet over the page gets the tap first), types with
// the keyboard, and checks what Soma's records say afterwards.

// findBtn is the button with this id on the page (a sheet's rows come first).
func findBtn(c *console, id string) (image.Rectangle, bool) {
	for _, b := range c.page.buttons {
		if b.id == id {
			return b.r, true
		}
	}
	for _, bs := range c.page.rowBtns {
		for _, b := range bs {
			if b.id == id {
				return b.r, true
			}
		}
	}
	return image.Rectangle{}, false
}

// screenOf scrolls the page so page point (x, y) shows, and gives its place on the screen.
func screenOf(c *console, x, y int) (int, int) {
	hh, vh := c.page.header, c.viewH()
	if y < hh {
		return x, y + c.barH
	}
	if ih := c.page.img.Rect.Dy(); ih > vh {
		sy := c.scrollY()
		if y < sy+hh+40 || y > sy+vh-260 { // keep it clear of the keyboard and the pills
			c.setScroll(modeSoma, min(max(y-hh-300, 0), ih-vh))
			c.blitPage()
		}
		sy = min(max(c.scrollY(), 0), ih-vh)
		return x, y - sy + c.barH
	}
	return x, y + c.barH
}

// tapBtn taps the button id where it's drawn. Caller holds drawMu.
func tapBtn(t *testing.T, c *console, id string) {
	t.Helper()
	r, ok := findBtn(c, id)
	if !ok {
		var ids []string
		for _, b := range c.page.buttons {
			if strings.HasPrefix(b.id, "wk:") || strings.HasPrefix(b.id, "so:") {
				ids = append(ids, b.id)
			}
		}
		t.Fatalf("no button %q on the page (%s); there are %v", id, c.somaUI.work, ids)
	}
	x, y := screenOf(c, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
	if got := c.page.hit(x, c.pageY(y)); got != id {
		t.Fatalf("tapping %q at (%d,%d) hits %q", id, x, y, got)
	}
	c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
	c.pageTouch(TouchPoint{Up: true, X: x, Y: y})
}

// swipeAcross moves a finger sideways across the middle of a zone (dir 1: right to left).
func swipeAcross(t *testing.T, c *console, zone string, dir int) {
	t.Helper()
	for _, z := range c.page.swipes {
		if z.id != zone {
			continue
		}
		x, y := screenOf(c, (z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
		for i := 1; i <= 5; i++ {
			c.pageTouch(TouchPoint{Moved: true, X: x - dir*i*60, Y: y + i})
		}
		c.pageTouch(TouchPoint{Up: true, X: x - dir*300, Y: y + 5})
		return
	}
	t.Fatalf("no swipe zone %q", zone)
}

// typeIn types text and return into the field being typed in.
func typeIn(t *testing.T, c *console, text string) {
	t.Helper()
	if !c.somaUI.typing {
		t.Fatalf("not typing (field %q)", c.somaUI.field)
	}
	c.somaKey([]byte("\x15" + text + "\r"))
}

func hasBtn(c *console, id string) bool { _, ok := findBtn(c, id); return ok }

func workAt(t *testing.T, page string) *console {
	c := workFixture(t)
	drawMu.Lock()
	t.Cleanup(drawMu.Unlock)
	c.setMode(modeSoma)
	c.somaUI.work = page
	c.showPage()
	return c
}

func TestProjectsUsable(t *testing.T) {
	c := workAt(t, "projects")
	shot(t, c, "edit-projects")

	// New project: typed, opened, in the records with Soma's fields.
	tapBtn(t, c, "wk:newproject")
	typeIn(t, c, "Bridge deck review")
	if c.somaUI.work != "project" {
		t.Fatalf("the new project didn't open: %q", c.somaUI.work)
	}
	id := c.somaUI.workProj
	o := somaObj("projects#" + id)
	if str(o["name"]) != "Bridge deck review" || str(o["status"]) != "active" || num(o["createdAt"]) == 0 || str(o["color"]) == "" {
		t.Fatalf("new project %v", o)
	}
	if ids := somaIDs("projects"); ids[len(ids)-1] != id {
		t.Fatalf("not at the end of the order: %v", ids)
	}

	// Its sheet: client, status, priority, deadline, rate, colour.
	tapBtn(t, c, "wk:sheet-open:proj:"+id)
	shot(t, c, "edit-project-sheet")
	tapBtn(t, c, "wk:sheet-open:pclientof:"+id)
	tapBtn(t, c, "wk:type-open:pnewclient:"+id)
	typeIn(t, c, "Harbour Council")
	tapBtn(t, c, "wk:pset:"+id+":priority:high")
	tapBtn(t, c, "wk:pset:"+id+":status:paused")
	tapBtn(t, c, "wk:pset:"+id+":color:#e2607a")
	tapBtn(t, c, "wk:pdue:"+id)
	shot(t, c, "edit-date-sheet")
	tapBtn(t, c, "wk:calnav:1")
	next := time.Now().In(displayZone)
	next = time.Date(next.Year(), next.Month()+1, 15, 0, 0, 0, 0, displayZone)
	tapBtn(t, c, "wk:pickdate:"+next.Format("2006-01-02"))
	tapBtn(t, c, "wk:type-open:prate:"+id)
	typeIn(t, c, "42.5")
	o = somaObj("projects#" + id)
	if str(o["client"]) != "Harbour Council" || str(o["priority"]) != "high" || str(o["status"]) != "paused" ||
		str(o["color"]) != "#e2607a" || str(o["due"]) != next.Format("2006-01-02") || num(o["rate"]) != 42.5 {
		t.Fatalf("edited project %v", o)
	}

	// Steps: add two (the field stays open for the next), tick, rename, move, delete.
	tapBtn(t, c, "wk:sheet:close")
	tapBtn(t, c, "wk:tab:1")
	tapBtn(t, c, "wk:type-open:step:"+id)
	typeIn(t, c, "Read the drawings")
	typeIn(t, c, "Check the bearings")
	c.somaUI.typing, c.skb.visible = false, false
	c.showPage()
	p, _ := wkProjectByID(id)
	if len(p.Steps) != 2 || p.Steps[1].Label != "Check the bearings" {
		t.Fatalf("steps %+v", p.Steps)
	}
	s0, s1 := p.Steps[0].ID, p.Steps[1].ID
	tapBtn(t, c, "wk:step:"+id+":"+s0)
	tapBtn(t, c, "wk:sheet-open:step:"+id+":"+s1)
	tapBtn(t, c, "wk:stepmove:"+id+":"+s1+":-1")
	tapBtn(t, c, "wk:type-open:steprename:"+id+":"+s1)
	typeIn(t, c, "Check the bearings and joints")
	p, _ = wkProjectByID(id)
	if p.Steps[0].ID != s1 || p.Steps[0].Label != "Check the bearings and joints" || !p.Steps[1].Done {
		t.Fatalf("steps after edits %+v", p.Steps)
	}
	shot(t, c, "edit-project-steps") // the step's sheet, back after renaming
	tapBtn(t, c, "wk:stepdel:"+id+":"+s1)
	if p, _ = wkProjectByID(id); len(p.Steps) != 1 {
		t.Fatalf("step not deleted %+v", p.Steps)
	}
	tapBtn(t, c, "wk:undo")
	if p, _ = wkProjectByID(id); len(p.Steps) != 2 {
		t.Fatalf("undo didn't bring the step back %+v", p.Steps)
	}

	// Time: by hand, then edited.
	tapBtn(t, c, "wk:tab:2")
	tapBtn(t, c, "wk:sheet-open:addtime:"+id)
	tapBtn(t, c, "wk:addtime:"+id+":90")
	var eid string
	for _, e := range wkEntries() {
		if e.ProjectID == id {
			eid = e.ID
			if d := e.End.Sub(e.Start); d != 90*time.Minute {
				t.Fatalf("entry of %v", d)
			}
		}
	}
	c.showPage()
	shot(t, c, "edit-project-time")
	tapBtn(t, c, "wk:sheet-open:entry:"+eid)
	tapBtn(t, c, "wk:eset:"+eid+":billable:0")
	tapBtn(t, c, "wk:eset:"+eid+":start:-15")
	if o := somaObj("timeEntries#" + eid); o["billable"] != false || (num(o["end"])-num(o["start"]))/60000 != 105 {
		t.Fatalf("entry after edits %v", o)
	}

	// Notes, and a finding.
	tapBtn(t, c, "wk:sheet:close")
	tapBtn(t, c, "wk:tab:3")
	tapBtn(t, c, "wk:type-open:pnote:"+id)
	typeIn(t, c, "Spoke to the engineer")
	tapBtn(t, c, "wk:type-open:pbrief:"+id)
	typeIn(t, c, "Deck slab and bearings, client wants rev A by Friday")
	p, _ = wkProjectByID(id)
	if len(p.Log) != 1 || p.Note == "" {
		t.Fatalf("notes %+v brief %q", p.Log, p.Note)
	}
	shot(t, c, "edit-project-notes")
	tapBtn(t, c, "wk:tab:4")
	tapBtn(t, c, "wk:type-open:pfinding:"+id)
	typeIn(t, c, "Bearing type not on drawings")
	var fid string
	for _, f := range wkFindings() {
		if f.ProjectID == id {
			fid = f.ID
		}
	}
	tapBtn(t, c, "wk:sheet-open:finding:"+fid)
	tapBtn(t, c, "wk:fset:"+fid+":severity:critical")
	tapBtn(t, c, "wk:fset:"+fid+":status:resolved")
	if o := somaObj("findings#" + fid); str(o["severity"]) != "critical" || str(o["status"]) != "resolved" || num(o["closedAt"]) == 0 {
		t.Fatalf("finding %v", o)
	}

	// Back to the list: sort, the client filter, search.
	tapBtn(t, c, "wk:sheet:close")
	tapBtn(t, c, "wk:back")
	if c.somaUI.work != "projects" {
		t.Fatalf("back went to %q", c.somaUI.work)
	}
	tapBtn(t, c, "wk:sheet-open:sort")
	tapBtn(t, c, "wk:psort:name")
	if c.somaUI.pf.sort != "name" {
		t.Fatal("sort")
	}
	tapBtn(t, c, "wk:sheet-open:clients")
	tapBtn(t, c, "wk:pclient:Northbank Ltd")
	c.showPage()
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:proj:") && b.id != "wk:proj:p1" && b.id != "wk:proj:p2" {
			t.Fatalf("%s shown with Northbank's filter", b.id)
		}
	}
	shot(t, c, "edit-projects-filtered")
	tapBtn(t, c, "wk:sheet-open:clients")
	tapBtn(t, c, "wk:pclient:")
	tapBtn(t, c, "wk:psearch")
	typeIn(t, c, "slab")
	if !hasBtn(c, "wk:proj:p3") || hasBtn(c, "wk:proj:p1") {
		t.Fatal("search for slab")
	}
	tapBtn(t, c, "wk:pquery:")

	// The board, the timeline (swiped), the archive.
	tapBtn(t, c, "wk:tab:1")
	shot(t, c, "edit-projects-board")
	tapBtn(t, c, "wk:sheet-open:pstatus:p5")
	tapBtn(t, c, "wk:pmove:p5:done")
	if p, _ := wkProjectByID("p5"); p.Status != "done" {
		t.Fatal("board move")
	}
	tapBtn(t, c, "wk:tab:2")
	shot(t, c, "edit-projects-timeline")
	swipeAcross(t, c, "ptimeline:", 1)
	if c.sel("week") != 1 {
		t.Fatalf("timeline swipe: week %d", c.sel("week"))
	}
	tapBtn(t, c, "wk:tab:3")
	shot(t, c, "edit-projects-archive")
	tapBtn(t, c, "wk:prestore:p5")
	if p, _ := wkProjectByID("p5"); p.Status != "active" {
		t.Fatal("restore")
	}
	tapBtn(t, c, "wk:tab:0")
	tapBtn(t, c, "wk:proj:p4")
	tapBtn(t, c, "wk:sheet-open:proj:p4")
	tapBtn(t, c, "wk:pdelete:p4")
	if _, ok := wkProjectByID("p4"); ok || c.somaUI.work != "projects" {
		t.Fatal("delete")
	}
	tapBtn(t, c, "wk:undo")
	if _, ok := wkProjectByID("p4"); !ok {
		t.Fatal("undo delete")
	}
}

// sel reads a page's chosen tab or chip as the page does.
func (c *console) sel(group string) int {
	k := &wk{c: c}
	return k.sel(group, 0)
}

func TestPlannerUsable(t *testing.T) {
	c := workAt(t, "planner")
	shot(t, c, "plan-week")
	today := somaToday()

	// A to-do at a time: tap the empty 10:00 today.
	tapBtn(t, c, "wk:slotadd:"+today+":600")
	typeIn(t, c, "Review rev B")
	var id string
	for _, td := range somaTodos() {
		if td.Text == "Review rev B" {
			id = td.ID
			if td.SlotDate != today || td.SlotStart != 600 || td.SlotMins != 60 {
				t.Fatalf("slot %+v", td)
			}
		}
	}
	c.showPage()
	tapBtn(t, c, "wk:sheet-open:task:"+id)
	shot(t, c, "plan-task-sheet")
	tapBtn(t, c, "wk:tslot:"+id+":start:15")
	tapBtn(t, c, "wk:tslot:"+id+":mins:-15")
	tapBtn(t, c, "wk:tprio:"+id+":high")
	tapBtn(t, c, "wk:tdue:"+id)
	tapBtn(t, c, "wk:pickdate:"+wkAddDays(today, 1))
	o := somaObj("todos#" + id)
	s, _ := o["slot"].(obj)
	if num(s["start"]) != 615 || num(s["mins"]) != 45 || str(o["priority"]) != "high" || str(o["due"]) != wkAddDays(today, 1) {
		t.Fatalf("after edits %v", o)
	}
	tapBtn(t, c, "wk:type-open:trename:"+id)
	typeIn(t, c, "Review rev B drawings")
	tapBtn(t, c, "wk:ttomorrow:"+id)
	if o := somaObj("todos#" + id); str(o["text"]) != "Review rev B drawings" || str(o["date"]) != wkAddDays(today, 1) {
		t.Fatalf("rename/tomorrow %v", o)
	}

	// A plain to-do, then plan the day around it.
	tapBtn(t, c, "wk:newtask")
	typeIn(t, c, "Email the council")
	tapBtn(t, c, "wk:planmyday")
	planned := false
	for _, td := range somaTodos() {
		if td.Text == "Email the council" && td.SlotDate == today {
			planned = true
		}
	}
	if !planned && time.Now().In(displayZone).Hour() < 22 {
		t.Fatal("plan my day didn't give it a time")
	}

	// Swipes: weeks, then a day, then a month.
	swipeAcross(t, c, "plan:", 1)
	if c.planDay().Format("2006-01-02") != wkAddDays(today, 7) {
		t.Fatalf("week swipe: %v", c.planDay())
	}
	swipeAcross(t, c, "plan:", -1)
	tapBtn(t, c, "wk:planday:"+today)
	shot(t, c, "plan-day")
	swipeAcross(t, c, "plan:", 1)
	if c.planDay().Format("2006-01-02") != wkAddDays(today, 1) {
		t.Fatalf("day swipe: %v", c.planDay())
	}
	tapBtn(t, c, "wk:plannav:0")
	tapBtn(t, c, "wk:tab:2")
	shot(t, c, "plan-month")
	swipeAcross(t, c, "plan:", 1)
	if c.planDay().Month() == time.Now().In(displayZone).Month() {
		t.Fatal("month swipe")
	}
	tapBtn(t, c, "wk:plannav:0")
	tapBtn(t, c, "wk:tab:3")
	shot(t, c, "plan-board")
	tapBtn(t, c, "wk:sheet-open:task:t2")
	tapBtn(t, c, "wk:tdel:t2")
	if somaObj("todos#t2") != nil {
		t.Fatal("delete")
	}
	tapBtn(t, c, "wk:undo")
	if somaObj("todos#t2") == nil {
		t.Fatal("undo")
	}

	// Routines: one, with a step.
	tapBtn(t, c, "wk:tab:4")
	tapBtn(t, c, "wk:type-open:rtnew:")
	typeIn(t, c, "Morning")
	rs := wkRoutines()
	if len(rs) != 1 {
		t.Fatalf("routines %+v", rs)
	}
	tapBtn(t, c, "wk:type-open:rtstep:"+rs[0].ID)
	typeIn(t, c, "Stretch 10")
	c.somaUI.typing, c.skb.visible = false, false
	c.showPage()
	if rs = wkRoutines(); len(rs[0].Steps) != 1 || rs[0].Steps[0].Seconds != 600 || rs[0].Steps[0].Label != "Stretch" {
		t.Fatalf("routine %+v", rs)
	}
	shot(t, c, "plan-routines")
}

func TestHabitsUsable(t *testing.T) {
	c := workAt(t, "habits")
	today := somaToday()
	shot(t, c, "habits-empty")

	tapBtn(t, c, "wk:type-open:newhabit:")
	typeIn(t, c, "Floss")
	byName := func(n string) somaHabit {
		for _, h := range somaHabits() {
			if h.Name == n {
				return h
			}
		}
		t.Fatalf("no habit %q", n)
		return somaHabit{}
	}
	h := byName("Floss")
	if str(h.o["since"]) != today || num(h.o["goalDaysPerWeek"]) != 7 || !strings.HasPrefix(h.ID, "habit-") {
		t.Fatalf("new habit %+v", h)
	}
	id := h.ID
	c.showPage()
	tapBtn(t, c, "wk:htick:"+id)
	if !byName("Floss").done(today) {
		t.Fatal("tick")
	}
	// A checklist and a ramp from the ready-made ones.
	tapBtn(t, c, "wk:hpreset:0") // Brush teeth
	tapBtn(t, c, "wk:hpreset:0") // Skincare, first of what's left
	brush, ramp := byName("Brush teeth"), byName("Skincare")
	if len(brush.steps()) != 2 || len(ramp.steps()) != 3 {
		t.Fatalf("presets %+v %+v", brush.o, ramp.o)
	}
	c.showPage()
	for _, s := range brush.steps() {
		tapBtn(t, c, "wk:hstep:"+brush.ID+":"+s.ID)
	}
	for _, h := range somaHabits() {
		if h.ID == brush.ID && !h.done(today) {
			t.Fatalf("both steps done but the day isn't: %v", h.o)
		}
	}
	tapBtn(t, c, "wk:hpreset:0") // now Read: a ramp
	var read somaHabit
	for _, h := range somaHabits() {
		if h.Name == "Read" {
			read = h
		}
	}
	if read.rampOf() == nil {
		t.Fatalf("ramp %v", read.o)
	}
	c.showPage()
	shot(t, c, "habits-today")
	tapBtn(t, c, "wk:hamt:"+read.ID+":5")
	tapBtn(t, c, "wk:hamt:"+read.ID+":5")
	for _, h := range somaHabits() {
		if h.ID == read.ID {
			if l, rung, _ := h.amount(today); l != 10 || !h.done(today) || rung != 10 {
				t.Fatalf("ramp logged %v of %v done %v", l, rung, h.done(today))
			}
		}
	}

	// Its sheet: colour, days, importance, when, a step.
	tapBtn(t, c, "wk:habit:"+id)
	shot(t, c, "habit-page")
	tapBtn(t, c, "wk:sheet-open:habit:"+id)
	tapBtn(t, c, "wk:hset:"+id+":goal:-1")
	tapBtn(t, c, "wk:hset:"+id+":coef:4")
	tapBtn(t, c, "wk:hset:"+id+":time:morning")
	tapBtn(t, c, "wk:hset:"+id+":color:#ff5c8a")
	shot(t, c, "habit-sheet")
	tapBtn(t, c, "wk:type-open:hrename:"+id)
	typeIn(t, c, "Floss twice")
	o := somaObj("habits#" + id)
	if num(o["goalDaysPerWeek"]) != 6 || num(o["coef"]) != 4 || str(o["time"]) != "morning" || str(o["color"]) != "#ff5c8a" || str(o["name"]) != "Floss twice" {
		t.Fatalf("edited habit %v", o)
	}
	tapBtn(t, c, "wk:sheet:close")
	tapBtn(t, c, "wk:type-open:hnote:"+id)
	typeIn(t, c, "Easier after a walk")
	if notes, _ := somaObj("habits#" + id)["notes"].([]any); len(notes) != 1 {
		t.Fatal("note")
	}
	tapBtn(t, c, "wk:back")
	for i := 1; i <= 3; i++ {
		tapBtn(t, c, "wk:tab:"+string(rune('0'+i)))
		shot(t, c, "habits-tab"+string(rune('0'+i)))
	}
	tapBtn(t, c, "wk:tab:1")
	swipeAcross(t, c, "hweek:", -1)
	if c.sel("week") != -1 {
		t.Fatal("week swipe")
	}
	tapBtn(t, c, "wk:tab:0")
	tapBtn(t, c, "wk:habit:"+id)
	tapBtn(t, c, "wk:sheet-open:habit:"+id)
	tapBtn(t, c, "wk:hdel:"+id)
	if somaObj("habits#"+id) != nil || c.somaUI.workSub != "" {
		t.Fatal("delete")
	}
	tapBtn(t, c, "wk:undo")
	if somaObj("habits#"+id) == nil {
		t.Fatal("undo")
	}
}
