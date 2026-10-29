package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The Work tab's data, from Soma's records as the workstation reads them: projects with their
// steps, rate and client (projects.ts), time entries (time-tracking.ts), findings
// (findings.ts) and the ledger.

type wkStep struct {
	ID, Label string
	Done      bool
	At        time.Time // ticked
	WorkMs    int64     // worked on it with ▶ (step-work.ts)
}

type wkProject struct {
	ID, Name, Client, Color, Status, Due, Priority, Note string
	Rate                                                 float64
	Created, Touched                                     time.Time
	Steps                                                []wkStep
	Log                                                  []wkNote // the project's running notes
	order                                                int      // its place in Soma's list, for its colour and the charts' order
}

// wkNote is a note in a project's log (ProjectNote): typed in, or filed by the work log.
type wkNote struct {
	ID, Text, Source string
	At               time.Time
}

func (p wkProject) done() int {
	n := 0
	for _, s := range p.Steps {
		if s.Done {
			n++
		}
	}
	return n
}

func (p wkProject) progress() float64 {
	if len(p.Steps) == 0 {
		return 0
	}
	return float64(p.done()) / float64(len(p.Steps))
}

// daysLeft is the days to its deadline (negative: late), and whether it has one.
func (p wkProject) daysLeft(now time.Time) (int, bool) {
	if p.Due == "" {
		return 0, false
	}
	d, err := time.ParseInLocation("2006-01-02", p.Due, displayZone)
	if err != nil {
		return 0, false
	}
	t := time.Date(now.In(displayZone).Year(), now.In(displayZone).Month(), now.In(displayZone).Day(), 0, 0, 0, 0, displayZone)
	return int(d.Sub(t).Hours() / 24), true
}

// stale: active and nothing moved for ten days (projects.ts isStale, STALE_DAYS).
func (p wkProject) stale(now time.Time) bool {
	last := p.Touched
	if last.IsZero() {
		last = p.Created
	}
	return p.Status == "active" && !last.IsZero() && now.Sub(last) > 10*24*time.Hour
}

func msTime(v any) time.Time {
	if n := num(v); n > 0 {
		return time.UnixMilli(int64(n))
	}
	return time.Time{}
}

// wkProjects is every project, active first, then by deadline.
func wkProjects() []wkProject {
	var out []wkProject
	now := time.Now()
	for _, o := range somaList("projects") {
		p := wkProject{ID: str(o["id"]), Name: str(o["name"]), Client: str(o["client"]), Color: str(o["color"]),
			Status: str(o["status"]), Due: str(o["due"]), Priority: str(o["priority"]), Note: str(o["note"]),
			Rate: num(o["rate"]), Created: msTime(o["createdAt"]), Touched: msTime(o["touchedAt"])}
		if p.Status == "" {
			p.Status = "active"
		}
		steps, _ := o["steps"].([]any)
		for _, s := range steps {
			so, ok := s.(obj)
			if !ok {
				continue
			}
			st := wkStep{ID: str(so["id"]), Label: str(so["label"]), At: msTime(so["at"])}
			st.Done, _ = so["done"].(bool)
			work, _ := so["work"].([]any)
			for _, w := range work {
				if wo, ok := w.(obj); ok {
					end := num(wo["end"])
					if end == 0 {
						end = float64(now.UnixMilli())
					}
					st.WorkMs += int64(min(max(end-num(wo["start"]), 0), 12*3600e3))
				}
			}
			p.Steps = append(p.Steps, st)
		}
		log, _ := o["log"].([]any)
		for _, n := range log {
			if no, ok := n.(obj); ok && strings.TrimSpace(str(no["text"])) != "" {
				p.Log = append(p.Log, wkNote{ID: str(no["id"]), Text: str(no["text"]), Source: str(no["source"]), At: msTime(no["at"])})
			}
		}
		sort.SliceStable(p.Log, func(i, j int) bool { return p.Log[i].At.After(p.Log[j].At) })
		out = append(out, p)
	}
	for i := range out {
		out[i].order = i
	}
	rank := map[string]int{"active": 0, "paused": 1, "done": 2, "archived": 3}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Status] < rank[out[j].Status] })
	return out
}

func wkProjectByID(id string) (wkProject, bool) {
	for _, p := range wkProjects() {
		if p.ID == id {
			return p, true
		}
	}
	return wkProject{}, false
}

type wkEntry struct {
	ID, ProjectID, Note, StepID string
	Start, End                  time.Time // End zero while running
	Billable                    bool
	FromSteps                   bool // posted from steps' worked time (fromSteps)
}

// dur is how long it ran, a running one up to now, at most 12 hours (MAX_ENTRY_MS).
func (e wkEntry) dur(now time.Time) time.Duration {
	end := e.End
	if end.IsZero() {
		end = now
	}
	return min(max(end.Sub(e.Start), 0), 12*time.Hour)
}

func wkEntries() []wkEntry {
	var out []wkEntry
	for _, o := range somaList("timeEntries") {
		e := wkEntry{ID: str(o["id"]), ProjectID: str(o["projectId"]), Note: str(o["note"]), StepID: str(o["stepId"]),
			Start: msTime(o["start"]), End: msTime(o["end"]), Billable: true}
		if b, ok := o["billable"].(bool); ok {
			e.Billable = b
		}
		e.FromSteps, _ = o["fromSteps"].(bool)
		if !e.Start.IsZero() {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// wkRunning is the timer running now, if any.
func wkRunning(entries []wkEntry) (wkEntry, bool) {
	for _, e := range entries {
		if e.End.IsZero() {
			return e, true
		}
	}
	return wkEntry{}, false
}

// wkHours is the time on each project between from and to (local days, to excluded).
func wkHours(entries []wkEntry, from, to time.Time, now time.Time) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, e := range entries {
		if !e.Start.Before(from) && e.Start.Before(to) {
			out[e.ProjectID] += e.dur(now)
		}
	}
	return out
}

func wkSum(m map[string]time.Duration) time.Duration {
	var t time.Duration
	for _, d := range m {
		t += d
	}
	return t
}

// wkValue is what the billable time between from and to is worth at the projects' rates.
func wkValue(entries []wkEntry, projects []wkProject, from, to, now time.Time) (value float64, billable, all time.Duration) {
	rate := map[string]float64{}
	for _, p := range projects {
		rate[p.ID] = p.Rate
	}
	for _, e := range entries {
		if e.Start.Before(from) || !e.Start.Before(to) {
			continue
		}
		d := e.dur(now)
		all += d
		if e.Billable {
			billable += d
			value += d.Hours() * rate[e.ProjectID]
		}
	}
	// Standby (waiting on the client between pieces of work) at half the rate (work_timelog.go).
	_, sv := wkStandbyIn(projects, from, to, now)
	value += sv
	return
}

type wkFinding struct {
	ID, ProjectID, Title, Severity, Status, Area, Due string
}

func (f wkFinding) closed() bool { return f.Status == "resolved" || f.Status == "accepted" }

func wkFindings() []wkFinding {
	var out []wkFinding
	for _, o := range somaList("findings") {
		out = append(out, wkFinding{ID: str(o["id"]), ProjectID: str(o["projectId"]), Title: str(o["title"]),
			Severity: str(o["severity"]), Status: str(o["status"]), Area: str(o["area"]), Due: str(o["due"])})
	}
	return out
}

// dayStart is local midnight of t's day.
func dayStart(t time.Time) time.Time {
	t = t.In(displayZone)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, displayZone)
}

// weekStart is the Monday of t's week.
func weekStart(t time.Time) time.Time {
	d := dayStart(t)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// wkUser is the name Soma greets with.
func wkUser() string {
	for _, k := range []string{"settings.name", "settings.userName", "settings.displayName"} {
		if s := strings.Trim(soma.get(k), `"`); s != "" {
			return s
		}
	}
	return "Amine"
}

// wkMoney formats an amount in Soma's base currency, as the workstation does.
func wkMoney(v float64) string {
	cur := strings.Trim(soma.get("settings.currency"), `"`)
	n := fmtThousands(int(v + 0.5))
	if v < 0 {
		n = "-" + fmtThousands(int(-v+0.5))
	}
	switch strings.ToUpper(cur) {
	case "GBP", "£":
		return "£" + n
	case "EUR", "€":
		return "€" + n
	case "USD", "$":
		return "$" + n
	case "", "DZD", "DA":
		return n + " DA"
	}
	return n + " " + cur
}

// wkMoney2 is an amount with pence, for a running clock's worth.
func wkMoney2(v float64) string {
	s := wkMoney(math.Floor(v))
	cents := fmt.Sprintf(".%02d", int(math.Round((v-math.Floor(v))*100))%100)
	if i := strings.IndexAny(s, " "); i > 0 {
		return s[:i] + cents + s[i:]
	}
	return s + cents
}

func fmtThousands(n int) string {
	var s []byte
	d := []byte(strconv.Itoa(n))
	for i, c := range d {
		if i > 0 && (len(d)-i)%3 == 0 {
			s = append(s, ',')
		}
		s = append(s, c)
	}
	return string(s)
}
