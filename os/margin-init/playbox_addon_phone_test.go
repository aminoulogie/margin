package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// An add-on set up on its website is sent from the phone page: the tablet fetches its manifest,
// keeps it (replacing one with the same id) and the page lists it. Other sites can't add one.
func TestPlayboxAddonFromThePhone(t *testing.T) {
	c := testConsole(t)
	oldDir, oldAddons, oldClient := playboxDir, addons, webClient
	playboxDir = t.TempDir()
	addons = &addonStore{loaded: true, list: []installedAddon{{Base: "https://mediafusion.example", Manifest: addonManifest{ID: "stremio.mediafusion", Name: "MediaFusion (empty)"}}}}
	t.Cleanup(func() { playboxDir, addons, webClient = oldDir, oldAddons, oldClient })
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tok123/manifest.json") {
			w.Write([]byte(`{"id":"stremio.mediafusion","name":"MediaFusion","resources":["stream"],"types":["movie","series"]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()
	webClient = func() *http.Client { return fake.Client() }
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.sendPage)
	mux.HandleFunc("/playbox-addon", c.playboxAddonPost)
	srv := httptest.NewServer(localOnly(mux))
	defer srv.Close()
	post := func(origin, link string) map[string]any {
		req, _ := http.NewRequest("POST", srv.URL+"/playbox-addon", strings.NewReader(url.Values{"url": {link}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var v map[string]any
		json.NewDecoder(resp.Body).Decode(&v)
		return v
	}
	link := fake.URL + "/tok123/manifest.json"
	if v := post("http://evil.example", link); v["ok"] == true {
		t.Fatalf("another site added an add-on: %v", v)
	}
	if v := post(srv.URL, fake.URL+"/nothing/manifest.json"); v["ok"] == true || v["error"] == nil {
		t.Fatalf("a bad link: %v", v)
	}
	if v := post(srv.URL, link); v["ok"] != true || v["name"] != "MediaFusion" {
		t.Fatalf("adding: %v", v)
	}
	all := addons.all()
	if len(all) != 1 || all[0].Base != fake.URL+"/tok123" || all[0].Manifest.Name != "MediaFusion" {
		t.Fatalf("installed: %+v (the set-up one should replace the empty one)", all)
	}
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 1<<16)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(b.String(), "Playbox add-on") || !strings.Contains(b.String(), "On the tablet now: <span>MediaFusion</span>") {
		t.Fatal("the phone page doesn't show the add-on card with the installed list")
	}
}
