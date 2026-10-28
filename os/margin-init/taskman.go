package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// Settings > Task Manager, as Windows has it: how busy the processor is (all of it and each
// core), memory in use, what Margin keeps on the GPU, and the processes using the most, from
// /proc, every two seconds while it's open.

type cpuTimes struct{ busy, total uint64 }

type procSample struct {
	pid   int
	name  string
	ticks uint64
	rssKB int
}

type taskSample struct {
	at       time.Time
	cpu      []cpuTimes // [0] all, then each core
	procs    map[int]procSample
	memTotal int // kB
	memUsed  int
	swapUsed int
}

type taskRow struct {
	name   string
	cpuPct float64
	rssKB  int
}

type taskState struct {
	prev, cur *taskSample
	history   []float64 // the processor, the last minute
	running   bool
}

func readCPU() []cpuTimes {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}
	var out []cpuTimes
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var t cpuTimes
		for i, v := range f[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			t.total += n
			if i != 3 && i != 4 { // idle and iowait aren't busy
				t.busy += n
			}
		}
		out = append(out, t)
	}
	return out
}

func readMem() (total, used, swapUsed int) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	m := map[string]int{}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			n, _ := strconv.Atoi(f[1])
			m[strings.TrimSuffix(f[0], ":")] = n
		}
	}
	free := m["MemAvailable"]
	if free == 0 { // kernels before 3.14 (this tablet's 3.4): free, plus what the cache can give back
		free = m["MemFree"] + m["Buffers"] + m["Cached"]
	}
	return m["MemTotal"], m["MemTotal"] - free, m["SwapTotal"] - m["SwapFree"]
}

func readProcs() map[int]procSample {
	out := map[int]procSample{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		pid, _ := strconv.Atoi(filepath.Base(d))
		b, err := os.ReadFile(d + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || end < open {
			continue
		}
		f := strings.Fields(s[end+1:])
		if len(f) < 22 {
			continue
		}
		ut, _ := strconv.ParseUint(f[11], 10, 64)
		st, _ := strconv.ParseUint(f[12], 10, 64)
		rss, _ := strconv.Atoi(f[21])
		out[pid] = procSample{pid: pid, name: s[open+1 : end], ticks: ut + st, rssKB: rss * os.Getpagesize() / 1024}
	}
	return out
}

func takeSample() *taskSample {
	t, u, sw := readMem()
	return &taskSample{at: time.Now(), cpu: readCPU(), procs: readProcs(), memTotal: t, memUsed: u, swapUsed: sw}
}

// pct is how busy cpu i was between the two samples.
func (ts *taskState) pct(i int) float64 {
	if ts.prev == nil || ts.cur == nil || i >= len(ts.cur.cpu) || i >= len(ts.prev.cpu) {
		return 0
	}
	a, b := ts.prev.cpu[i], ts.cur.cpu[i]
	if b.total <= a.total {
		return 0
	}
	return 100 * float64(b.busy-a.busy) / float64(b.total-a.total)
}

// top is the processes that used the most processor between the samples, then memory.
func (ts *taskState) top(n int) []taskRow {
	if ts.prev == nil || ts.cur == nil || len(ts.cur.cpu) == 0 || len(ts.prev.cpu) == 0 {
		return nil
	}
	all := float64(ts.cur.cpu[0].total - ts.prev.cpu[0].total)
	var rows []taskRow
	for pid, p := range ts.cur.procs {
		r := taskRow{name: p.name, rssKB: p.rssKB}
		if q, ok := ts.prev.procs[pid]; ok && all > 0 && p.ticks >= q.ticks {
			r.cpuPct = 100 * float64(p.ticks-q.ticks) / all * float64(max(len(ts.cur.cpu)-1, 1))
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].cpuPct != rows[j].cpuPct {
			return rows[i].cpuPct > rows[j].cpuPct
		}
		return rows[i].rssKB > rows[j].rssKB
	})
	return rows[:min(n, len(rows))]
}

// startTaskManager samples every two seconds while the pane is open. Caller holds drawMu.
func (c *console) startTaskManager() {
	ts := &c.tasks
	if ts.running {
		return
	}
	ts.running = true
	ts.cur = takeSample()
	go func() {
		for {
			time.Sleep(2 * time.Second)
			s := takeSample()
			drawMu.Lock()
			if c.mode != modeSettings || c.setPane != "tasks" || !c.screenOn {
				ts.running = false
				drawMu.Unlock()
				return
			}
			ts.prev, ts.cur = ts.cur, s
			ts.history = append(ts.history, ts.pct(0))
			if len(ts.history) > 30 {
				ts.history = ts.history[1:]
			}
			c.showPage()
			drawMu.Unlock()
		}
	}()
}

// gpuBytes is what Margin keeps on the GPU now: the page's slices and the row being slid.
func (c *console) gpuBytes() int {
	if c.disp == nil {
		return 0
	}
	n := len(c.sc.resident) * pageSlice * c.s.W * 4
	if c.sc.strip != "" {
		n += 12288 * 512 * 4
	}
	return n
}

// taskPane draws the Task Manager. Returns the y under it.
func (c *console) taskPane(p *page, x, y, pw int) int {
	f := apple()
	img := p.img
	ts := &c.tasks
	c.startTaskManager()
	card := func(h int) image.Rectangle {
		r := image.Rect(x, y, x+pw, y+h)
		ui.RoundRect(img, r, radiusCard, apCard)
		return r
	}
	bar := func(r image.Rectangle, pct float64, col uint32) {
		ui.RoundRect(img, r, r.Dy()/2, apCard2)
		if pct > 0 {
			ui.RoundRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+max(r.Dy(), int(float64(r.Dx())*min(pct, 100)/100)), r.Max.Y), r.Dy()/2, rgb(col))
		}
	}

	// The processor: the last minute as a graph, then each core.
	all := ts.pct(0)
	cores := 0
	if ts.cur != nil {
		cores = len(ts.cur.cpu) - 1
	}
	r := card(300 + 50*max(cores, 1))
	apText(img, f.headline, r.Min.X+30, r.Min.Y+50, apLabel, "Processor")
	label := fmt.Sprintf("%.0f%%", all)
	apText(img, f.title, r.Max.X-30-ui.TextWidth(f.title, label), r.Min.Y+56, rgb(0x0a84ff), label)
	g := image.Rect(r.Min.X+30, r.Min.Y+80, r.Max.X-30, r.Min.Y+240)
	ui.RoundRect(img, g, 12, apCard2)
	if n := len(ts.history); n > 1 {
		step := g.Dx() / 29
		for i := 1; i < n; i++ {
			x0, x1 := g.Max.X-(n-i)*step, g.Max.X-(n-1-i)*step
			y0 := g.Max.Y - int(float64(g.Dy())*ts.history[i-1]/100)
			y1 := g.Max.Y - int(float64(g.Dy())*ts.history[i]/100)
			line(img, x0, y0, x1, y1, 4, rgb(0x0a84ff))
		}
	}
	cy := r.Min.Y + 290
	for i := 1; i <= cores; i++ {
		apText(img, f.caption, r.Min.X+30, cy, apSecondary, fmt.Sprintf("Core %d", i))
		bar(image.Rect(r.Min.X+160, cy-22, r.Max.X-130, cy-6), ts.pct(i), 0x0a84ff)
		apText(img, f.caption, r.Max.X-110, cy, apLabel, fmt.Sprintf("%.0f%%", ts.pct(i)))
		cy += 50
	}
	y = r.Max.Y + 20

	// Memory.
	r = card(170)
	total, used := 0, 0
	if ts.cur != nil {
		total, used = ts.cur.memTotal, ts.cur.memUsed
	}
	mp := 0.0
	if total > 0 {
		mp = 100 * float64(used) / float64(total)
	}
	apText(img, f.headline, r.Min.X+30, r.Min.Y+50, apLabel, "Memory")
	label = fmt.Sprintf("%.0f%%", mp)
	apText(img, f.title, r.Max.X-30-ui.TextWidth(f.title, label), r.Min.Y+56, rgb(0xbf5af2), label)
	bar(image.Rect(r.Min.X+30, r.Min.Y+80, r.Max.X-30, r.Min.Y+104), mp, 0xbf5af2)
	apText(img, f.caption, r.Min.X+30, r.Min.Y+146, apSecondary, fmt.Sprintf("%d of %d MB in use", used/1024, total/1024))
	y = r.Max.Y + 20

	// The GPU: what Margin keeps on it (the SGX's load isn't readable on this kernel).
	r = card(170)
	gb := c.gpuBytes()
	gpuMB := float64(gb) / (1 << 20)
	budget := float64(pageResident*pageSlice*c.s.W*4+12288*512*4) / (1 << 20)
	apText(img, f.headline, r.Min.X+30, r.Min.Y+50, apLabel, "GPU")
	gp := 100 * gpuMB / budget
	label = fmt.Sprintf("%.0f%%", gp)
	if c.disp == nil {
		label = "off"
	}
	apText(img, f.title, r.Max.X-30-ui.TextWidth(f.title, label), r.Min.Y+56, rgb(0x30d158), label)
	bar(image.Rect(r.Min.X+30, r.Min.Y+80, r.Max.X-30, r.Min.Y+104), gp, 0x30d158)
	apText(img, f.caption, r.Min.X+30, r.Min.Y+146, apSecondary, fmt.Sprintf("%.0f of %.0f MB of Margin's pages and rows", gpuMB, budget))
	y = r.Max.Y + 30

	// Processes.
	apText(img, f.caption, x+30, y+40, apSecondary, "PROCESSES")
	y += 56
	rows := ts.top(12)
	r = card(40 + 64*max(len(rows), 1))
	ry := r.Min.Y + 56
	if len(rows) == 0 {
		apText(img, f.callout, r.Min.X+30, ry, apSecondary, "Measuring...")
	}
	for _, t := range rows {
		apText(img, f.body, r.Min.X+30, ry, apLabel, clip(f.body, t.name, pw-420))
		apText(img, f.callout, r.Max.X-370, ry, rgb(0x0a84ff), fmt.Sprintf("%5.1f%%", t.cpuPct))
		apText(img, f.callout, r.Max.X-200, ry, apSecondary, fmt.Sprintf("%d MB", t.rssKB/1024))
		ry += 64
	}
	return r.Max.Y + 20
}
