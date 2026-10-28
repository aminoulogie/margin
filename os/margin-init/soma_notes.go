package main

import (
	"fmt"
	"image"
	"margin-init/ui"
	"strings"
	"time"
)

// somaNotesView is the vault's notes, newest first, or the open note: its lines drawn as
// Markdown (headings, checkboxes to tick, bullets), with a line or a task to add at the end.
func (c *console) somaNotesView(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	tw := w - 2*mx
	su := &c.somaUI
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	if n := somaNoteByID(su.note); su.note != "" && n != nil {
		apText(img, f.body, mx, y+40, apBlue, "‹ All notes")
		btn("so:notes", image.Rect(0, y, mx+300, y+70))
		y += 90
		if n.folder() != "" {
			apText(img, f.caption, mx, y+30, apSecondary, n.folder())
			y += 44
		}
		inBlock := false
		for i, l := range strings.Split(n.Text, "\n") {
			if strings.Contains(l, "<!-- soma -->") {
				inBlock = true
				continue
			}
			if strings.Contains(l, "<!-- /soma -->") {
				inBlock = false
				continue
			}
			t := strings.TrimSpace(l)
			switch {
			case t == "":
				y += 24
			case strings.HasPrefix(t, "# "):
				y = drawParagraphs(img, f.serifLarge, strings.TrimPrefix(t, "# "), mx, y+20, tw, 80, y+400, apLabel) + 10
			case strings.HasPrefix(t, "## "), strings.HasPrefix(t, "### "):
				y = drawParagraphs(img, f.title, strings.TrimLeft(t, "# "), mx, y+16, tw, 56, y+300, apLabel) + 6
			default:
				if task, done, text := noteTask(l); task {
					r := image.Rect(mx, y, w-mx, y+84)
					col := apBlue
					if inBlock {
						col = apSecondary // SOMA's own list: it follows SOMA, not taps
					}
					cx, cy := r.Min.X+30, (r.Min.Y+r.Max.Y)/2
					ui.Circle(img, cx, cy, 20, col)
					if done {
						iconCheck(img, cx-12, cy, rgb(0xffffff))
					} else {
						ui.Circle(img, cx, cy, 16, apBG)
					}
					tcol := apLabel
					if done {
						tcol = apSecondary
					}
					apText(img, f.body, r.Min.X+74, cy+13, tcol, clip(f.body, text, r.Dx()-90))
					if !inBlock {
						btn(fmt.Sprintf("so:tick:%d", i), r)
					}
					y += 88
					continue
				}
				if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
					ui.Circle(img, mx+14, y+30, 6, apSecondary)
					y = drawParagraphs(img, f.body, t[2:], mx+40, y, tw-40, 48, y+600, apLabel)
				} else {
					y = drawParagraphs(img, f.body, strings.ReplaceAll(t, "**", ""), mx, y, tw, 48, y+900, apLabel)
				}
				y += 8
			}
			if y > p.img.Rect.Dy()-600 {
				apText(img, f.caption, mx, y+30, apSecondary, "The rest is on the PC: this note is long.")
				y += 60
				break
			}
		}
		y += 30
		y = c.somaAddRow(p, "notetask", "Add a task", y)
		y = c.somaAddRow(p, "noteline", "Add a line", y)
		return y
	}

	notes := somaNotes()
	y = somaSection(img, mx, y, "Notes", fmt.Sprintf("%d in the vault", len(notes)))
	y = c.somaAddRow(p, "newnote", "New note", y)
	y += 10
	if len(notes) == 0 {
		y = drawParagraphs(img, f.callout, "No notes yet. In Soma on the PC, open Settings > Notes vault and choose your Obsidian vault: its notes appear here, and what you write here goes back to it.", mx, y+20, tw, 44, y+400, apSecondary)
		return y
	}
	for _, n := range notes {
		r := image.Rect(mx, y, w-mx, y+120)
		smCard(img, r, smSurface)
		apText(img, f.headline, r.Min.X+34, r.Min.Y+52, apLabel, clip(f.headline, n.title(), r.Dx()-300))
		when := time.UnixMilli(n.Mtime).In(displayZone)
		stamp := when.Format("2 Jan")
		if somaDate(when) == somaToday() {
			stamp = when.Format("15:04")
		}
		apText(img, f.caption, r.Max.X-34-ui.TextWidth(f.caption, stamp), r.Min.Y+52, apSecondary, stamp)
		preview := ""
		for _, l := range strings.Split(n.Text, "\n") {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "<!--") {
				preview = strings.ReplaceAll(t, "**", "")
				break
			}
		}
		if n.folder() != "" {
			preview = n.folder() + "  ·  " + preview
		}
		apText(img, f.caption, r.Min.X+34, r.Min.Y+94, apSecondary, clip(f.caption, preview, r.Dx()-68))
		btn("so:note:"+n.ID, r)
		y += 132
		if y > p.img.Rect.Dy()-300 {
			break
		}
	}
	return y
}
