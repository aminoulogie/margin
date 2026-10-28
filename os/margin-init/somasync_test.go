package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Vectors made by Soma's own crypto.ts (src/lib/sync, run under Node) for the secret
// bytes i*37+11: the tablet must find the same vault, the same record ids, and open what
// the PC and the phone seal (and they must open what it seals: TestSomaSealOpensInSoma).
const (
	somaVecCode  = "1CR5-AYMZ-RKMG-WCTR-FPHC-FV0H-6SDR-19EA-XWA3-JQM3-N36Z-45SW-C630-48W1"
	somaVecVault = "ZB0rBW1gaSvmHMNrYGHzRw"
	somaVecToken = "_mzGSG2f8nlIoiyK82Z5P3v24mV-4TxCvfvhyINyeHg"
	somaVecID    = "IFnsnf8TEQLOhO8O4f5Mhg" // recordId("habits#abc123")
	somaVecBlob  = "lTaP0L730IQJNZoyFXPN0sjjrf6vB3nDuYtMQqdXnpwLjccVdp1sTXrNGtBlKJVnlLDIEnA6BT0bI2YSES5Rfo-i9g6t8WhioS7ETTD1nFD3i7q3MD9BPdizdub3mfqCpFAztbINfc12GUcSq78C8LFRgCEjS6UdMa76IFWzIDCzV-DnK--0xRuw6UYhOvKjPE40Fw"
)

func TestSomaKeysMatchSoma(t *testing.T) {
	secret := somaSecret(somaVecCode)
	if secret == nil {
		t.Fatal("Soma's recovery code was rejected")
	}
	for i, b := range secret {
		if b != byte(i*37+11) {
			t.Fatalf("secret byte %d is %d", i, b)
		}
	}
	if somaSecret("1CR5-AYMZ-RKMG-WCTR-FPHC-FV0H-6SDR-19EA-XWA3-JQM3-N36Z-45SW-C630-48W2") != nil {
		t.Error("a mistyped code was accepted")
	}
	if somaSecret("1cr5 aymz rkmg wctr fphc fv0h 6sdr 19ea xwa3 jqm3 n36z 45sw c630 48w1") == nil {
		t.Error("lower case and spaces should be fine")
	}
	k, err := somaKeysFor(secret)
	if err != nil {
		t.Fatal(err)
	}
	if k.vault != somaVecVault || k.token != somaVecToken {
		t.Fatalf("vault %s token %s: not Soma's", k.vault, k.token)
	}
	if id := k.recordID("habits#abc123"); id != somaVecID {
		t.Fatalf("record id %s, Soma's is %s", id, somaVecID)
	}
	e, err := k.open(somaVecBlob)
	if err != nil {
		t.Fatalf("can't open Soma's record: %v", err)
	}
	if e.Key != "todos#x1" || e.Value == nil || *e.Value != `{"id":"x1","text":"café ✓","done":false}` || e.T != 1700000000000 || e.Device != "pc000001" {
		t.Fatalf("opened %+v", e)
	}
	v := "hello"
	blob, _ := k.seal(somaEnvelope{Key: "a", Value: &v, T: 5, Device: "tab"})
	if back, err := k.open(blob); err != nil || *back.Value != "hello" {
		t.Fatalf("round trip: %v", err)
	}
}

// fakeRelay is Soma's sync server (server-core.ts) in memory: one vault, newest blob per id,
// a sequence number per write.
type fakeRelay struct {
	mu    sync.Mutex
	token string
	seq   int64
	recs  map[string]struct {
		seq  int64
		blob string
	}
}

func (f *fakeRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token || r.Header.Get("X-Vault") == "" {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	switch r.URL.Path {
	case "/v1/push":
		var body struct{ Items []struct{ ID, Blob string } }
		json.NewDecoder(r.Body).Decode(&body)
		for _, it := range body.Items {
			f.seq++
			f.recs[it.ID] = struct {
				seq  int64
				blob string
			}{f.seq, it.Blob}
		}
		json.NewEncoder(w).Encode(map[string]any{"seq": f.seq})
	case "/v1/pull":
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		type item struct {
			ID   string `json:"id"`
			Seq  int64  `json:"seq"`
			Blob string `json:"blob"`
		}
		var items []item
		for id, rec := range f.recs {
			if rec.seq > after {
				items = append(items, item{id, rec.seq, rec.blob})
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
		json.NewEncoder(w).Encode(map[string]any{"items": items, "more": false})
	default:
		http.NotFound(w, r)
	}
}

// pcPut is the PC writing a record (as Soma's engine does).
func (f *fakeRelay) pcPut(t *testing.T, k *somaKeys, key string, value any, at int64) {
	b, _ := json.Marshal(value)
	v := string(b)
	blob, err := k.seal(somaEnvelope{Key: key, Value: &v, T: at, Device: "pc000001"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.seq++
	f.recs[k.recordID(key)] = struct {
		seq  int64
		blob string
	}{f.seq, blob}
	f.mu.Unlock()
}

// pcGet is the PC reading a record back: its value and which device wrote it.
func (f *fakeRelay) pcGet(t *testing.T, k *somaKeys, key string) (obj, string) {
	f.mu.Lock()
	rec, ok := f.recs[k.recordID(key)]
	f.mu.Unlock()
	if !ok {
		return nil, ""
	}
	e, err := k.open(rec.blob)
	if err != nil || e.Value == nil {
		t.Fatalf("%s: %v", key, err)
	}
	var o obj
	json.Unmarshal([]byte(*e.Value), &o)
	return o, e.Device
}

func somaTestLink(t *testing.T) (*console, *fakeRelay, *somaKeys) {
	c := testConsole(t)
	oldDir, oldSoma, oldClient := somaDir, soma, webClient
	somaDir, soma = t.TempDir(), &somaStore{Recs: map[string]somaRec{}}
	k, _ := somaKeysFor(somaSecret(somaVecCode))
	relay := &fakeRelay{token: k.token, recs: map[string]struct {
		seq  int64
		blob string
	}{}}
	srv := httptest.NewServer(relay)
	webClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { srv.Close(); somaDir, soma, webClient = oldDir, oldSoma, oldClient })
	today := somaToday()
	now := time.Now().UnixMilli() - 60000
	relay.pcPut(t, k, "habits#h1", obj{"id": "h1", "name": "Stretch", "color": "#30d158", "desc": "", "goalDaysPerWeek": 7, "history": obj{}}, now)
	relay.pcPut(t, k, "habits#h2", obj{"id": "h2", "name": "Brush", "color": "#0a84ff", "history": obj{},
		"steps": []any{obj{"id": "am", "name": "Morning", "target": 1}, obj{"id": "pm", "name": "Night", "target": 2}}}, now)
	relay.pcPut(t, k, "habits@order", []string{"h2", "h1"}, now)
	relay.pcPut(t, k, "todos#t1", obj{"id": "t1", "text": "Buy milk", "done": false, "date": today, "scope": "day"}, now)
	relay.pcPut(t, k, "todos@order", []string{"t1"}, now)
	relay.pcPut(t, k, "dayPlans/"+today, []any{obj{"id": "b1", "label": "Sleep", "hours": 8, "color": "#5e5ce6", "fixed": true, "start": 23},
		obj{"id": "b2", "label": "Work", "hours": 16, "color": "#ff9f0a", "fixed": false}}, now)
	relay.pcPut(t, k, "history/"+today, obj{"split": "Push", "durationFormatted": "52:10", "totalSets": 18, "totalVol": 7400, "caloriesBurned": 380}, now)
	relay.pcPut(t, k, "nutrition/"+today, obj{"items": []any{obj{"name": "Eggs", "cals": 300, "p": 25, "c": 2, "f": 20}}, "goals": obj{"cals": 2400, "protein": 160}, "water": 1500}, now)
	relay.pcPut(t, k, "reading/"+today, 20, now)
	if err := soma.setLink(strings.TrimPrefix(srv.URL, "https://"), "1cr5 aymz rkmg wctr fphc fv0h 6sdr 19ea xwa3 jqm3 n36z 45sw c630 48w1"); err != nil {
		t.Fatal(err)
	}
	return c, relay, k
}

// The tablet joins the PC's vault, shows its data, and what it changes reaches the PC.
func TestSomaSyncsWithThePC(t *testing.T) {
	c, relay, k := somaTestLink(t)
	soma.sync()
	if s := soma.status(); !strings.HasPrefix(s, "Synced") {
		t.Fatalf("status %q", s)
	}
	hs := somaHabits()
	if len(hs) != 2 || hs[0].Name != "Brush" || hs[1].Name != "Stretch" {
		t.Fatalf("habits %+v (in Soma's order)", hs)
	}
	today := somaToday()
	if plan := somaPlan(today); len(plan) != 2 || clockOf(plan[0].Start) != "23:00" || clockOf(plan[1].Start) != "07:00" {
		t.Fatalf("plan %+v", plan)
	}
	if s := somaTraining(today); s == nil || s.Split != "Push" || s.Sets != 18 {
		t.Fatalf("training %+v", s)
	}
	if n := somaNutrition(today); n.Cals != 300 || n.GoalCals != 2400 {
		t.Fatalf("nutrition %+v", n)
	}

	// The tab draws, all three parts.
	drawMu.Lock()
	c.setMode(modeSoma)
	for _, v := range []string{"so:view:calendar", "so:view:reports", "so:view:today"} {
		c.somaTap(v)
	}
	// Tick both habits, read 15 minutes, tick the to-do, add one.
	c.somaTap("so:habit:h1")
	c.somaTap("so:habit:h2")
	c.somaTap("so:read:15")
	c.somaTap("so:todo:t1")
	c.somaTap("so:type:todo")
	c.somaKey([]byte("Call mum\r"))
	drawMu.Unlock()
	soma.sync()

	h1, dev := relay.pcGet(t, k, "habits#h1")
	if dev != soma.Device || h1["history"].(obj)[today] != true || h1["name"] != "Stretch" || h1["goalDaysPerWeek"] != float64(7) {
		t.Fatalf("the PC sees habit h1 as %v from %q", h1, dev)
	}
	h2, _ := relay.pcGet(t, k, "habits#h2")
	steps := h2["stepLog"].(obj)[today].(obj)
	if h2["history"].(obj)[today] != true || steps["am"] != float64(1) || steps["pm"] != float64(2) {
		t.Fatalf("a habit with steps: %v", h2)
	}
	if t1, _ := relay.pcGet(t, k, "todos#t1"); t1["done"] != true {
		t.Fatalf("to-do %v", t1)
	}
	order := soma.get("todos@order")
	var ids []string
	json.Unmarshal([]byte(order), &ids)
	if len(ids) < 2 || ids[0] != "t1" {
		t.Fatalf("order %v", ids)
	}
	if nt, _ := relay.pcGet(t, k, "todos#"+ids[1]); nt["text"] != "Call mum" || nt["date"] != today || nt["scope"] != "day" {
		t.Fatalf("new to-do %v", nt)
	}
	if somaReading(today) != 35 {
		t.Fatalf("reading %d", somaReading(today))
	}

	// Plan the day: tap 18:30 on today's timeline and type; it's a to-do at that time, as
	// Soma's addTodoAt makes it.
	drawMu.Lock()
	c.somaTap("so:view:calendar")
	c.somaTap("so:day:" + today)
	c.somaTap("so:at:1110")
	if !c.somaUI.typing || c.somaUI.field != "at:1110" {
		t.Fatalf("typing %v in %q", c.somaUI.typing, c.somaUI.field)
	}
	c.somaKey([]byte("Gym bag\r"))
	drawMu.Unlock()
	soma.sync()
	var slotted obj
	for _, td := range somaTodos() {
		if td.Text == "Gym bag" {
			slotted, _ = relay.pcGet(t, k, "todos#"+td.ID)
		}
	}
	slot, _ := slotted["slot"].(obj)
	if slotted == nil || slotted["date"] != today || slotted["scope"] != "day" || slot["date"] != today || slot["start"] != float64(1110) || slot["mins"] != float64(30) {
		t.Fatalf("the PC sees the planned to-do as %v", slotted)
	}

	// The PC changes the to-do back later: the tablet takes it.
	relay.pcPut(t, k, "todos#t1", obj{"id": "t1", "text": "Buy oat milk", "done": false, "date": today, "scope": "day"}, time.Now().UnixMilli()+1000)
	soma.sync()
	for _, td := range somaTodos() {
		if td.ID == "t1" && (td.Text != "Buy oat milk" || td.Done) {
			t.Fatalf("the PC's newer edit didn't win: %+v", td)
		}
	}

	// A restart: everything is still there, and still linked.
	dev = soma.Device
	soma = &somaStore{Recs: map[string]somaRec{}}
	if !soma.linked() || soma.Device != dev || len(somaHabits()) != 2 {
		t.Fatal("the tablet forgot Soma after a restart")
	}
}

// A link.txt sent from the PC links the tablet, even after Margin started.
func TestSomaLinkFromThePC(t *testing.T) {
	oldDir, oldSoma := somaDir, soma
	somaDir, soma = t.TempDir(), &somaStore{Recs: map[string]somaRec{}}
	t.Cleanup(func() { somaDir, soma = oldDir, oldSoma })
	if soma.linked() {
		t.Fatal("linked with nothing")
	}
	os.WriteFile(filepath.Join(somaDir, "link.txt"), []byte("soma-sync.example.workers.dev\r\n"+somaVecCode+"\r\n"), 0o600)
	if !soma.linked() {
		t.Fatal("link.txt wasn't taken")
	}
	if soma.link.URL != "https://soma-sync.example.workers.dev" || soma.keys.vault != somaVecVault {
		t.Fatalf("linked to %q, vault %q", soma.link.URL, soma.keys.vault)
	}
	if _, err := os.Stat(filepath.Join(somaDir, "link.txt")); err == nil {
		t.Error("link.txt should be replaced by link.json (readable by root only)")
	}
}

// Linking from the phone: the Send to Books page takes the address and code pasted from
// Soma on the phone; a mistyped code says so; another site can't do it; the page never
// shows the code.
func TestSomaLinkFromThePhone(t *testing.T) {
	c := testConsole(t)
	oldDir, oldSoma := somaDir, soma
	somaDir, soma = t.TempDir(), &somaStore{Recs: map[string]somaRec{}}
	t.Cleanup(func() { somaDir, soma = oldDir, oldSoma })
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.sendPage)
	mux.HandleFunc("/soma-link", c.somaLinkPost)
	srv := httptest.NewServer(localOnly(mux))
	defer srv.Close()
	post := func(origin, code string) map[string]any {
		form := url.Values{"url": {"soma-sync.example.workers.dev"}, "code": {code}}
		req, _ := http.NewRequest("POST", srv.URL+"/soma-link", strings.NewReader(form.Encode()))
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
	if v := post("http://evil.example", somaVecCode); v["ok"] == true || soma.linked() {
		t.Fatalf("another site linked Soma: %v", v)
	}
	if v := post(srv.URL, somaVecCode[:len(somaVecCode)-1]+"2"); v["ok"] == true || !strings.Contains(fmt.Sprint(v["error"]), "typo") {
		t.Fatalf("a mistyped code: %v", v)
	}
	if v := post(srv.URL, somaVecCode); v["ok"] != true || !soma.linked() || soma.keys.vault != somaVecVault {
		t.Fatalf("linking from the phone: %v", v)
	}
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(b)
	if !strings.Contains(page, "Link Soma") || !strings.Contains(page, "Linked.") || strings.Contains(page, somaVecCode[:9]) {
		t.Fatal("the page should say Soma is linked, and never show the code")
	}
}

// Goals and projects from the PC show on the tablet, and what's ticked there reaches the PC.
func TestSomaGoalsAndProjects(t *testing.T) {
	c, relay, k := somaTestLink(t)
	now, at := time.Now(), time.Now().UnixMilli()
	week, month := somaPeriod("week", now), somaPeriod("month", now)
	relay.pcPut(t, k, "goals#g1", obj{"id": "g1", "title": "Run 20 km", "horizon": "week", "period": week, "target": 20, "progress": 18, "unit": "km", "done": false, "color": "#30d158", "createdAt": at}, at)
	relay.pcPut(t, k, "goals#g2", obj{"id": "g2", "title": "Read two books", "horizon": "month", "period": month, "done": false, "color": "#0a84ff", "createdAt": at}, at)
	relay.pcPut(t, k, "goals#g3", obj{"id": "g3", "title": "Old week", "horizon": "week", "period": "2020-W01", "done": false, "color": "#0a84ff", "createdAt": at}, at)
	relay.pcPut(t, k, "projects#p1", obj{"id": "p1", "name": "Client site", "client": "Acme", "status": "active", "due": "2099-01-01", "color": "#ff9f0a", "createdAt": at,
		"steps": []any{obj{"id": "s1", "label": "Mockups", "done": true}, obj{"id": "s2", "label": "Build", "done": false}}}, at)
	relay.pcPut(t, k, "projects#p2", obj{"id": "p2", "name": "Paused", "status": "paused", "color": "#ff9f0a", "createdAt": at, "steps": []any{}}, at)
	soma.sync()

	if g := somaGoalsNow("week"); len(g) != 1 || g[0].Title != "Run 20 km" || g[0].Target != 20 {
		t.Fatalf("week goals %+v", g)
	}
	if p := somaProjects(); len(p) != 1 || p[0].Name != "Client site" || p[0].done() != 1 {
		t.Fatalf("projects %+v", p)
	}

	drawMu.Lock()
	c.setMode(modeSoma)
	c.somaTap("so:view:goals")
	shot(t, c, "soma-goals")
	c.somaTap("so:goal:g1:1")
	c.somaTap("so:goal:g1:1") // 20 of 20: done
	c.somaTap("so:goal:g2:0") // plain: ticked
	c.somaTap("so:step:p1:s2")
	c.somaTap("so:type:step:p1")
	c.somaKey([]byte("Launch\r"))
	drawMu.Unlock()
	soma.sync()

	if g1, _ := relay.pcGet(t, k, "goals#g1"); g1["progress"] != float64(20) || g1["done"] != true || g1["doneAt"] == nil {
		t.Fatalf("the PC sees g1 as %v", g1)
	}
	if g2, _ := relay.pcGet(t, k, "goals#g2"); g2["done"] != true {
		t.Fatalf("the PC sees g2 as %v", g2)
	}
	p1, _ := relay.pcGet(t, k, "projects#p1")
	steps := p1["steps"].([]any)
	if len(steps) != 3 || steps[1].(obj)["done"] != true || steps[2].(obj)["label"] != "Launch" || p1["touchedAt"] == nil {
		t.Fatalf("the PC sees p1 as %v", p1)
	}
}

// The vault's notes from the PC: listed, opened, a task ticked, a line and a note added.
func TestSomaNotes(t *testing.T) {
	c, relay, k := somaTestLink(t)
	at := time.Now().UnixMilli()
	relay.pcPut(t, k, "notes#Projects/Site.md", obj{"id": "Projects/Site.md", "mtime": at - 5000,
		"text": "# Site\n\nThe plan, **in bold**.\n\n## Steps\n- [ ] Mockups ^s-t9\n- [x] Brief\n- a bullet\n\n<!-- soma -->\n- [ ] generated\n<!-- /soma -->\n"}, at)
	relay.pcPut(t, k, "notes#Daily/2026-10-28.md", obj{"id": "Daily/2026-10-28.md", "mtime": at, "text": "# 2026-10-28\n"}, at)
	soma.sync()
	if ns := somaNotes(); len(ns) != 2 || ns[0].title() != "2026-10-28" || ns[1].folder() != "Projects" {
		t.Fatalf("notes %+v", ns)
	}

	drawMu.Lock()
	c.setMode(modeSoma)
	c.somaTap("so:view:notes")
	shot(t, c, "soma-notes")
	c.somaTap("so:note:Projects/Site.md")
	shot(t, c, "soma-note")
	c.somaTap("so:tick:5") // Mockups
	c.somaTap("so:type:notetask")
	c.somaKey([]byte("Send invoice\r"))
	c.somaTap("so:notes")
	c.somaTap("so:type:newnote")
	c.somaKey([]byte("Ideas\r"))
	drawMu.Unlock()
	soma.sync()

	site, _ := relay.pcGet(t, k, "notes#Projects/Site.md")
	text := site["text"].(string)
	if !strings.Contains(text, "- [x] Mockups ^s-t9\n") || !strings.HasSuffix(text, "<!-- /soma -->\n- [ ] Send invoice\n") || site["mtime"].(float64) <= float64(at) {
		t.Fatalf("the PC sees the note as %q (%v)", text, site["mtime"])
	}
	if idea, _ := relay.pcGet(t, k, "notes#Inbox/Ideas.md"); idea["text"] != "# Ideas\n\n" {
		t.Fatalf("new note %v", idea)
	}
	if c.somaUI.note != "Inbox/Ideas.md" {
		t.Fatalf("the new note isn't open: %q", c.somaUI.note)
	}
}

// Home shows Soma's day on top: rings, habits, to-dos to tick.
func TestSomaOnHome(t *testing.T) {
	c, _, _ := somaTestLink(t)
	soma.sync()
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeBooksHome)
	shot(t, c, "home-soma")
	c.somaTap("so:todo:t1")
	if td := somaTodos(); len(td) == 0 || !td[0].Done {
		t.Fatal("ticking a to-do on Home didn't tick it")
	}
}

// Going to sleep starts the night in Soma; the alarm's sunrise, ring and "I'm up" log it, and
// the morning shows the weather and the day. Wind-down turns the screen warm.
func TestSleepAndAlarm(t *testing.T) {
	c, relay, k := somaTestLink(t)
	soma.sync()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"current":{"temperature_2m":17.4,"weather_code":1},"daily":{"temperature_2m_max":[22.1],"temperature_2m_min":[12]}}`)
	}))
	defer srv.Close()
	oldURL, oldClient := weatherURL, webClient
	weatherURL, webClient = srv.URL, func() *http.Client { return srv.Client() }
	t.Cleanup(func() { weatherURL, webClient = oldURL, oldClient; c.s.warm = false })

	drawMu.Lock()
	c.setMode(modeSoma)
	c.somaTap("so:sleep")
	if c.screenOn || somaAsleepSince() == 0 {
		t.Fatal("going to sleep should start the night and turn the screen off")
	}
	// Asleep since eight hours ago; the alarm is in ten minutes: the sunrise.
	soma.put("settings.sleepStart", fmt.Sprint(time.Now().Add(-8*time.Hour).UnixMilli()))
	now := time.Now().In(displayZone)
	c.cfg.AlarmOn, c.cfg.AlarmMin = true, now.Hour()*60+now.Minute()+10
	if c.cfg.AlarmMin >= 24*60 {
		drawMu.Unlock()
		t.Skip("too close to midnight")
	}
	c.sleepTick(now)
	if c.alarm != "sunrise" || !c.screenOn {
		t.Fatalf("alarm %q, screen %v", c.alarm, c.screenOn)
	}
	shot(t, c, "alarm-sunrise")
	c.sleepTick(now.Add(10 * time.Minute))
	if c.alarm != "ringing" {
		t.Fatalf("alarm %q at its time", c.alarm)
	}
	c.alarmTouch(TouchPoint{Up: true})
	if c.alarm != "morning" || somaAsleepSince() != 0 {
		t.Fatalf("after I'm up: %q, asleep %v", c.alarm, somaAsleepSince())
	}
	drawMu.Unlock()
	waitFor(t, "the weather", func() bool { return c.weather.Temp == 17.4 })
	drawMu.Lock()
	c.drawAlarm()
	shot(t, c, "alarm-morning")
	c.alarmTouch(TouchPoint{Up: true})
	if c.alarm != "" {
		t.Fatal("a second tap should leave the morning screen")
	}
	c.sleepTick(now.Add(11 * time.Minute)) // dismissed today: no second ring
	if c.alarm != "" {
		t.Fatal("the alarm rang twice")
	}
	c.cfg.WindOn, c.cfg.WindMin = true, 0
	c.sleepTick(now)
	if !c.winding || !c.s.warm || c.mode != modeSoma {
		t.Fatal("wind-down should warm the screen and open Soma")
	}
	drawMu.Unlock()
	soma.sync()

	day, _ := relay.pcGet(t, k, "nutrition/"+somaToday())
	sl, _ := day["sleep"].(obj)
	if sl == nil || sl["hours"].(float64) < 7.9 || sl["hours"].(float64) > 8.1 || day["items"] == nil {
		t.Fatalf("the PC sees the night as %v", day)
	}
	if soma.get("settings.sleepStart") != "" {
		t.Fatal("sleepStart still set")
	}
}

// Amounts climb like Soma's ramps; runs forgive a missed day a week; habits group by time;
// to-dos take a priority; the evening review moves what's left to tomorrow.
func TestSomaHabitsAndReview(t *testing.T) {
	c, relay, k := somaTestLink(t)
	today := somaToday()
	d0, _ := time.Parse("2006-01-02", today)
	day := func(n int) string { return d0.AddDate(0, 0, n).Format("2006-01-02") }
	at := time.Now().UnixMilli()
	// A ramp from 10 pages, +5 a day it's met, started two days ago: met both days, so today's is 20.
	relay.pcPut(t, k, "habits#r1", obj{"id": "r1", "name": "Read pages", "color": "#30d158", "history": obj{},
		"ramp":      obj{"start": 10, "target": 50, "step": 5, "from": day(-2), "unit": "page", "advance": "earned"},
		"amountLog": obj{day(-2): 10, day(-1): 15}}, at)
	// Done every day for two weeks but one: a 13-day run (the miss is forgiven).
	hist := obj{}
	for i := 1; i <= 14; i++ {
		if i != 5 {
			hist[day(-i)] = true
		}
	}
	relay.pcPut(t, k, "habits#s1", obj{"id": "s1", "name": "Stretch", "color": "#0a84ff", "history": hist}, at)
	soma.sync()
	var r1, s1 somaHabit
	for _, h := range somaHabits() {
		switch h.ID {
		case "r1":
			r1 = h
		case "s1":
			s1 = h
		}
	}
	if _, rung, _ := r1.amount(today); rung != 20 {
		t.Fatalf("today's rung %v, want 20", rung)
	}
	if n := s1.streak(today); n != 13 {
		t.Fatalf("run %d, want 13", n)
	}

	drawMu.Lock()
	c.setMode(modeSoma)
	for i := 0; i < 4; i++ {
		c.somaTap("so:amt:r1:5")
	}
	c.somaTap("so:when:s1")
	c.somaTap("so:prio:t1")
	c.somaTap("so:type:todo")
	c.somaKey([]byte("Later\r"))
	c.somaTap("so:todo:t1")
	shot(t, c, "soma-today")
	n := somaMoveToTomorrow()
	drawMu.Unlock()
	soma.sync()

	h, _ := relay.pcGet(t, k, "habits#r1")
	if h["amountLog"].(obj)[today] != float64(20) || h["history"].(obj)[today] != true {
		t.Fatalf("the PC sees the ramp as %v", h)
	}
	if s, _ := relay.pcGet(t, k, "habits#s1"); s["time"] != "morning" {
		t.Fatalf("time %v", s["time"])
	}
	if t1, _ := relay.pcGet(t, k, "todos#t1"); t1["priority"] != "high" || t1["date"] != today {
		t.Fatalf("t1 %v (done: it stays today)", t1)
	}
	if n != 1 {
		t.Fatalf("moved %d, want 1 (Later)", n)
	}
	for _, td := range somaTodos() {
		if td.Text == "Later" && td.Date != day(1) {
			t.Fatalf("Later is on %s", td.Date)
		}
	}
}

// The calendar's week, and moving a timed to-do: its handle, then the new time.
func TestSomaWeekAndMove(t *testing.T) {
	c, relay, k := somaTestLink(t)
	today := somaToday()
	relay.pcPut(t, k, "todos#t2", obj{"id": "t2", "text": "Call", "done": false, "date": today, "scope": "day",
		"slot": obj{"date": today, "start": 600, "mins": 45}}, time.Now().UnixMilli())
	soma.sync()
	drawMu.Lock()
	c.setMode(modeSoma)
	c.somaTap("so:view:calendar")
	c.somaTap("so:cal:week")
	shot(t, c, "soma-week")
	c.somaTap("so:day:" + today)
	c.somaTap("so:move:t2")
	if c.somaUI.moving != "t2" {
		t.Fatal("the handle didn't pick it up")
	}
	c.somaTap("so:moveto:900")
	drawMu.Unlock()
	soma.sync()
	t2, _ := relay.pcGet(t, k, "todos#t2")
	if s := t2["slot"].(obj); s["start"] != float64(900) || s["mins"] != float64(45) || s["date"] != today {
		t.Fatalf("the PC sees the slot as %v", s)
	}
}

// Reports switch between a week, a month and a year.
func TestSomaReports(t *testing.T) {
	c, _, _ := somaTestLink(t)
	soma.sync()
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeSoma)
	c.somaTap("so:view:reports")
	shot(t, c, "soma-reports")
	for _, r := range []string{"month", "year", "week"} {
		c.somaTap("so:rep:" + r)
		if c.somaUI.repRange != r {
			t.Fatalf("range %q", c.somaUI.repRange)
		}
	}
}
