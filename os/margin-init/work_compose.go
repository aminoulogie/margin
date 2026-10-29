package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"margin-init/ui"
)

// The task composer: adding a to-do on the workstation types into a card just above the
// keyboard, as Things or Reminders on the iPad do: the title in a big field, and under it
// what the to-do is for and when: the day (today, tomorrow, the weekend, any day), a time
// and how long, and its priority. Tapping those doesn't stop the typing; return or Add adds
// the to-do with all of it.

const composerH = 300

// taskComposer is what the composer has chosen, beside the title being typed.
type taskComposer struct {
	date  string // the day ("" none: the week's list)
	week  bool   // on the week's own list
	start int    // minutes from midnight, -1: no time
	mins  int
	prio  string // "high", "low", ""
}

// composing: the field being typed is a new to-do's title.
func (c *console) composing() bool {
	su := &c.somaUI
	if c.mode != modeSoma || !su.typing {
		return false
	}
	f := strings.TrimPrefix(su.field, "w.")
	return strings.HasPrefix(f, "newtask:") || strings.HasPrefix(f, "slotadd:") || strings.HasPrefix(f, "newweek:")
}

// typeBarHeight is the height of what's drawn above the keyboard while typing.
func (c *console) typeBarHeight() int {
	if c.composing() {
		return composerH
	}
	return typeBarH
}

// cmpFromField is the composer for a new to-do's field: "newtask:<date>" (no time),
// "slotadd:<date>:<minute>" (an hour from then), "newweek:<monday>" (the week's list).
func cmpFromField(field string) taskComposer {
	name, arg, _ := strings.Cut(field, ":")
	cm := taskComposer{date: somaToday(), start: -1, mins: 60}
	switch name {
	case "newtask":
		if arg != "" {
			cm.date = arg
		}
	case "slotadd":
		date, m, _ := strings.Cut(arg, ":")
		cm.date = date
		fmt.Sscan(m, &cm.start)
	case "newweek":
		cm.week = true
		if mondayOf(cm.date) != arg && arg != "" {
			cm.date = arg
		}
	}
	return cm
}

type cmpBtn struct {
	id string
	r  image.Rectangle // screen
}

// composerLayout draws the composer into img (screen rows y0 on) and says where its buttons are.
func (c *console) composerLayout(img *image.RGBA, y0 int) []cmpBtn {
	cm := &c.somaUI.cmp
	var btns []cmpBtn
	W := c.s.W
	x0 := 24
	if c.page != nil && c.page.left > 0 {
		x0 = c.page.left + 16
	}
	R := func(a, b, cc, d int) image.Rectangle { return image.Rect(a, y0+b, cc, y0+d) }
	ui.Fill(img, img.Rect, wkBG)
	ui.Fill(img, R(0, 0, W, 1), wkLine)
	card := R(x0, 14, W-24, composerH-12)
	ui.RoundRect(img, card, 22, wkRim)
	ui.RoundRect(img, card.Inset(2), 20, wkCard)

	// The title, with Add at its end.
	text := c.somaUI.editing
	ft := wkFont("semi", 30)
	tx := card.Min.X + 74
	ui.Circle(img, card.Min.X+40, card.Min.Y+50, 15, wkRim)
	ui.Circle(img, card.Min.X+40, card.Min.Y+50, 13, wkCard)
	add := image.Rect(card.Max.X-150, card.Min.Y+18, card.Max.X-18, card.Min.Y+82)
	addOn := strings.TrimSpace(text) != ""
	if addOn {
		ui.RoundRect(img, add, 32, wkLime)
	} else {
		ui.RoundRect(img, add, 32, blend(wkCard, wkLime, 0.18))
	}
	lucideIcon(img, "arrow-up", add.Min.X+36, (add.Min.Y+add.Max.Y)/2, 26, map[bool]color.RGBA{true: wkOnLime, false: wkMuted}[addOn])
	apText(img, wkFont("semi", 22), add.Min.X+58, (add.Min.Y+add.Max.Y)/2+8, map[bool]color.RGBA{true: wkOnLime, false: wkMuted}[addOn], "Add")
	btns = append(btns, cmpBtn{"add", add})
	room := add.Min.X - 20 - tx
	if text == "" {
		apText(img, ft, tx, card.Min.Y+61, wkLime, "|")
		apText(img, ft, tx+14, card.Min.Y+61, wkFaint, "What needs doing?")
	} else {
		s := withCursor(text, c.fieldBack)
		for s != "" && ui.TextWidth(ft, s) > room {
			s = string([]rune(s)[1:])
		}
		apText(img, ft, tx, card.Min.Y+61, wkFG, s)
	}
	ui.Fill(img, image.Rect(card.Min.X+24, card.Min.Y+100, card.Max.X-24, card.Min.Y+102), wkLine)

	// A chip: icon, label, chosen or not. Returns its right edge.
	f := wkFont("medium", 20)
	chip := func(id, icon, label string, x, y int, on bool) int {
		w := ui.TextWidth(f, label) + 40
		if icon != "" {
			w += 30
		}
		r := image.Rect(x, y, x+w, y+52)
		bg, fg, ic := blend(wkCard, rgb(0xffffff), 0.06), wkText2, wkMuted
		if on {
			bg, fg, ic = blend(wkCard, wkLime, 0.2), wkLime, wkLime
		}
		ui.RoundRect(img, r, 26, bg)
		lx := x + 20
		if icon != "" {
			lucideIcon(img, icon, x+30, y+26, 22, ic)
			lx = x + 50
		}
		apText(img, f, lx, y+34, fg, label)
		btns = append(btns, cmpBtn{id, r})
		return r.Max.X + 10
	}
	// The day.
	today := somaToday()
	tomorrow := wkAddDays(today, 1)
	sat := today
	for t, _ := time.ParseInLocation("2006-01-02", today, displayZone); t.Weekday() != time.Saturday; t = t.AddDate(0, 0, 1) {
		sat = wkAddDays(t.Format("2006-01-02"), 1)
	}
	y := card.Min.Y + 118
	x := card.Min.X + 24
	x = chip("day:"+today, "sun", "Today", x, y, !cm.week && cm.date == today)
	x = chip("day:"+tomorrow, "sunrise", "Tomorrow", x, y, !cm.week && cm.date == tomorrow)
	if sat != today && sat != tomorrow {
		x = chip("day:"+sat, "sofa", "Weekend", x, y, !cm.week && cm.date == sat)
	}
	x = chip("week", "calendar-range", "This week", x, y, cm.week)
	other := cm.date != "" && cm.date != today && cm.date != tomorrow && cm.date != sat && !cm.week
	label := "Pick a day"
	if other {
		label = wkDate(cm.date)
	}
	chip("pick", "calendar", label, x, y, other)

	// The time and its length, and the priority.
	y += 70
	x = card.Min.X + 24
	if cm.start < 0 || cm.week {
		x = chip("time:on", "clock", "Add a time", x, y, false)
	} else {
		step := func(id, icon string, x int) int {
			r := image.Rect(x, y, x+52, y+52)
			ui.RoundRect(img, r, 26, blend(wkCard, rgb(0xffffff), 0.06))
			lucideIcon(img, icon, x+26, y+26, 22, wkFG)
			btns = append(btns, cmpBtn{id, r})
			return r.Max.X + 6
		}
		x = step("start:-15", "minus", x)
		x = chip("time:off", "clock", clockOf(float64(cm.start)/60), x, y, true) - 4
		x = step("start:15", "plus", x) + 16
		x = step("mins:-15", "minus", x)
		x = chip("mins", "timer", minsLabel(cm.mins), x, y, true) - 4
		x = step("mins:15", "plus", x) + 16
	}
	pl := map[string]string{"": "Priority", "high": "High", "low": "Low"}[cm.prio]
	chip("prio", "flag", pl, x, y, cm.prio != "")
	return btns
}

// drawComposer draws the composer onto the screen, above the keyboard (the caller flushes).
func (c *console) drawComposer() {
	y0 := c.skb.y0 - composerH
	img := image.NewRGBA(image.Rect(0, y0, c.s.W, c.skb.y0))
	c.composerLayout(img, y0)
	c.s.blitRGBA(img, 0, y0)
}

// composerTap handles a finger landing on the composer. True if it was on it. Caller
// holds drawMu.
func (c *console) composerTap(x, y int) bool {
	if !c.composing() || !c.skb.visible || y < c.skb.y0-composerH || y >= c.skb.y0 {
		return false
	}
	img := image.NewRGBA(image.Rect(0, c.skb.y0-composerH, c.s.W, c.skb.y0))
	var hit string
	for _, b := range c.composerLayout(img, c.skb.y0-composerH) {
		if (image.Point{x, y}).In(b.r.Inset(-6)) {
			hit = b.id
		}
	}
	cm := &c.somaUI.cmp
	what, arg, _ := strings.Cut(hit, ":")
	switch what {
	case "":
		return true // the composer itself: the typing goes on
	case "add":
		c.somaDone()
		c.showPage()
		return true
	case "day":
		cm.date, cm.week = arg, false
	case "week":
		cm.week = !cm.week
		if cm.week {
			cm.date = somaToday()
		}
	case "pick": // the date sheet, then back to typing
		su := &c.somaUI
		su.cmpPicking, su.cmpField, su.cmpText = true, strings.TrimPrefix(su.field, "w."), su.editing
		su.typing, su.editing, su.prompt = false, "", "" // not done: put aside for the sheet
		c.skb.visible = false
		c.wkPickDate("cmpday:", cm.date, "Which day")
		c.showPage()
		return true
	case "time":
		if arg == "on" {
			cm.week = false
			if cm.date == "" {
				cm.date = somaToday()
			}
			now := time.Now().In(displayZone)
			cm.start = 9 * 60
			if cm.date == somaToday() {
				cm.start = min(max((now.Hour()+1)*60, 7*60), 22*60)
			}
		} else {
			cm.start = -1
		}
	case "start":
		var d int
		fmt.Sscan(arg, &d)
		cm.start = min(max(cm.start+d, 0), 24*60-15)
	case "mins":
		var d int
		fmt.Sscan(arg, &d)
		if d == 0 { // the length itself: the usual ones in turn
			next := map[int]int{15: 30, 30: 45, 45: 60, 60: 90, 90: 120, 120: 15}
			if n, ok := next[cm.mins]; ok {
				cm.mins = n
			} else {
				cm.mins = 60
			}
		} else {
			cm.mins = min(max(cm.mins+d, 15), 12*60)
		}
	case "prio":
		cm.prio = map[string]string{"": "high", "high": "low", "low": ""}[cm.prio]
	}
	c.drawComposer()
	c.s.Flush()
	return true
}

// composerAdd adds the to-do the composer has, titled text.
func (c *console) composerAdd(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	cm := c.somaUI.cmp
	date := cm.date
	if date == "" {
		date = somaToday()
	}
	switch {
	case cm.week:
		somaAddTodo(text, "week", date)
	case cm.start >= 0:
		somaAddTodoAt(text, date, cm.start, cm.mins)
	default:
		somaAddTodo(text, "day", date)
	}
	if cm.prio != "" {
		ts := somaTodos()
		for i := len(ts) - 1; i >= 0; i-- {
			if ts[i].Text == text {
				id := ts[i].ID
				somaPatch("todos", id, func(o obj) { o["priority"] = cm.prio })
				break
			}
		}
	}
	when := wkDate(date)
	if cm.week {
		when = "this week"
	} else if cm.start >= 0 {
		when += " " + clockOf(float64(cm.start)/60)
	}
	c.toastMsg("Added · " + when)
}

func init() {
	// The composer's own day sheet: back to the typing after.
	wkDateSetters["cmpday"] = func(c *console, _, date string) {
		if date != "" {
			c.somaUI.cmp.date, c.somaUI.cmp.week = date, false
		}
		su := &c.somaUI
		su.cmpPicking, su.sheet, su.sheetStack, su.cmpKeep = false, "", nil, true
		c.wkType(su.cmpField, "New to-do", su.cmpText)
	}
}
