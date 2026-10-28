package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Settings > Wallpaper lists photos, and the one picked is the lock screen's background.
func TestWallpaper(t *testing.T) {
	c := testConsole(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/list") {
			json.NewEncoder(w).Encode([]map[string]string{{"id": "10", "author": "Paul Jarvis"}, {"id": "11", "author": "Ana"}})
			return
		}
		img := image.NewRGBA(image.Rect(0, 0, 120, 192))
		for i := range img.Pix {
			img.Pix[i] = 200
		}
		img.Set(5, 5, color.RGBA{10, 120, 200, 255})
		jpeg.Encode(w, img, nil)
	}))
	defer srv.Close()
	oldBase, oldClient := picsumBase, webClient
	picsumBase, webClient = srv.URL, func() *http.Client { return srv.Client() }
	t.Cleanup(func() { picsumBase, webClient = oldBase, oldClient; c.cfg.Wallpaper = "" })

	drawMu.Lock()
	c.setPane = "wallpaper"
	c.setMode(modeSettings)
	drawMu.Unlock()
	waitFor(t, "the list", func() bool { return len(c.walls.items) == 2 })
	drawMu.Lock()
	c.showPage()
	shot(t, c, "settings-wallpaper")
	c.wallTap("wall:10")
	drawMu.Unlock()
	if c.cfg.Wallpaper != "10" {
		t.Fatalf("wallpaper %q", c.cfg.Wallpaper)
	}
	waitFor(t, "the photo", func() bool { return covers.get(wallURL("10", c.s.W, c.s.H), c.s.W, c.s.H-c.barH) != nil })
	drawMu.Lock()
	c.locked = true
	c.redrawAll()
	shot(t, c, "lock-wallpaper")
	c.locked = false
	drawMu.Unlock()
}
