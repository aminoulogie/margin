package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// margin-init soma-check food: today's and yesterday's food, and today's habit ticks, as the
// tablet has them next to what Soma's relay has, plus the newest things the relay got and from
// which device. Read only: nothing is changed or sent. It tells which side is stuck when food
// logged on the phone doesn't show here, or a habit ticked here doesn't show on the phone:
//   - the relay's newest records from the phone's device are old: the phone isn't sending
//     (iPhone apps stop while they're in the background: keep Soma open a few seconds after a change);
//   - the relay has the phone's food and the tablet doesn't: the tablet isn't taking it;
//   - the tablet's habit is on the relay from the tablet's device: it's sent, the phone has to pull.
func runSomaFoodCheck() int {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		somaDir = "/proc/1/root" + marginHome + "/soma"
	}
	soma.load()
	now := time.Now().In(displayZone)
	when := func(ms int64) string { return time.UnixMilli(ms).In(displayZone).Format("Mon 02 Jan 15:04:05") }
	soma.mu.Lock()
	linked := soma.keys != nil
	fmt.Printf("tablet clock: %s (WAT)\n", now.Format("Mon 2006-01-02 15:04:05"))
	fmt.Printf("tablet: device %s, pulled up to seq %d, last good sync %s, %d records kept\n",
		soma.Device, soma.Seq, soma.LastOK.In(displayZone).Format("2006-01-02 15:04"), len(soma.Recs))
	mine := map[string]somaRec{}
	dirty := 0
	for k, r := range soma.Recs {
		if r.Dirty {
			dirty++
		}
		if r.V != nil && (strings.HasPrefix(k, "nutrition/") || strings.HasPrefix(k, "habits#")) {
			mine[k] = r
		}
	}
	device := soma.Device
	soma.mu.Unlock()
	fmt.Printf("tablet: %d changes not sent yet\n", dirty)
	if !linked {
		fmt.Println("not linked to Soma: nothing more to check")
		return 1
	}

	type rec struct {
		seq    int64
		key    string
		value  *string
		device string
		t      int64
		size   int
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	relay := map[string]rec{}
	var all []rec
	var top int64
	for after := int64(0); ; {
		b, err := soma.call(ctx, "GET", fmt.Sprintf("/v1/pull?after=%d", after), nil)
		if err != nil {
			fmt.Printf("relay: %v\n", err)
			return 1
		}
		var res struct {
			Items []struct {
				Seq  int64  `json:"seq"`
				Blob string `json:"blob"`
			} `json:"items"`
			More bool `json:"more"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			fmt.Printf("relay: %v\n", err)
			return 1
		}
		for _, it := range res.Items {
			after, top = max(after, it.Seq), max(top, it.Seq)
			e, err := soma.keys.open(it.Blob)
			if err != nil {
				continue
			}
			r := rec{it.Seq, e.Key, e.Value, e.Device, e.T, len(it.Blob)}
			all = append(all, r)
			relay[e.Key] = r
		}
		if !res.More || len(res.Items) == 0 {
			break
		}
	}
	fmt.Printf("relay: up to seq %d (the tablet has pulled up to %d), %d records\n", top, soma.Seq, len(relay))

	// Who wrote the relay's newest records.
	sort.Slice(all, func(i, j int) bool { return all[i].seq > all[j].seq })
	fmt.Println("the relay's 14 newest records (seq, when it was written, device, name, size):")
	for i := 0; i < len(all) && i < 14; i++ {
		r := all[i]
		who := r.device
		if r.device == device {
			who += " (this tablet)"
		}
		fmt.Printf("  %d  %s  %s  %s  %d bytes\n", r.seq, when(r.t), who, r.key, r.size)
	}
	last := map[string]int64{}
	for _, r := range relay {
		last[r.device] = max(last[r.device], r.t)
	}
	for d, t := range last {
		who := d
		if d == device {
			who += " (this tablet)"
		}
		fmt.Printf("device %s last wrote to the relay: %s\n", who, when(t))
	}

	// Food.
	day := func(v *string) (items, planned int, water float64, ok bool) {
		if v == nil {
			return 0, 0, 0, false
		}
		var o obj
		if json.Unmarshal([]byte(*v), &o) != nil {
			return 0, 0, 0, false
		}
		a, _ := o["items"].([]any)
		p, _ := o["planned"].([]any)
		return len(a), len(p), num(o["water"]), true
	}
	for _, d := range []string{somaDate(now), somaDate(now.AddDate(0, 0, -1))} {
		k := "nutrition/" + d
		m, inMine := mine[k]
		r, inRelay := relay[k]
		fmt.Printf("food %s:\n", d)
		if inMine {
			i, p, w, _ := day(m.V)
			fmt.Printf("  tablet: %d eaten, %d waiting, water %.0f ml, written %s by %s\n", i, p, w, when(m.T), m.D)
		} else {
			fmt.Println("  tablet: no record")
		}
		if inRelay {
			i, p, w, _ := day(r.value)
			fmt.Printf("  relay:  %d eaten, %d waiting, water %.0f ml, written %s by %s\n", i, p, w, when(r.t), r.device)
		} else {
			fmt.Println("  relay:  no record (nothing has sent this day yet)")
		}
		switch {
		case inRelay && !inMine:
			fmt.Println("  -> the relay has it and the tablet hasn't taken it")
		case inMine && !inRelay:
			fmt.Println("  -> only the tablet has it: the phone doesn't have it either")
		case inMine && inRelay && m.T > r.t:
			fmt.Println("  -> the tablet's copy is newer than the relay's")
		case inMine && inRelay && m.T < r.t:
			fmt.Println("  -> the relay's copy is newer than the tablet's: the next pull takes it")
		}
	}

	// Habits ticked today.
	today := somaDate(now)
	ticked := func(v *string) (bool, string, bool) {
		if v == nil {
			return false, "", false
		}
		var o obj
		if json.Unmarshal([]byte(*v), &o) != nil {
			return false, "", false
		}
		h, _ := o["history"].(obj)
		on, _ := h[today].(bool)
		return on, str(o["name"]), true
	}
	keys := map[string]bool{}
	for k := range mine {
		if strings.HasPrefix(k, "habits#") {
			keys[k] = true
		}
	}
	for k := range relay {
		if strings.HasPrefix(k, "habits#") {
			keys[k] = true
		}
	}
	var lines []string
	for k := range keys {
		m, inMine := mine[k]
		r, inRelay := relay[k]
		var tOn, rOn bool
		name := ""
		if inMine {
			tOn, name, _ = ticked(m.V)
		}
		if inRelay {
			var n string
			rOn, n, _ = ticked(r.value)
			if name == "" {
				name = n
			}
		}
		if !tOn && !rOn {
			continue
		}
		line := fmt.Sprintf("  %s: tablet %s, relay %s", name, map[bool]string{true: "ticked", false: "not ticked"}[tOn],
			map[bool]string{true: "ticked", false: "not ticked"}[rOn])
		if inRelay {
			line += fmt.Sprintf(" (relay copy written %s by %s)", when(r.t), r.device)
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	fmt.Printf("habits ticked today (%s), %d:\n", today, len(lines))
	for _, l := range lines {
		fmt.Println(l)
	}
	return 0
}
