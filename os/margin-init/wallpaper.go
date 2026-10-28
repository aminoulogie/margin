package main

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"strings"
	"time"

	"margin-init/ui"
)

// Wallpapers: Settings > Wallpaper shows HD photos from Lorem Picsum (free, no account; the
// photos are Unsplash's), and the one picked is the lock screen's background, kept with the
// covers (covers.go), so it's downloaded once and there from then on.

var picsumBase = "https://picsum.photos"

type wallpaper struct {
	ID, Author string
}

// wallURL is a photo at w x h.
func wallURL(id string, w, h int) string { return fmt.Sprintf("%s/id/%s/%d/%d", picsumBase, id, w, h) }

type wallState struct {
	items   []wallpaper
	page    int
	loading bool
	err     string
}

// loadWallpapers fetches the next page of photos. Caller holds drawMu.
func (c *console) loadWallpapers() {
	ws := &c.walls
	if ws.loading {
		return
	}
	ws.loading, ws.err = true, ""
	page := ws.page + 1
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var list []struct {
			ID     string `json:"id"`
			Author string `json:"author"`
		}
		err := getJSON(ctx, fmt.Sprintf("%s/v2/list?page=%d&limit=24", picsumBase, page), &list)
		drawMu.Lock()
		defer drawMu.Unlock()
		ws.loading = false
		if err != nil {
			ws.err = "Couldn't load wallpapers: is Wi-Fi on?"
		} else {
			ws.page = page
			for _, it := range list {
				ws.items = append(ws.items, wallpaper{it.ID, it.Author})
			}
		}
		if c.mode == modeSettings && c.setPane == "wallpaper" {
			c.showPage()
		}
	}()
}

// wallpaperPane draws the choice: the book's cover (the default), then the photos.
func (c *console) wallpaperPane(p *page, x, y, pw int) int {
	f := apple()
	img := p.img
	ws := &c.walls
	if ws.page == 0 && !ws.loading && ws.err == "" {
		c.loadWallpapers()
	}
	const cols = 3
	gap := 20
	tw := (pw - (cols-1)*gap) / cols
	th := tw * 16 / 10
	cell := func(i int) image.Rectangle {
		return image.Rect(x+(i%cols)*(tw+gap), y+(i/cols)*(th+gap+50), x+(i%cols)*(tw+gap)+tw, y+(i/cols)*(th+gap+50)+th)
	}
	// The default: the book being read.
	r := cell(0)
	ui.RoundRect(img, r, 20, apCard)
	apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, "Book Cover")
	if c.cfg.Wallpaper == "" {
		ui.RoundRect(img, image.Rect(r.Min.X, r.Max.Y+10, r.Max.X, r.Max.Y+16), 3, apBlue)
	}
	p.buttons = append(p.buttons, button{"wall:", r})
	for i, wp := range ws.items {
		r := cell(i + 1)
		if r.Min.Y > img.Rect.Dy()-th-200 {
			break
		}
		if th := covers.get(wallURL(wp.ID, 240, 384), r.Dx(), r.Dy()); th != nil {
			draw.Draw(img, r, th, th.Rect.Min, draw.Src)
		} else {
			ui.RoundRect(img, r, 20, apCard2)
		}
		apText(img, f.caption, r.Min.X+4, r.Max.Y+40, apSecondary, clip(f.caption, wp.Author, r.Dx()-8))
		if c.cfg.Wallpaper == wp.ID {
			ui.RoundRect(img, image.Rect(r.Min.X, r.Max.Y+10, r.Max.X, r.Max.Y+16), 3, apBlue)
		}
		p.buttons = append(p.buttons, button{"wall:" + wp.ID, r})
	}
	n := len(ws.items) + 1
	y = cell(n-1).Max.Y + 130
	switch {
	case ws.err != "":
		apText(img, f.callout, x, y, apRed, ws.err)
		p.buttons = append(p.buttons, button{"wall:more", image.Rect(x, y-50, x+pw, y+30)})
	case ws.loading:
		apText(img, f.callout, x, y, apSecondary, "Loading...")
	case len(ws.items) > 0:
		apText(img, f.body, x, y, apBlue, "More wallpapers")
		p.buttons = append(p.buttons, button{"wall:more", image.Rect(x, y-50, x+pw, y+30)})
	}
	return y + 60
}

// wallTap picks a wallpaper. Caller holds drawMu.
func (c *console) wallTap(id string) bool {
	rest, ok := strings.CutPrefix(id, "wall:")
	if !ok {
		return false
	}
	if rest == "more" {
		c.loadWallpapers()
	} else {
		c.cfg.Wallpaper = rest
		c.cfg.save()
		if rest != "" {
			covers.get(wallURL(rest, c.s.W, c.s.H), c.s.W, c.s.H-c.barH) // start downloading it now
		}
		c.toastMsg("Wallpaper set for the lock screen")
	}
	c.showPage()
	return true
}
