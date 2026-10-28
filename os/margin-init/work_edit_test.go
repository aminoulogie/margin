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
