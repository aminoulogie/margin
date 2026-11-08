package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Copying, pasting and duplicating to-dos in the Planner (2026-11-08, user: "copy paste, or
// duplicate, and a multiple selection to duplicate to the next day in the same spot; it asks
// what day and how many days: say 6, it's on the next 6 days").
//
//   - A to-do's sheet: Duplicate… (which day it starts on, how many days, every day or weekdays
//     only; the copies keep its time), Copy, Select more.
//   - Select (the Planner's header): tap to-dos to pick several, then Duplicate, Copy or Delete.
//   - Copied to-dos are pasted by tapping a time on the calendar: the first goes there, the others
//     keep their times relative to it (or go on that day's list if they had no time). The copies
//     stay ready for another paste until Cancel.
// Every duplicate, paste and delete can be undone.

// planSelected are the to-dos picked in the Planner now (drawn ringed).
var planSelected = map[string]bool{}

// planSelKey names the selection, for the kept calendar days (work_calweek.go).
func planSelKey() string {
	var ids []string
	for id := range planSelected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// syncPlanSel makes planSelected what the Planner's state says. Caller holds drawMu.
func (c *console) syncPlanSel() {
	clear(planSelected)
	if c.somaUI.planSelOn {
		for _, id := range c.somaUI.planSel {
			planSelected[id] = true
		}
	}
}

// somaDuplicateTodo makes a copy of to-do id on date (its time kept, not done), without
// writing the order (the caller does, once). Returns the new id ("" if there's no such to-do).
func somaDuplicateTodo(id, date string, start int) string {
	o := somaObj("todos#" + id)
	if o == nil {
		return ""
	}
	n := obj{}
	for k, v := range o {
		n[k] = v
	}
	nid := somaNewID()
	n["id"], n["done"], n["cleared"], n["date"] = nid, false, false, date
	delete(n, "completedAt")
	delete(n, "doneAt")
	if s, ok := o["slot"].(obj); ok {
		ns := obj{}
		for k, v := range s {
			ns[k] = v
		}
		ns["date"] = date
		if start >= 0 {
			ns["start"] = min(max(start, 0), 24*60-15)
		}
		n["slot"] = ns
		n["scope"] = "day"
	} else if str(n["scope"]) == "week" {
		n["date"] = date
	}
	somaPut("todos#"+nid, n)
	return nid
}

// somaTodoOrder writes the to-dos' order with any new ones at the end.
func somaTodoOrder() {
	var order []string
	for _, t := range somaList("todos") {
		order = append(order, str(t["id"]))
	}
	somaPut("todos@order", order)
}

// todoDay is the day a to-do is on (its time's day, else its list's).
func todoDay(o obj) string {
	if s, ok := o["slot"].(obj); ok && str(s["date"]) != "" {
		return str(s["date"])
	}
	return str(o["date"])
}

// dupDays are the days the copies go on: n days from first (weekdays only: Saturdays and
// Sundays skipped, still n copies).
func dupDays(first string, n int, weekdays bool) []string {
	var out []string
	for d := calDate(first); len(out) < n; d = d.AddDate(0, 0, 1) {
		if weekdays && (d.Weekday() == time.Saturday || d.Weekday() == time.Sunday) {
			continue
		}
		out = append(out, somaDate(d))
	}
	return out
}

// removeTodosUndo takes the to-dos ids away and offers to bring them back.
func (c *console) removeTodosUndo(label string, ids []string) {
	type gone struct {
		o  obj
		at int
	}
	var all []gone
	for _, id := range ids {
		if o, at := somaRemove("todos", id); o != nil {
			all = append(all, gone{o, at})
		}
	}
	if len(all) == 0 {
		return
	}
	c.offerUndo(label, func() {
		for i := len(all) - 1; i >= 0; i-- {
			somaRestore("todos", all[i].o, all[i].at)
		}
	})
}

// madeUndo offers to take back the copies just made.
func (c *console) madeUndo(label string, made []string) {
	if len(made) == 0 {
		return
	}
	c.offerUndo(label, func() {
		for _, id := range made {
			somaRemove("todos", id)
		}
	})
}

// nOf is "1 copy", "6 copies".
func nOf(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	if one == "copy" {
		return fmt.Sprintf("%d copies", n)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// planTapHook handles a tap while picking to-dos (it toggles them) or with copies to paste (a
// time on the calendar pastes them there). True if it took the tap. Caller holds drawMu.
func (c *console) planTapHook(id string) bool {
	su := &c.somaUI
	if su.work != "planner" {
		return false
	}
	if su.planSelOn && su.sheet == "" {
		if t, ok := strings.CutPrefix(id, "wk:sheet-open:task:"); ok {
			if i := slices.Index(su.planSel, t); i >= 0 {
				su.planSel = slices.Delete(su.planSel, i, i+1)
			} else {
				su.planSel = append(slices.Clone(su.planSel), t)
			}
			return true
		}
		if strings.HasPrefix(id, "wk:slotadd:") {
			return true // picking: a free time does nothing
		}
	}
	if len(su.planClip) > 0 && su.sheet == "" && !su.planSelOn {
		if arg, ok := strings.CutPrefix(id, "wk:slotadd:"); ok {
			date, m, _ := strings.Cut(arg, ":")
			at, _ := strconv.Atoi(m)
			c.pasteTodos(date, at)
			return true
		}
	}
	return false
}

// pasteTodos pastes the copied to-dos on date, the first at minute at.
func (c *console) pasteTodos(date string, at int) {
	su := &c.somaUI
	first := -1
	for _, id := range su.planClip {
		if s, ok := somaObj("todos#" + id)["slot"].(obj); ok {
			if st := int(num(s["start"])); first < 0 || st < first {
				first = st
			}
		}
	}
	var made []string
	for _, id := range su.planClip {
		o := somaObj("todos#" + id)
		if o == nil {
			continue
		}
		start := at
		if s, ok := o["slot"].(obj); ok {
			start = at + int(num(s["start"])) - first
		}
		if nid := somaDuplicateTodo(id, date, start); nid != "" {
			made = append(made, nid)
		}
	}
	somaTodoOrder()
	c.madeUndo("Pasted "+nOf(len(made), "to-do")+" · "+wkDate(date), made)
}

func init() {
	// The to-do's own sheet gets Duplicate…, Copy and Select more (added by planTaskExtras).
	wkActions["dupopen"] = func(c *console, ids string) {
		su := &c.somaUI
		su.dupIDs = strings.Split(ids, ",")
		day := somaToday()
		if o := somaObj("todos#" + su.dupIDs[0]); o != nil && todoDay(o) != "" {
			day = todoDay(o)
		}
		su.dupFrom, su.dupDays, su.dupWeekdays = wkAddDays(day, 1), 1, false
		c.openSheet("dup:")
	}
	wkSheets["dup"] = func(k *wk, _ string) *wkSheet {
		su := &k.c.somaUI
		n := len(su.dupIDs)
		title := "Duplicate"
		if n == 1 {
			title = "Duplicate “" + str(somaObj("todos#" + su.dupIDs[0])["text"]) + "”"
		} else {
			title = "Duplicate " + nOf(n, "to-do")
		}
		days := dupDays(su.dupFrom, su.dupDays, su.dupWeekdays)
		span := wkDate(days[0])
		if len(days) > 1 {
			span += " – " + wkDate(days[len(days)-1])
		}
		return &wkSheet{title: title, sub: "Same time, on the days you pick", rows: []wkRow{
			{id: "wk:dupday", label: "Starting", icon: "calendar", detail: wkDate(su.dupFrom)},
			{kind: "stepper", label: "How many days", detail: nOf(su.dupDays, "day"), minus: "wk:dupn:-1", plus: "wk:dupn:1"},
			{kind: "seg", label: "Which days", opts: []wkOpt{
				{id: "wk:dupwk:0", label: "Every day", sel: !su.dupWeekdays},
				{id: "wk:dupwk:1", label: "Weekdays", sel: su.dupWeekdays}}},
			{kind: "text", detail: span},
			{id: "wk:dupgo", label: fmt.Sprintf("Duplicate · %s", nOf(n*len(days), "copy")), icon: "copy", col: wkLime},
		}}
	}
	wkActions["dupday"] = func(c *console, _ string) {
		c.wkPickDate("dupday:", c.somaUI.dupFrom, "Starting which day")
	}
	wkDateRequired["dupday"] = true
	wkDateSetters["dupday"] = func(c *console, _, date string) {
		if date != "" {
			c.somaUI.dupFrom = date
		}
	}
	wkActions["dupn"] = func(c *console, arg string) {
		d, _ := strconv.Atoi(arg)
		c.somaUI.dupDays = min(max(c.somaUI.dupDays+d, 1), 60)
		c.somaUI.keep = true
	}
	wkActions["dupwk"] = func(c *console, arg string) {
		c.somaUI.dupWeekdays = arg == "1"
		c.somaUI.keep = true
	}
	wkActions["dupgo"] = func(c *console, _ string) {
		su := &c.somaUI
		days := dupDays(su.dupFrom, su.dupDays, su.dupWeekdays)
		var made []string
		for _, d := range days {
			for _, id := range su.dupIDs {
				if nid := somaDuplicateTodo(id, d, -1); nid != "" {
					made = append(made, nid)
				}
			}
		}
		somaTodoOrder()
		su.dupIDs, su.planSelOn, su.planSel = nil, false, nil
		su.sheet, su.sheetStack = "", nil
		c.madeUndo(fmt.Sprintf("%s made · %s", nOf(len(made), "copy"), nOf(len(days), "day")), made)
	}

	wkActions["tcopy"] = func(c *console, ids string) {
		su := &c.somaUI
		su.planClip = strings.Split(ids, ",")
		su.planSelOn, su.planSel = false, nil
		su.sheet, su.sheetStack = "", nil
		c.toastMsg("Copied " + nOf(len(su.planClip), "to-do") + " · tap a time to paste")
	}
	wkActions["clipclear"] = func(c *console, _ string) { c.somaUI.planClip = nil }

	wkActions["selstart"] = func(c *console, id string) {
		su := &c.somaUI
		su.planSelOn, su.planSel, su.planClip = true, nil, nil
		if id != "" {
			su.planSel = []string{id}
		}
		su.sheet, su.sheetStack = "", nil
	}
	wkActions["seldone"] = func(c *console, _ string) { c.somaUI.planSelOn, c.somaUI.planSel = false, nil }
	wkActions["seldup"] = func(c *console, _ string) {
		if len(c.somaUI.planSel) > 0 {
			wkActions["dupopen"](c, strings.Join(c.somaUI.planSel, ","))
		}
	}
	wkActions["selcopy"] = func(c *console, _ string) {
		if len(c.somaUI.planSel) > 0 {
			wkActions["tcopy"](c, strings.Join(c.somaUI.planSel, ","))
		}
	}
	wkActions["seldel"] = func(c *console, _ string) {
		su := &c.somaUI
		if len(su.planSel) == 0 {
			return
		}
		ids := su.planSel
		su.planSelOn, su.planSel = false, nil
		c.removeTodosUndo(nOf(len(ids), "to-do")+" deleted", ids)
	}
}

// planTaskExtras are the rows a to-do's sheet gets for copying.
func planTaskExtras(id string) []wkRow {
	return []wkRow{
		{id: "wk:dupopen:" + id, label: "Duplicate…", icon: "copy"},
		{id: "wk:tcopy:" + id, label: "Copy", icon: "files", detail: "then tap a time"},
		{id: "wk:selstart:" + id, label: "Select more", icon: "list-checks"},
	}
}

// planSelHeader draws the Planner's header buttons while picking to-dos or with copies to
// paste, in place of the usual ones; the subtitle says what to do. False: the usual header.
func (k *wk) planSelHeader() (sub string, ok bool) {
	su := &k.c.somaUI
	switch {
	case su.planSelOn:
		x := k.primary("wk:seldone", "check", "Done", wkX1)
		if len(su.planSel) > 0 {
			x = k.outline("wk:seldel", "trash-2", "Delete", x-13, 103, 63)
			x = k.outline("wk:selcopy", "files", "Copy", x-13, 103, 63)
			k.outline("wk:seldup", "copy", "Duplicate", x-13, 103, 63)
		}
		if len(su.planSel) == 0 {
			return "Tap to-dos to pick them", true
		}
		return nOf(len(su.planSel), "to-do") + " picked · tap more, or Duplicate", true
	case len(su.planClip) > 0:
		k.outline("wk:clipclear", "x", "Cancel", wkX1, 103, 63)
		return nOf(len(su.planClip), "to-do") + " copied · tap a time on the calendar to paste", true
	}
	return "", false
}
