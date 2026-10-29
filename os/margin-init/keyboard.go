package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"margin-init/fonts"
	"margin-init/ui"
)

// The on-screen keyboard: a terminal-friendly layout under the console. A key lights up when
// touched, follows the finger while it moves, and is typed when the finger lifts (the first
// touch frame sometimes carries a bogus coordinate, so committing on lift is also more
// reliable). Holding a repeatable key (backspace, arrows) repeats it.

const (
	kbRowH   = 100 // logical pixels per key row
	kbGap    = 8   // space between keys
	kbPad    = 8   // around the keyboard
	kbRows   = 6
	kbHeight = kbRows*kbRowH + 2*kbPad

	repeatDelay = 280 * time.Millisecond // before a held key repeats
	repeatEvery = 45 * time.Millisecond  // then this often, speeding up to repeatFast
	repeatFast  = 15 * time.Millisecond

	// Holding ⌫: one character as it's touched, then after a moment one every 10 ms,
	// steadily, until the finger lifts.
	deleteDelay = 300 * time.Millisecond
	deleteEvery = 10 * time.Millisecond

	trackStep = 22 // pixels of finger travel per character, sliding on the space bar
)

type keyAction int

const (
	actType keyAction = iota
	actShift
	actCtrl
	actAlt
	actLayer // letters <-> symbols
	actHide
)

type kbKey struct {
	label, shiftLabel string
	out, shiftOut     string
	w                 float64 // width in key units (a row is 10 units)
	act               keyAction
	repeat            bool
	r                 image.Rectangle // keyboard-local, filled in by layout
}

// k makes a typing key that sends its label; shift sends shiftLabel.
func k(label, shiftLabel string) *kbKey {
	return &kbKey{label: label, shiftLabel: shiftLabel, out: label, shiftOut: shiftLabel, w: 1}
}

// special makes a named key that sends out.
func special(label, out string, w float64, act keyAction, repeat bool) *kbKey {
	return &kbKey{label: label, shiftLabel: label, out: out, shiftOut: out, w: w, act: act, repeat: repeat}
}

func letters(s string) []*kbKey {
	var ks []*kbKey
	for _, r := range s {
		ks = append(ks, k(string(r), strings.ToUpper(string(r))))
	}
	return ks
}

func pairs(s, shifted string) []*kbKey {
	var ks []*kbKey
	sr := []rune(shifted)
	for i, r := range []rune(s) {
		ks = append(ks, k(string(r), string(sr[i])))
	}
	return ks
}

// kbLayout returns the two layers (letters, symbols). Rows 0, 4 and 5 are shared.
func kbLayout() [2][][]*kbKey {
	top := []*kbKey{
		special("esc", "\x1b", 1.25, actType, false),
		special("tab", "\t", 1.25, actType, false),
		special("ctrl", "", 1.25, actCtrl, false),
		special("alt", "", 1.25, actAlt, false),
		special("←", "\x1b[D", 1.25, actType, true),
		special("↓", "\x1b[B", 1.25, actType, true),
		special("↑", "\x1b[A", 1.25, actType, true),
		special("→", "\x1b[C", 1.25, actType, true),
	}
	bksp := special("bksp", "\x7f", 2, actType, true)
	enter := special("enter", "\r", 1, actType, false)
	row4 := append(append([]*kbKey{special("shift", "", 1, actShift, false)}, letters("zxcvbnm")...), bksp)
	row5 := []*kbKey{
		special("sym", "", 1.5, actLayer, false),
		k(",", "<"), k("-", "_"),
		special("space", " ", 3, actType, false),
		k(".", ">"), k("/", "?"),
		special("hide", "", 1.5, actHide, false),
	}
	lettersLayer := [][]*kbKey{
		top,
		pairs("1234567890", "!@#$%^&*()"),
		letters("qwertyuiop"),
		append(letters("asdfghjkl"), enter),
		row4,
		row5,
	}
	symbolsLayer := [][]*kbKey{
		top,
		pairs("!@#$%^&*()", "1234567890"),
		pairs("~`|\\=+_[]&", "~`|\\=+_[]&"),
		append(pairs("<>:;\"'?{}", "<>:;\"'?{}"), enter),
		row4,
		row5,
	}
	return [2][][]*kbKey{lettersLayer, symbolsLayer}
}

// textLayout is the keyboard for fields on pages (a search, a to-do, a Wi-Fi password), as
// on an iPad: no terminal keys, a row of numbers, and space, comma, full stop and apostrophe
// at hand. Five rows.
func textLayout() [2][][]*kbKey {
	bksp := special("bksp", "\x7f", 2, actType, true)
	enter := special("enter", "\r", 1, actType, false)
	shift := special("shift", "", 1, actShift, false)
	bottom := []*kbKey{
		special("sym", "", 1.5, actLayer, false),
		k(",", "!"),
		special("space", " ", 4, actType, false),
		k(".", "?"), k("'", "\""),
		special("hide", "", 1.5, actHide, false),
	}
	return [2][][]*kbKey{{
		pairs("1234567890", "!@#$%^&*()"),
		letters("qwertyuiop"),
		append(letters("asdfghjkl"), enter),
		append(append([]*kbKey{shift}, letters("zxcvbnm")...), bksp),
		bottom,
	}, {
		pairs("1234567890", "!@#$%^&*()"),
		pairs("-/:;()$&@\"", "[]{}#%^*+="),
		append(pairs(".,?!'#%+=", "_\\|~<>€£¥"), enter),
		append(append([]*kbKey{shift}, pairs("_\\|~<>*", "_\\|~<>*")...), bksp),
		bottom,
	}}
}

// keyboard state. Methods that draw expect the caller to hold drawMu.
type keyboard struct {
	mu        sync.Mutex
	s         *Screen
	y0        int // top of the keyboard on the logical screen
	layers    [2][][]*kbKey
	layer     int
	shift     bool // one-shot
	ctrl      bool // one-shot
	alt       bool // one-shot
	visible   bool
	pressed   map[int]*kbKey // touch slot -> key under it
	repeats   map[int]chan struct{}
	repeated  map[int]bool
	track     map[int]int // slot -> x where the space bar's trackpad last moved the cursor
	downX     map[int]int
	tracked   map[int]bool
	typed     map[int]bool // fingers whose key already typed, on touching it
	send      func([]byte)
	onHide    func(visible bool) // console relayout
	big       font.Face
	small     font.Face
	dark      bool     // iPadOS's dark keyboard (the terminal)
	themed    bool     // dark in Dark Mode, light in Light Mode (the fields' keyboard)
	height    int      // from y0 to the screen's bottom
	drawn     []uint32 // the keyboard as last drawn, a column of the screen at a time
	drawnLook string   // and the look it was drawn in
}

// newKeyboard is the terminal's keyboard (esc, tab, ctrl, alt, arrows).
func newKeyboard(s *Screen, send func([]byte), onHide func(bool)) (*keyboard, error) {
	return newKeyboardOf(s, kbLayout(), send, onHide)
}

// newTextKeyboard is the keyboard for typing into a page's fields.
func newTextKeyboard(s *Screen, send func([]byte), onHide func(bool)) (*keyboard, error) {
	kb, err := newKeyboardOf(s, textLayout(), send, onHide)
	if kb != nil {
		kb.themed = true
	}
	return kb, err
}

func newKeyboardOf(s *Screen, layers [2][][]*kbKey, send func([]byte), onHide func(bool)) (*keyboard, error) {
	// The system font, with DejaVu for the key symbols (⇧ ⌫ ⌨).
	big := textFace("inter", fonts.InterRegular, false, 42)
	small := textFace("inter", fonts.InterRegular, false, 28)
	h := len(layers[0])*kbRowH + 2*kbPad
	kb := &keyboard{s: s, y0: s.H - h, height: h, layers: layers, visible: true,
		pressed: map[int]*kbKey{}, repeats: map[int]chan struct{}{}, repeated: map[int]bool{}, typed: map[int]bool{},
		track: map[int]int{}, tracked: map[int]bool{}, downX: map[int]int{},
		send: send, onHide: onHide, big: big, small: small}
	kb.layout()
	return kb, nil
}

// layout places every key of both layers in keyboard-local coordinates.
func (kb *keyboard) layout() {
	unit := float64(kb.s.W-2*kbPad) / 10
	for _, layer := range kb.layers {
		for row, keys := range layer {
			x := float64(kbPad)
			y := kbPad + row*kbRowH
			for _, key := range keys {
				w := key.w * unit
				key.r = image.Rect(int(x)+kbGap/2, y+kbGap/2, int(x+w)-kbGap/2, y+kbRowH-kbGap/2)
				x += w
			}
		}
	}
}

// keyAt returns the key at logical screen coordinates, or nil.
func (kb *keyboard) keyAt(x, y int) *kbKey {
	p := image.Pt(x, y-kb.y0)
	for _, keys := range kb.layers[kb.layer] {
		for _, key := range keys {
			if p.In(key.r) {
				return key
			}
		}
	}
	return nil
}

// iPadOS keyboard colours: light and dark.
type kbColours struct{ bg, key, special, pressed, text, shadow, active color.RGBA }

var (
	kbLight = kbColours{rgb(0xd1d4db), rgb(0xffffff), rgb(0xabb0ba), rgb(0xabb0ba), rgb(0x000000), rgb(0x898a8d), rgb(0x007aff)}
	kbDarkC = kbColours{rgb(0x2b2b2e), rgb(0x6b6b6f), rgb(0x46464a), rgb(0x8c8c90), rgb(0xffffff), rgb(0x0f0f10), rgb(0x0a84ff)}
)

func (kb *keyboard) colours() kbColours {
	if kb.dark || kb.themed && apDark {
		return kbDarkC
	}
	return kbLight
}

// keyLabels: iPadOS's names and symbols for the named keys.
var keyLabels = map[string]string{"bksp": "⌫", "shift": "⇧", "enter": "return", "hide": "⌨", "sym": ".?123"}

func (kb *keyboard) active(key *kbKey) bool {
	switch key.act {
	case actShift:
		return kb.shift
	case actCtrl:
		return kb.ctrl
	case actAlt:
		return kb.alt
	case actLayer:
		return kb.layer == 1
	}
	return false
}

// drawKey paints one key. Caller holds drawMu.
func (kb *keyboard) drawKey(key *kbKey, pressed bool) {
	r := key.r
	col := kb.colours()
	img := image.NewRGBA(image.Rect(0, 0, r.Dx()+kbGap, r.Dy()+kbGap))
	ui.Fill(img, img.Rect, col.bg)
	bg, fg := col.key, col.text
	if key.act != actType || utf8.RuneCountInString(key.label) > 1 { // named keys: esc, ⌫...
		bg = col.special
	}
	if kb.active(key) {
		bg, fg = col.key, col.active
	}
	if pressed {
		bg = col.pressed
	}
	inner := image.Rect(kbGap/2, kbGap/2, kbGap/2+r.Dx(), kbGap/2+r.Dy())
	ui.RoundRect(img, image.Rect(inner.Min.X, inner.Min.Y+2, inner.Max.X, inner.Max.Y+2), 12, col.shadow)
	ui.RoundRect(img, inner, 12, bg)
	label := key.label
	if kb.shift && key.act == actType {
		label = key.shiftLabel
	}
	if l, ok := keyLabels[label]; ok {
		label = l
	}
	if key.act == actLayer && kb.layer == 1 {
		label = "ABC"
	}
	face := kb.big
	if len([]rune(label)) > 1 || label == "⌨" {
		face = kb.small
	}
	m := face.Metrics()
	w := font.MeasureString(face, label).Ceil()
	cx, cy := inner.Min.X+inner.Dx()/2, inner.Min.Y+inner.Dy()/2
	d := font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: face,
		Dot: fixed.P(cx-w/2, cy+(m.Ascent.Ceil()-m.Descent.Ceil())/2)}
	d.DrawString(label)
	kb.s.blitRGBA(img, r.Min.X-kbGap/2, kb.y0+r.Min.Y-kbGap/2)
}

// draw paints the whole keyboard and flushes. Caller holds drawMu.
func (kb *keyboard) draw() {
	if !kb.visible {
		return
	}
	// Drawn once per look (layer, shift, ctrl, alt, colours): then it's put back as it was
	// sent to the panel, one copy per screen column, instead of every key drawn again.
	s := kb.s
	look := fmt.Sprint(kb.layer, kb.shift, kb.ctrl, kb.alt, kb.colours().bg, kb.y0, kb.height, s.W, s.fbW, s.fbH)
	cacheable := s.rot == Rot90 && s.bpp == 32 && s.W == s.fbH && kb.y0+kb.height <= s.fbW
	if cacheable && kb.drawn != nil && kb.drawnLook == look {
		h := kb.height
		for x := 0; x < s.W; x++ {
			fy := s.fbH - 1 - x
			copy(words(s.buf[fy*s.stride:])[kb.y0:kb.y0+h], kb.drawn[x*h:(x+1)*h])
		}
		s.markRows(0, s.fbH-1)
		s.Flush()
		return
	}
	bg := image.NewRGBA(image.Rect(0, 0, kb.s.W, kb.height))
	ui.Fill(bg, bg.Rect, kb.colours().bg)
	kb.s.blitRGBA(bg, 0, kb.y0)
	for _, keys := range kb.layers[kb.layer] {
		for _, key := range keys {
			kb.drawKey(key, false)
		}
	}
	if cacheable {
		h := kb.height
		if len(kb.drawn) != s.W*h {
			kb.drawn = make([]uint32, s.W*h)
		}
		for x := 0; x < s.W; x++ {
			fy := s.fbH - 1 - x
			copy(kb.drawn[x*h:(x+1)*h], words(s.buf[fy*s.stride:])[kb.y0:kb.y0+h])
		}
		kb.drawnLook = look
	}
	kb.s.Flush()
}

// output is what a typing key sends right now, with shift/ctrl/alt applied.
func (kb *keyboard) output(key *kbKey) []byte {
	out := key.out
	if kb.shift {
		out = key.shiftOut
	}
	if kb.ctrl && len(out) == 1 {
		c := out[0]
		switch {
		case c >= 'a' && c <= 'z':
			out = string(c - 'a' + 1)
		case c >= '@' && c <= '_': // @ A..Z [ \ ] ^ _
			out = string(c - '@')
		case c == ' ':
			out = "\x00"
		}
	}
	if kb.alt {
		out = "\x1b" + out
	}
	return []byte(out)
}

// commit performs a key: types it, or toggles a modifier/layer/visibility.
// Caller holds drawMu (it redraws).
func (kb *keyboard) commit(key *kbKey) {
	switch key.act {
	case actShift:
		kb.shift = !kb.shift
		kb.draw()
	case actCtrl:
		kb.ctrl = !kb.ctrl
		kb.drawKey(key, false)
		kb.s.Flush()
	case actAlt:
		kb.alt = !kb.alt
		kb.drawKey(key, false)
		kb.s.Flush()
	case actLayer:
		kb.layer = 1 - kb.layer
		kb.draw()
	case actHide:
		kb.visible = false
		kb.onHide(false)
	default:
		kb.send(kb.output(key))
		if kb.shift || kb.ctrl || kb.alt {
			kb.shift, kb.ctrl, kb.alt = false, false, false
			kb.draw()
		}
	}
}

// touch handles one finger's frame. Caller holds drawMu.
func (kb *keyboard) touch(p TouchPoint) {
	if !kb.visible {
		if p.Up { // any tap shows the keyboard again
			kb.visible = true
			kb.onHide(true)
		}
		return
	}
	prev := kb.pressed[p.Slot]
	// The space bar is a trackpad, as on iOS: hold it and slide, and the cursor follows.
	if prev != nil && prev.out == " " && prev.act == actType {
		if p.Moved {
			if _, on := kb.track[p.Slot]; !on && abs(p.X-kb.downX[p.Slot]) > trackStep {
				kb.track[p.Slot] = kb.downX[p.Slot]
				kb.tracked[p.Slot] = true
			}
			if last, on := kb.track[p.Slot]; on {
				for ; p.X-last >= trackStep; last += trackStep {
					kb.send([]byte("\x1b[C"))
				}
				for ; last-p.X >= trackStep; last -= trackStep {
					kb.send([]byte("\x1b[D"))
				}
				kb.track[p.Slot] = last
				return
			}
		}
		if p.Up && kb.tracked[p.Slot] {
			delete(kb.track, p.Slot)
			delete(kb.tracked, p.Slot)
			delete(kb.pressed, p.Slot)
			kb.drawKey(prev, false)
			kb.s.Flush()
			return // slid: no space typed
		}
	}
	if p.Down {
		kb.downX[p.Slot] = p.X
		delete(kb.track, p.Slot)
		delete(kb.tracked, p.Slot)
	}
	switch {
	case p.Down || p.Moved:
		key := kb.keyAt(p.X, p.Y)
		if key != prev {
			if prev != nil {
				kb.drawKey(prev, false)
				kb.stopRepeat(p.Slot)
			}
			if key != nil {
				kb.drawKey(key, true)
				if key.repeat {
					kb.startRepeat(p.Slot, key)
				}
			}
			kb.s.Flush()
			if key == nil {
				delete(kb.pressed, p.Slot)
			} else {
				kb.pressed[p.Slot] = key
			}
			// A key types the moment it's touched, not when the finger lifts; the space
			// bar waits, as sliding on it moves the cursor instead.
			if p.Down && key != nil && key.act != actHide && !(key.act == actType && key.out == " ") {
				kb.typed[p.Slot] = true
				kb.commit(key)
			}
		}
	case p.Up:
		key := kb.keyAt(p.X, p.Y)
		if key == nil {
			key = prev
		}
		delete(kb.pressed, p.Slot)
		repeated := kb.repeated[p.Slot] || kb.typed[p.Slot]
		delete(kb.typed, p.Slot)
		kb.stopRepeat(p.Slot)
		if prev != nil {
			kb.drawKey(prev, false)
			kb.s.Flush()
		}
		if key != nil && !repeated {
			kb.commit(key)
		}
	}
}

// startRepeat types key again and again while the finger stays on it.
func (kb *keyboard) startRepeat(slot int, key *kbKey) {
	stop := make(chan struct{})
	kb.repeats[slot] = stop
	kb.repeated[slot] = false
	if key.out == "\x7f" {
		go kb.repeatDelete(slot, key, stop)
		return
	}
	go func() {
		select {
		case <-stop:
			return
		case <-time.After(repeatDelay):
		}
		every := repeatEvery
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			drawMu.Lock()
			select {
			case <-stop: // lifted while we waited for the lock
				drawMu.Unlock()
				return
			default:
			}
			kb.repeated[slot] = true
			kb.send(kb.output(key))
			drawMu.Unlock()
			select {
			case <-stop:
				return
			case <-t.C:
			}
			if every > repeatFast { // the longer it's held, the faster it goes
				every = max(every*9/10, repeatFast)
				t.Reset(every)
			}
		}
	}()
}

// repeatDelete deletes at a steady deleteEvery from deleteDelay on. The characters due are
// counted from the clock, and sent together when a redraw took longer than one step, so
// the speed stays the same however slow the screen behind is.
func (kb *keyboard) repeatDelete(slot int, key *kbKey, stop chan struct{}) {
	select {
	case <-stop:
		return
	case <-time.After(deleteDelay):
	}
	start, sent := time.Now(), 0
	t := time.NewTicker(deleteEvery)
	defer t.Stop()
	for {
		drawMu.Lock()
		select {
		case <-stop: // lifted while we waited for the lock
			drawMu.Unlock()
			return
		default:
		}
		kb.repeated[slot] = true
		due := int(time.Since(start)/deleteEvery) + 1
		if n := min(due-sent, 50); n > 0 {
			kb.send(bytes.Repeat(kb.output(key), n))
			sent = due
		}
		drawMu.Unlock()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (kb *keyboard) stopRepeat(slot int) {
	if stop, ok := kb.repeats[slot]; ok {
		close(stop)
		delete(kb.repeats, slot)
	}
	delete(kb.repeated, slot)
}
