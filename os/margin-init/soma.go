package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"

	"rsc.io/qr"
)

// The Soma tab: Soma (the PC and iPhone app) on the tablet, kept in step with them through
// Soma's own sync (somasync.go). A beta, kept simple: Today (habits, reading, to-dos),
// Calendar (a month; a day's plan, to-dos, training and food), Reports (training,
// nutrition, habits, reading). Ticking a habit, reading time and to-dos can be changed here;
// the rest is shown.

type somaUI struct {
	view       string    // "today", "calendar", "goals", "notes", "reports"
	note       string    // the note open in "notes" ("" for the list)
	month      time.Time // the calendar's month
	sel        string    // the calendar's day
	calWeek    bool      // the calendar shows a week, not the month
	moving     string    // a timed to-do being moved: the next time tapped is its new time
	repRange   string    // the reports' range: "week", "month", "year"
	plan       int       // the spending plan shown in Money
	delShift   string    // a shift being deleted (asked twice)
	delStep    int
	need       map[string]int // the height each view last needed
	todoMenu   string         // the to-do held for its actions
	todoWeek   bool           // Home's To do shows this week, not today
	sleepDraft string         // hours typed in Log the gap, not logged yet
	typing     bool
	field      string // what's being typed: "todo", "week", "day" (a to-do), "url", "code"
	editing    string
	url        string // the sync address, typed before the code
	msg        string // the last thing that went wrong
	slotY      int    // page y under the to-do being typed on the timeline
}

func somaColour(hex string, def color.RGBA) color.RGBA {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if len(h) != 6 || err != nil {
		return def
	}
	return rgb(uint32(v))
}

func (c *console) somaPage() *page {
	f := apple()
	w, mx := c.s.W, pageGutter
	tw := w - 2*mx
	su := &c.somaUI
	if su.view == "" {
		su.view = "home"
	}
	// Soma's own colours while its pages draw (styles.css): the page, cards, text, accent.
	if apDark {
		saved := [...]color.RGBA{apBG, apCard, apCard2, apLabel, apSecondary, apBlue, apSeparator}
		apBG, apCard, apCard2, apLabel, apSecondary, apBlue, apSeparator = smBG, smSurface, smSurf3, smFG, smMuted, smPink, rgb(0x2a2f3d)
		defer func() {
			apBG, apCard, apCard2, apLabel, apSecondary, apBlue, apSeparator = saved[0], saved[1], saved[2], saved[3], saved[4], saved[5], saved[6]
		}()
	}
	soma.load()
	soma.mu.Lock()
	soma.changed = func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.mode == modeSoma && !c.somaUI.typing {
			c.redrawSoon(func() {
				if c.mode == modeSoma {
					c.showPage()
				}
			})
		}
	}
	soma.mu.Unlock()

	// A first guess at the page's height; a page that turns out taller is drawn again at the
	// height it needed (remembered, so it's drawn once from then on).
	h := map[string]int{"home": 4200, "today": 2600, "calendar": 4200, "goals": 2400, "notes": 2400, "money": 2800, "reports": 3400}[su.view]
	if su.need == nil {
		su.need = map[string]int{}
	}
	h = max(h, su.need[su.view])
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img, header: tabsH}
	c.booksTabs(p, "tab:soma")
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	pill := func(id, label string, r image.Rectangle, primary bool) {
		bg, fg := apCard2, apBlue
		if primary {
			bg, fg = apBlue, rgb(0xffffff)
		}
		ui.RoundRect(img, r, r.Dy()/2, bg)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, label)
		btn(id, r)
	}
	field := func(id, label, value string, y int) {
		r := image.Rect(mx, y, w-mx, y+96)
		smCard(img, r, smSurface)
		on := su.typing && su.field == id
		text, col := value, apLabel
		if text == "" && !on {
			text, col = label, apSecondary
		}
		if on {
			text += "|"
		}
		apText(img, f.body, r.Min.X+30, r.Min.Y+60, col, clip(f.body, text, r.Dx()-60))
		btn("so:type:"+id, r)
	}

	if !soma.linked() {
		apText(img, f.serifLarge, mx, 228, apLabel, "Soma")
		y := 300
		how := "Link the tablet to your Soma: in Soma on the phone or the PC, open Settings > Sync, and type its server address and recovery code here. The tablet then keeps your habits, to-dos, plans and reports in step with them."
		if a := sendAddress(); a != "" {
			how = "Link the tablet to your Soma. Easiest: on your phone, open Soma > Settings > Sync, copy the server address and the recovery code, then open http://" + a + " in the phone's browser and paste them under Link Soma. Or type them here."
		}
		drawParagraphs(img, f.callout, how, mx, y, tw, 44, y+300, apSecondary)
		y += 240
		url := su.url
		if su.typing && su.field == "url" {
			url = su.editing
		}
		field("url", "Sync address (https://...workers.dev)", url, y)
		code := ""
		if su.typing && su.field == "code" {
			code = su.editing
		}
		field("code", "Recovery code (56 characters)", code, y+120)
		pill("so:link", "Link", image.Rect(mx, y+260, w-mx, y+356), true)
		if su.msg != "" {
			drawParagraphs(img, f.callout, su.msg, mx, y+420, tw, 44, y+560, apRed)
		}
		if a := sendAddress(); a != "" {
			// Scanned with the phone's camera: opens the page where the address and code are pasted.
			const size = 440
			x0 := (w - size) / 2
			drawQR(img, "http://"+a+"/#soma", image.Rect(x0, y+600, x0+size, y+600+size))
			apText(img, f.callout, mx, y+600+size+60, apSecondary, "Scan with the iPhone camera, then paste the address and code.")
		}
		return p
	}

	y := 300
	if su.view == "home" {
		y = c.somaHome(p, tabsH)
	} else {
		// The part's own title, as Soma's pages have; the dock below moves between them.
		ui.Fill(img, image.Rect(0, tabsH, w, 300), smBG)
		ui.Fill(img, img.Rect.Intersect(image.Rect(0, 300, w, img.Rect.Dy())), smBG)
		titles := map[string]string{"today": "Fuel", "calendar": "Calendar", "goals": "Projects", "notes": "Mind", "money": "Money", "reports": "Train"}
		ui.Fill(img, image.Rect(0, tabsH, w, 300), smBG)
		apText(img, smFont("bold", 30), mx, 240, smFG, titles[su.view])
		apText(img, f.caption, mx, 290, smFaint, time.Now().In(displayZone).Format("Monday 2 January")+"  ·  "+soma.status())
		apText(img, f.body, w-mx-150, 240, apBlue, "Sync now")
		btn("so:sync", image.Rect(w-mx-180, 190, w, 270))
		if su.msg != "" {
			apText(img, f.caption, mx, 340, apRed, clip(f.caption, su.msg, tw))
		}
		y = 360
	}
	switch su.view {
	case "calendar":
		y = c.somaCalendar(p, y)
	case "reports":
		y = c.somaReports(p, y)
	case "goals":
		y = c.somaGoals(p, y)
	case "notes":
		y = c.somaNotesView(p, y)
	case "money":
		y = c.somaMoney(p, y)
	case "home":
	default:
		y = c.somaToday(p, y)
	}
	end := y + 40 + smDockH // the dock, fixed at the bottom, takes the page's last rows
	if end > img.Rect.Dy() && su.need[su.view] < end {
		su.need[su.view] = end + 400 // room to grow a little before the next redraw
		return c.somaPage()
	}
	if end < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		// Shorter than drawn: crop the page so it doesn't scroll into nothing.
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
	}
	c.smDock(p)
	return p
}

// section draws a section title; returns the y under it.
func somaSection(img *image.RGBA, x, y int, title, note string) int {
	f := apple()
	apText(img, f.title, x, y+50, apLabel, title)
	if note != "" {
		apText(img, f.caption, x+ui.TextWidth(f.title, title)+24, y+50, apSecondary, note)
	}
	return y + 80
}

// somaTodoRows draws to-dos as rows to tick.
func (c *console) somaTodoRows(p *page, items []somaTodo, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	for _, t := range items {
		r := image.Rect(mx, y, w-mx, y+92)
		smCard(img, r, smSurface)
		cx, cy := r.Min.X+50, (r.Min.Y+r.Max.Y)/2
		ui.Circle(img, cx, cy, 22, apBlue)
		if t.Done {
			iconCheck(img, cx-13, cy, rgb(0xffffff))
		} else {
			ui.Circle(img, cx, cy, 18, apCard)
		}
		col := apLabel
		if t.Done {
			col = apSecondary
		}
		text := t.Text
		if t.SlotDate != "" {
			text = clockOf(float64(t.SlotStart)/60) + "  " + text
		}
		apText(img, f.body, r.Min.X+96, cy+14, col, clip(f.body, text, r.Dx()-230))
		// Priority: ! high, · low; tap to change.
		pr := image.Rect(r.Max.X-110, r.Min.Y, r.Max.X, r.Max.Y)
		switch t.Priority {
		case "high":
			apTextCenter(img, f.title, (pr.Min.X+pr.Max.X)/2, cy, apRed, "!")
		case "low":
			apTextCenter(img, f.caption, (pr.Min.X+pr.Max.X)/2, cy, apSecondary, "low")
		default: // no priority yet: a faint mark, so there's something to tap
			ui.RoundRect(img, image.Rect((pr.Min.X+pr.Max.X)/2-22, cy-22, (pr.Min.X+pr.Max.X)/2+22, cy+22), 22, apCard2)
			apTextCenter(img, f.caption, (pr.Min.X+pr.Max.X)/2, cy, blend(apSecondary, apCard, 0.3), "!")
		}
		p.buttons = append(p.buttons, button{"so:todo:" + t.ID, image.Rect(r.Min.X, r.Min.Y, pr.Min.X, r.Max.Y)}, button{"so:prio:" + t.ID, pr})
		if c.somaUI.todoMenu == t.ID { // held: its actions over the row
			ui.RoundRect(img, r, radiusCard, apCard2)
			acts := []struct{ id, label string }{{"tomorrow", "Tomorrow"}, {"priority", "Priority"}, {"delete", "Delete"}, {"close", "Cancel"}}
			aw := r.Dx() / len(acts)
			for i, a := range acts {
				ar := image.Rect(r.Min.X+i*aw, r.Min.Y, r.Min.X+(i+1)*aw, r.Max.Y)
				col := apBlue
				if a.id == "delete" {
					col = apRed
				}
				apTextCenter(img, f.headline, (ar.Min.X+ar.Max.X)/2, (ar.Min.Y+ar.Max.Y)/2, col, a.label)
				p.buttons = append(p.buttons, button{"so:tdmenu:" + a.id + ":" + t.ID, ar})
			}
		}
		y += 104
	}
	return y
}

func (c *console) somaAddRow(p *page, id, label string, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	su := &c.somaUI
	r := image.Rect(mx, y, w-mx, y+92)
	ui.RoundRect(img, r, radiusCard, apCard2)
	text, col := "+  "+label, apBlue
	if su.typing && su.field == id {
		text, col = withCursor(su.editing, c.fieldBack), apLabel
	}
	apText(img, f.body, r.Min.X+34, (r.Min.Y+r.Max.Y)/2+14, col, clip(f.body, text, r.Dx()-60))
	p.buttons = append(p.buttons, button{"so:type:" + id, r})
	return y + 104
}

// somaTap handles the Soma tab's buttons. Caller holds drawMu.
func (c *console) somaTap(id string) bool {
	if !strings.HasPrefix(id, "so:") {
		return false
	}
	su := &c.somaUI
	su.msg = ""
	field := strings.TrimPrefix(id, "so:type:")
	if at, ok := strings.CutPrefix(id, "so:at:"); ok {
		field = "at:" + at
	}
	if su.typing && field != su.field {
		c.somaDone() // a tap elsewhere: what was typed is kept
	}
	switch {
	case strings.HasPrefix(id, "so:view:"):
		su.view = strings.TrimPrefix(id, "so:view:")
		c.setScroll(modeSoma, 0)
	case id == "so:sync":
		go soma.sync()
	case strings.HasPrefix(id, "so:habit:"):
		if err := somaToggleHabit(strings.TrimPrefix(id, "so:habit:")); err != nil {
			su.msg = err.Error()
		}
	case strings.HasPrefix(id, "so:goal:"): // so:goal:<id>:<delta>
		g, d, _ := strings.Cut(strings.TrimPrefix(id, "so:goal:"), ":")
		delta, _ := strconv.ParseFloat(d, 64)
		somaGoalTap(g, delta)
	case id == "so:sleep":
		somaGoToSleep()
		go soma.sync()
		c.setScreen(false)
		return true
	case id == "so:wake":
		if msg := somaWakeUp(); msg != "" {
			c.toastMsg(msg)
		}
		go soma.sync()
	case strings.HasPrefix(id, "so:note:"):
		su.note = strings.TrimPrefix(id, "so:note:")
		c.setScroll(modeSoma, 0)
	case id == "so:notes":
		su.note = ""
		c.setScroll(modeSoma, 0)
	case strings.HasPrefix(id, "so:tick:"): // so:tick:<line>
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "so:tick:"))
		somaNoteToggle(su.note, i)
	case strings.HasPrefix(id, "so:step:"): // so:step:<project>:<step>
		pj, st, _ := strings.Cut(strings.TrimPrefix(id, "so:step:"), ":")
		somaStepTap(pj, st)
	case id == "so:plan":
		su.plan++
	case strings.HasPrefix(id, "so:delshift:"):
		c.somaDelShiftTap(strings.TrimPrefix(id, "so:delshift:"))
	case strings.HasPrefix(id, "so:rep:"):
		su.repRange = strings.TrimPrefix(id, "so:rep:")
	case id == "so:cal:week", id == "so:cal:month":
		su.calWeek = id == "so:cal:week"
	case strings.HasPrefix(id, "so:wk:"):
		d, _ := strconv.Atoi(strings.TrimPrefix(id, "so:wk:"))
		t, _ := time.ParseInLocation("2006-01-02", su.sel, displayZone)
		su.sel = t.AddDate(0, 0, 7*d).Format("2006-01-02")
	case strings.HasPrefix(id, "so:move:"):
		if m := strings.TrimPrefix(id, "so:move:"); su.moving == m {
			su.moving = ""
		} else {
			su.moving = m
		}
	case strings.HasPrefix(id, "so:moveto:"):
		m, _ := strconv.Atoi(strings.TrimPrefix(id, "so:moveto:"))
		somaMoveTodo(su.moving, su.sel, m)
		su.moving = ""
	case strings.HasPrefix(id, "so:ringsday:"):
		c.ringsDay = strings.TrimPrefix(id, "so:ringsday:")
		if c.ringsDay == somaToday() {
			c.ringsDay = ""
		}
	case strings.HasPrefix(id, "so:tdmenu:"): // so:tdmenu:<action>:<id>
		act, tid, _ := strings.Cut(strings.TrimPrefix(id, "so:tdmenu:"), ":")
		switch act {
		case "tomorrow":
			somaMoveTodoTomorrow(tid)
		case "priority":
			somaTodoPriority(tid)
		case "delete":
			somaDeleteTodo(tid)
			c.toastMsg("To-do deleted")
		}
		su.todoMenu = ""
	case id == "so:todoscope:day", id == "so:todoscope:week":
		su.todoWeek = id == "so:todoscope:week"
	case id == "so:creatine":
		somaAddCreatine(5)
		c.toastMsg("+5 g creatine")
	case id == "so:logsleep":
		text := su.sleepDraft
		if su.typing && su.field == "sleep" {
			text = su.editing
			su.typing, c.skb.visible = false, false
		}
		if msg := somaLogSleepHours(text); msg != "" {
			c.toastMsg(msg)
		}
		su.sleepDraft = ""
	case strings.HasPrefix(id, "so:water:"):
		ml, _ := strconv.Atoi(strings.TrimPrefix(id, "so:water:"))
		somaAddWater(float64(ml))
	case strings.HasPrefix(id, "so:amt:"): // so:amt:<habit>:<delta>
		h, d, _ := strings.Cut(strings.TrimPrefix(id, "so:amt:"), ":")
		delta, _ := strconv.ParseFloat(d, 64)
		somaLogAmount(h, delta)
	case strings.HasPrefix(id, "so:when:"):
		somaHabitWhen(strings.TrimPrefix(id, "so:when:"))
	case strings.HasPrefix(id, "so:prio:"):
		somaTodoPriority(strings.TrimPrefix(id, "so:prio:"))
	case id == "so:tomorrow":
		if n := somaMoveToTomorrow(); n > 0 {
			c.toastMsg(fmt.Sprintf("Moved %d to tomorrow", n))
		}
	case strings.HasPrefix(id, "so:todo:"):
		somaToggleTodo(strings.TrimPrefix(id, "so:todo:"))
	case strings.HasPrefix(id, "so:read:"):
		m, _ := strconv.Atoi(strings.TrimPrefix(id, "so:read:"))
		somaAddReading(somaToday(), m)
	case strings.HasPrefix(id, "so:month:"):
		d, _ := strconv.Atoi(strings.TrimPrefix(id, "so:month:"))
		su.month = su.month.AddDate(0, d, 0)
	case strings.HasPrefix(id, "so:day:"):
		su.sel = strings.TrimPrefix(id, "so:day:")
	case strings.HasPrefix(id, "so:at:"):
		if !su.typing {
			su.typing, su.field, su.editing, c.fieldBack = true, field, "", 0
		}
		c.skb.visible = true
		c.showPage()
		// Keep the slot being typed in above the keyboard.
		if sy := c.barH + su.slotY - c.scrollY(); sy > c.skb.y0-30 {
			c.setScroll(modeSoma, c.scrollY()+sy-(c.skb.y0-30))
		}
	case strings.HasPrefix(id, "so:type:"):
		f := strings.TrimPrefix(id, "so:type:")
		if !su.typing || su.field != f {
			su.typing, su.field, su.editing, c.fieldBack = true, f, "", 0
			if f == "url" {
				su.editing = su.url
			}
		}
		c.skb.visible = true
	case id == "so:link": // what was being typed was just tried (somaDone)
		if !soma.linked() && su.msg == "" {
			su.msg = "Type the sync address, then the recovery code."
		}
	}
	c.showPage()
	return true
}

// somaKey takes the keyboard's output while typing in Soma. Caller holds drawMu.
func (c *console) somaKey(b []byte) {
	su := &c.somaUI
	if len(b) == 0 {
		return
	}
	var done bool
	if su.editing, done = editLine(su.editing, &c.fieldBack, b, 200); done {
		c.somaDone()
	}
	c.fieldTyped()
}

// somaDone finishes what was being typed: a to-do is added, the link is tried.
func (c *console) somaDone() {
	su := &c.somaUI
	text := strings.TrimSpace(su.editing)
	su.typing, su.editing = false, ""
	c.skb.visible = false
	switch su.field {
	case "sleep":
		if msg := somaLogSleepHours(text); msg != "" {
			c.toastMsg(msg)
		}
	case "food":
		if text != "" && !somaAddFood(text) {
			su.msg = "Type the food, then its calories (and protein): Wrap 450 35"
		}
	case "workout":
		if text != "" && !somaLogWorkout(text) {
			su.msg = "Type the split, then the minutes: Push 60"
		}
	case "todo":
		somaAddTodo(text, "day", somaToday())
	case "week":
		somaAddTodo(text, "week", somaToday())
	case "day":
		somaAddTodo(text, "day", su.sel)
	case "url":
		su.url = text
		if text != "" { // the code next
			su.typing, su.field = true, "code"
			c.skb.visible = true
		}
	case "code":
		if err := soma.setLink(su.url, text); err != nil {
			su.msg = err.Error()
		} else {
			su.msg = ""
			go soma.sync()
		}
	default:
		if pj, ok := strings.CutPrefix(su.field, "step:"); ok {
			somaAddStep(pj, text)
		}
		switch su.field {
		case "noteline":
			somaNoteAppend(su.note, text)
		case "notetask":
			if text != "" {
				somaNoteAppend(su.note, "- [ ] "+text)
			}
		}
		if su.field == "newnote" {
			if id := somaNewNote(text); id != "" {
				su.note = id
			}
		}
		if at, ok := strings.CutPrefix(su.field, "at:"); ok {
			m, _ := strconv.Atoi(at)
			somaAddTodoAt(text, su.sel, m, 30)
		}
	}
}

// drawQR draws text as a QR code filling r (a square), black on white with a quiet border.
func drawQR(img *image.RGBA, text string, r image.Rectangle) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return
	}
	n := code.Size + 8 // 4 modules of white all round
	m := r.Dx() / n
	if m < 1 {
		return
	}
	draw.Draw(img, r, image.White, image.Point{}, draw.Src)
	o := r.Min.Add(image.Pt((r.Dx()-code.Size*m)/2, (r.Dy()-code.Size*m)/2))
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				draw.Draw(img, image.Rect(o.X+x*m, o.Y+y*m, o.X+(x+1)*m, o.Y+(y+1)*m), image.Black, image.Point{}, draw.Src)
			}
		}
	}
}

// somaTodoMenu shows a held to-do's actions (tomorrow, priority, delete). Caller holds drawMu.
func (c *console) somaTodoMenu(id string) {
	c.somaUI.todoMenu = id
	c.showPage()
}
