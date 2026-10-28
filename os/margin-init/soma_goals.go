package main

import (
	"fmt"
	"image"
	"margin-init/ui"
	"strconv"
	"strings"
	"time"
)

// somaGoals is the week's, month's and year's goals, then the active projects with their steps.
func (c *console) somaGoals(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	now := time.Now().In(displayZone)
	_, wk := now.ISOWeek()
	for _, hz := range []struct{ id, title, note string }{
		{"week", "This Week", fmt.Sprintf("week %d", wk)},
		{"month", "This Month", now.Format("January")},
		{"year", "This Year", now.Format("2006")},
	} {
		goals := somaGoalsNow(hz.id)
		done := 0
		for _, g := range goals {
			if g.Done {
				done++
			}
		}
		y = somaSection(img, mx, y, hz.title, fmt.Sprintf("%s  ·  %d of %d done", hz.note, done, len(goals)))
		if len(goals) == 0 {
			apText(img, f.callout, mx, y+30, apSecondary, "No goals here yet: add them in Soma > Goals.")
			y += 70
		}
		for _, g := range goals {
			r := image.Rect(mx, y, w-mx, y+104)
			ui.RoundRect(img, r, radiusCard, apCard)
			col := somaColour(g.Color, apBlue)
			cx, cy := r.Min.X+56, (r.Min.Y+r.Max.Y)/2
			ui.Circle(img, cx, cy, 26, col)
			if g.Done {
				iconCheck(img, cx-14, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, cx, cy, 21, apCard)
			}
			right := r.Max.X - 30
			if g.Target > 0 { // counted: progress, a bar and - / + buttons
				minus := image.Rect(r.Max.X-230, r.Min.Y+18, r.Max.X-140, r.Max.Y-18)
				plus := image.Rect(r.Max.X-120, r.Min.Y+18, r.Max.X-30, r.Max.Y-18)
				for _, b := range []struct {
					r     image.Rectangle
					t, id string
				}{{minus, "-", ":-1"}, {plus, "+", ":1"}} {
					ui.RoundRect(img, b.r, 30, apCard2)
					apTextCenter(img, f.title, (b.r.Min.X+b.r.Max.X)/2, (b.r.Min.Y+b.r.Max.Y)/2, apBlue, b.t)
					btn("so:goal:"+g.ID+b.id, b.r)
				}
				label := fmt.Sprintf("%s / %s %s", strconv.FormatFloat(g.Progress, 'f', -1, 64), strconv.FormatFloat(g.Target, 'f', -1, 64), g.Unit)
				lw := ui.TextWidth(f.caption, label)
				apText(img, f.caption, r.Max.X-260-lw, cy+10, apSecondary, label)
				right = r.Max.X - 280 - lw
				bar := image.Rect(r.Min.X+110, r.Max.Y-18, right, r.Max.Y-10)
				ui.RoundRect(img, bar, 4, apCard2)
				if fill := int(float64(bar.Dx()) * min(1, g.Progress/g.Target)); fill > 8 {
					ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+fill, bar.Max.Y), 4, col)
				}
			} else {
				btn("so:goal:"+g.ID+":0", r)
			}
			tcol := apLabel
			if g.Done {
				tcol = apSecondary
			}
			apText(img, f.headline, r.Min.X+110, cy+8, tcol, clip(f.headline, g.Title, right-r.Min.X-120))
			y += 120
		}
		y += 30
	}

	projects := somaProjects()
	y = somaSection(img, mx, y, "Projects", fmt.Sprintf("%d active", len(projects)))
	if len(projects) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No active projects: add them in Soma > Projects.")
		y += 70
	}
	today := somaToday()
	for _, pj := range projects {
		col := somaColour(pj.Color, apBlue)
		head := image.Rect(mx, y, w-mx, y+130)
		ui.RoundRect(img, head, 22, apCard)
		apText(img, f.headline, head.Min.X+34, head.Min.Y+56, apLabel, clip(f.headline, pj.Name, head.Dx()-400))
		var sub []string
		if pj.Client != "" {
			sub = append(sub, pj.Client)
		}
		if pj.Due != "" {
			if t, err := time.Parse("2006-01-02", pj.Due); err == nil {
				t0, _ := time.Parse("2006-01-02", today)
				d := int(t.Sub(t0).Hours() / 24)
				switch {
				case d < 0:
					sub = append(sub, fmt.Sprintf("%d days late", -d))
				case d == 0:
					sub = append(sub, "due today")
				default:
					sub = append(sub, fmt.Sprintf("%d days left", d))
				}
			}
		}
		if pj.Priority != "" {
			sub = append(sub, pj.Priority+" priority")
		}
		apText(img, f.caption, head.Min.X+34, head.Min.Y+100, apSecondary, clip(f.caption, strings.Join(sub, "  ·  "), head.Dx()-400))
		n, done := len(pj.Steps), pj.done()
		count := fmt.Sprintf("%d / %d", done, n)
		apText(img, f.headline, head.Max.X-34-ui.TextWidth(f.headline, count), head.Min.Y+56, col, count)
		bar := image.Rect(head.Max.X-334, head.Min.Y+86, head.Max.X-34, head.Min.Y+96)
		ui.RoundRect(img, bar, 5, apCard2)
		if n > 0 && done > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+bar.Dx()*done/n, bar.Max.Y), 5, col)
		}
		y += 140
		for _, st := range pj.Steps {
			r := image.Rect(mx+40, y, w-mx, y+84)
			ui.RoundRect(img, r, 20, apCard)
			cx, cy := r.Min.X+44, (r.Min.Y+r.Max.Y)/2
			ui.Circle(img, cx, cy, 20, col)
			if st.Done {
				iconCheck(img, cx-12, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, cx, cy, 16, apCard)
			}
			tcol := apLabel
			if st.Done {
				tcol = apSecondary
			}
			apText(img, f.body, r.Min.X+86, cy+13, tcol, clip(f.body, st.Label, r.Dx()-120))
			btn("so:step:"+pj.ID+":"+st.ID, r)
			y += 94
		}
		y = c.somaAddRow(p, "step:"+pj.ID, "Add a step", y)
		y += 30
	}
	return y
}
