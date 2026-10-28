package main

import (
	"slices"
	"strings"
	"time"
)

// Changes made in the workstation, written the way Soma's own store makes them (store.ts,
// projects.ts, todos.ts, findings.ts), so the phone and the PC read them back as their own: an
// id'd list is its items (field#id) and its order (field@order).

// somaIDs is a list's ids in its order.
func somaIDs(field string) []string {
	var ids []string
	for _, o := range somaList(field) {
		ids = append(ids, str(o["id"]))
	}
	return ids
}

// somaAdd puts a new item at the end of a list.
func somaAdd(field, id string, o obj) {
	ids := somaIDs(field)
	somaPut(field+"#"+id, o)
	if !slices.Contains(ids, id) {
		ids = append(ids, id)
	}
	somaPut(field+"@order", ids)
}

// somaRemove takes an item out of a list; returns it and where it was, for undo.
func somaRemove(field, id string) (obj, int) {
	o := somaObj(field + "#" + id)
	ids := somaIDs(field)
	i := slices.Index(ids, id)
	if o == nil || i < 0 {
		return nil, -1
	}
	soma.put(field+"#"+id, "")
	somaPut(field+"@order", slices.Delete(ids, i, i+1))
	return o, i
}

// somaRestore puts a removed item back where it was (unless it's back already).
func somaRestore(field string, o obj, at int) {
	id := str(o["id"])
	ids := somaIDs(field)
	if o == nil || slices.Contains(ids, id) {
		return
	}
	somaPut(field+"#"+id, o)
	at = min(max(at, 0), len(ids))
	somaPut(field+"@order", slices.Insert(ids, at, id))
}

// somaPatch changes an item of a list in place.
func somaPatch(field, id string, fn func(o obj)) {
	o := somaObj(field + "#" + id)
	if o == nil {
		return
	}
	fn(o)
	somaPut(field+"#"+id, o)
}

// somaMove moves an item of a list by delta places.
func somaMove(field, id string, delta int) {
	ids := somaIDs(field)
	i := slices.Index(ids, id)
	j := i + delta
	if i < 0 || j < 0 || j >= len(ids) {
		return
	}
	ids[i], ids[j] = ids[j], ids[i]
	somaPut(field+"@order", ids)
}

func nowMs() float64 { return float64(time.Now().UnixMilli()) }

// --- projects ------------------------------------------------------------------------------------

// wkProjectColours are Soma's PROJECT_COLORS.
var wkProjectColours = []string{"#5b8cff", "#35c9a8", "#f0a63c", "#e2607a", "#9b7bf0", "#3fb6f0", "#5ecf7a", "#e0785a"}

// wkNewProject is addProject: active, no steps, the next colour; returns its id.
func wkNewProject(name, client string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled"
	}
	id := "pj-" + somaNewID()
	o := obj{"id": id, "name": name, "color": wkProjectColours[len(somaIDs("projects"))%len(wkProjectColours)],
		"steps": []any{}, "status": "active", "createdAt": nowMs()}
	if client = strings.TrimSpace(client); client != "" {
		o["client"] = client
	}
	somaAdd("projects", id, o)
	return id
}

// wkSetProject sets (or with nil, removes) one of a project's fields (patchProject).
func wkSetProject(id, key string, v any) {
	somaPatch("projects", id, func(o obj) {
		if v == nil || v == "" {
			delete(o, key)
		} else {
			o[key] = v
		}
	})
}

// wkProjectSteps changes a project's steps; touch marks it moved (adding, ticking, removing).
func wkProjectSteps(id string, touch bool, fn func(steps []any) []any) {
	somaPatch("projects", id, func(o obj) {
		steps, _ := o["steps"].([]any)
		o["steps"] = fn(steps)
		if touch {
			o["touchedAt"] = nowMs()
		}
	})
}

func stepIndex(steps []any, stepID string) int {
	for i, s := range steps {
		if so, ok := s.(obj); ok && str(so["id"]) == stepID {
			return i
		}
	}
	return -1
}

func wkRenameStep(pid, sid, label string) {
	if label = strings.TrimSpace(label); label == "" {
		return
	}
	wkProjectSteps(pid, false, func(steps []any) []any {
		if i := stepIndex(steps, sid); i >= 0 {
			steps[i].(obj)["label"] = label
		}
		return steps
	})
}

func wkMoveStep(pid, sid string, delta int) {
	wkProjectSteps(pid, false, func(steps []any) []any {
		i := stepIndex(steps, sid)
		if j := i + delta; i >= 0 && j >= 0 && j < len(steps) {
			steps[i], steps[j] = steps[j], steps[i]
		}
		return steps
	})
}

func wkRemoveStep(pid, sid string) {
	wkProjectSteps(pid, true, func(steps []any) []any {
		if i := stepIndex(steps, sid); i >= 0 {
			return slices.Delete(steps, i, i+1)
		}
		return steps
	})
}

// wkAddProjectNote is upsertProjectNote with a new note, newest last as Soma keeps them.
func wkAddProjectNote(pid, text string) {
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	somaPatch("projects", pid, func(o obj) {
		log, _ := o["log"].([]any)
		o["log"] = append(log, obj{"id": "pn-" + somaNewID(), "at": nowMs(), "text": text})
	})
}

func wkRemoveProjectNote(pid, nid string) {
	somaPatch("projects", pid, func(o obj) {
		log, _ := o["log"].([]any)
		o["log"] = slices.DeleteFunc(log, func(n any) bool { no, _ := n.(obj); return str(no["id"]) == nid })
	})
}

// --- time entries -----------------------------------------------------------------------------------

// wkAddEntry logs time on a project by hand (addTimeEntry).
func wkAddEntry(pid string, start, end time.Time, note string) string {
	id := "te-" + somaNewID()
	o := obj{"id": id, "projectId": pid, "start": float64(start.UnixMilli()), "end": float64(end.UnixMilli())}
	if note = strings.TrimSpace(note); note != "" {
		o["note"] = note
	}
	somaAdd("timeEntries", id, o)
	return id
}

// --- findings ---------------------------------------------------------------------------------------

// wkSetFindingStatus is patchFinding({status}): closing stamps when, reopening clears it.
func wkSetFindingStatus(id, status string) {
	somaPatch("findings", id, func(o obj) {
		was := str(o["status"]) == "resolved" || str(o["status"]) == "accepted"
		o["status"] = status
		now := status == "resolved" || status == "accepted"
		switch {
		case now && !was:
			o["closedAt"] = nowMs()
		case !now:
			delete(o, "closedAt")
		}
	})
}

// wkNewFinding is addFinding on a project.
func wkNewFinding(pid, title, severity string) {
	if title = strings.TrimSpace(title); title == "" {
		return
	}
	id := "fd-" + somaNewID()
	somaAdd("findings", id, obj{"id": id, "projectId": pid, "title": title, "severity": severity, "status": "open", "createdAt": nowMs()})
}

// --- to-dos -------------------------------------------------------------------------------------------

// wkSetSlot is setTodoSlot: a to-do given a time is that day's to-do; nil slot takes the time off.
func wkSetSlot(id, date string, start, mins int) {
	somaPatch("todos", id, func(o obj) {
		if date == "" {
			delete(o, "slot")
			return
		}
		const day, step = 24 * 60, 15
		start = min(max((start+step/2)/step*step, 0), day-step)
		if mins <= 0 {
			mins = 60
		}
		mins = min(max((mins+step/2)/step*step, step), day-start)
		o["slot"] = obj{"date": date, "start": start, "mins": mins}
		o["scope"], o["date"], o["cleared"] = "day", date, false
	})
}

// wkSetTodoDate puts a to-do on another day's list (keeping its time of day, if it has one).
func wkSetTodoDate(id, date string) {
	somaPatch("todos", id, func(o obj) {
		o["date"], o["scope"], o["cleared"] = date, "day", false
		if s, ok := o["slot"].(obj); ok {
			s["date"] = date
		}
	})
}
