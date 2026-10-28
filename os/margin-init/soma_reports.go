package main

import (
	"fmt"
	"image"
	"image/color"
	"margin-init/ui"
	"strconv"
	"strings"
	"time"
)

func (c *console) somaReports(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	su := &c.somaUI
	if su.repRange == "" {
		su.repRange = "week"
	}
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	seg := image.Rect(w/2-240, y+10, w/2+240, y+76)
	ui.RoundRect(img, seg, 18, apCard2)
	for i, m := range []string{"Week", "Month", "Year"} {
		r := image.Rect(seg.Min.X+i*160, seg.Min.Y, seg.Min.X+(i+1)*160, seg.Max.Y)
		if strings.ToLower(m) == su.repRange {
			ui.RoundRect(img, r.Inset(5), 14, apCard)
		}
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, m)
		btn("so:rep:"+strings.ToLower(m), r)
	}
	y += 110

	// The buckets: days for a week or a month, months for a year.
	date := somaToday()
	type bucket struct {
		label string
		days  []string
	}
	var bs []bucket
	switch su.repRange {
	case "year":
		t, _ := time.Parse("2006-01-02", date)
		for m := 11; m >= 0; m-- {
			first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -m, 0)
			b := bucket{label: first.Format("Jan")[:1]}
			for d := first; d.Month() == first.Month() && d.Format("2006-01-02") <= date; d = d.AddDate(0, 0, 1) {
				b.days = append(b.days, d.Format("2006-01-02"))
			}
			bs = append(bs, b)
		}
	default:
		n := 7
		if su.repRange == "month" {
			n = 30
		}
		for _, d := range days(date, n) {
			t, _ := time.Parse("2006-01-02", d)
			l := t.Format("Mon")[:1]
			if n == 30 {
				l = ""
				if t.Day()%5 == 0 {
					l = strconv.Itoa(t.Day())
				}
			}
			bs = append(bs, bucket{l, []string{d}})
		}
	}
	// avg is a bucket's average over the days that have something; sum adds them up.
	avg := func(b bucket, v func(string) (float64, bool)) float64 {
		t, n := 0.0, 0
		for _, d := range b.days {
			if x, ok := v(d); ok {
				t += x
				n++
			}
		}
		if n == 0 {
			return 0
		}
		return t / float64(n)
	}
	sum := func(b bucket, v func(string) (float64, bool)) float64 {
		t := 0.0
		for _, d := range b.days {
			if x, ok := v(d); ok {
				t += x
			}
		}
		return t
	}
	chart := func(title, note string, vals []float64, goal float64, col color.RGBA) {
		y = somaSection(img, mx, y, title, note)
		r := image.Rect(mx, y, w-mx, y+300)
		ui.RoundRect(img, r, radiusCard, apCard)
		top := goal
		for _, v := range vals {
			top = max(top, v)
		}
		if top <= 0 {
			top = 1
		}
		plot := image.Rect(r.Min.X+30, r.Min.Y+30, r.Max.X-30, r.Max.Y-60)
		if goal > 0 {
			gy := plot.Max.Y - int(float64(plot.Dy())*goal/top)
			for x := plot.Min.X; x < plot.Max.X; x += 16 {
				ui.Fill(img, image.Rect(x, gy, x+8, gy+2), apSecondary)
			}
		}
		bw := plot.Dx() / max(len(vals), 1)
		for i, v := range vals {
			bh := int(float64(plot.Dy()) * v / top)
			x := plot.Min.X + i*bw
			if bh > 0 {
				ui.RoundRect(img, image.Rect(x+bw/6, plot.Max.Y-max(bh, 6), x+bw-bw/6, plot.Max.Y), min(bw/4, 10), col)
			}
			apTextCenter(img, f.caption, x+bw/2, r.Max.Y-30, apSecondary, bs[i].label)
		}
		y += 330
	}
	series := func(v func(string) (float64, bool), useSum bool) []float64 {
		out := make([]float64, len(bs))
		for i, b := range bs {
			if useSum && su.repRange == "year" {
				out[i] = sum(b, v)
			} else {
				out[i] = avg(b, v)
			}
		}
		return out
	}
	mean := func(vals []float64) float64 {
		t, n := 0.0, 0
		for _, v := range vals {
			if v > 0 {
				t += v
				n++
			}
		}
		if n == 0 {
			return 0
		}
		return t / float64(n)
	}
	per := map[string]string{"week": "a day", "month": "a day", "year": "a day, by month"}[su.repRange]

	cals := series(func(d string) (float64, bool) { n := somaNutrition(d); return n.Cals, n.Logged }, false)
	goalCals := somaNutrition(date).GoalCals
	if goalCals == 0 {
		goalCals = 2400
	}
	chart("Calories", fmt.Sprintf("%.0f kcal %s on average", mean(cals), per), cals, goalCals, rgb(0xfa114f))
	prot := series(func(d string) (float64, bool) { n := somaNutrition(d); return n.P, n.Logged }, false)
	chart("Protein", fmt.Sprintf("%.0f g %s on average", mean(prot), per), prot, max(somaNutrition(date).GoalP, 160), rgb(0x7dff00))

	sessions := 0
	trainVol := series(func(d string) (float64, bool) {
		if s := somaTraining(d); s != nil {
			sessions++
			return s.Volume, true
		}
		return 0, false
	}, true)
	chart("Training", fmt.Sprintf("%d session%s  ·  volume in kg", sessions, map[bool]string{true: "", false: "s"}[sessions == 1]), trainVol, 0, rgb(0xff9f0a))

	habits := somaHabits()
	habitPct := series(func(d string) (float64, bool) {
		if len(habits) == 0 {
			return 0, false
		}
		n := 0
		for _, h := range habits {
			if h.done(d) {
				n++
			}
		}
		return 100 * float64(n) / float64(len(habits)), true
	}, false)
	chart("Habits", fmt.Sprintf("%.0f%% done on average", mean(habitPct)), habitPct, 100, rgb(0x30d158))

	read := series(func(d string) (float64, bool) { m := somaReading(d); return float64(m), m > 0 }, true)
	total := 0.0
	for _, v := range read {
		total += v
	}
	chart("Reading", fmt.Sprintf("%.0f min in all", total), read, 0, apBlue)

	sleep := series(func(d string) (float64, bool) {
		o := somaObj("nutrition/" + d)
		if sl, ok := o["sleep"].(obj); ok && num(sl["hours"]) > 0 {
			return num(sl["hours"]), true
		}
		return 0, false
	}, false)
	chart("Sleep", fmt.Sprintf("%.1f h a night on average", mean(sleep)), sleep, 8, rgb(0x5e5ce6))
	return y
}
