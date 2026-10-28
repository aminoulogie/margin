package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// A very small X11 client, enough to drive a desktop app with the tablet's touches and
// keyboard: the connection, XTEST's fake input (pointer moves, buttons, keys), the keyboard
// mapping (to turn a character into a key and shift), and the window tree (to make an app's
// windows fill the screen, as there's no window manager). Little-endian, no authentication
// (Xvfb runs with -ac on a socket only Alpine's root can reach).

type xconn struct {
	c         net.Conn
	seq       uint16
	root      uint32
	w, h      int
	xtest     byte
	minKey    byte
	perKey    int
	keysyms   []uint32 // (keycode-minKey)*perKey + column
	maximised map[uint32]bool
}

var le = binary.LittleEndian

func pad4(n int) int { return (4 - n%4) % 4 }

func xdial(path string) (*xconn, error) {
	c, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return nil, err
	}
	x := &xconn{c: c, maximised: map[uint32]bool{}}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write([]byte{'l', 0, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		c.Close()
		return nil, err
	}
	head := make([]byte, 8)
	if _, err := io.ReadFull(c, head); err != nil {
		c.Close()
		return nil, err
	}
	body := make([]byte, int(le.Uint16(head[6:]))*4)
	if _, err := io.ReadFull(c, body); err != nil {
		c.Close()
		return nil, err
	}
	if head[0] != 1 {
		c.Close()
		return nil, fmt.Errorf("X refused the connection: %q", body)
	}
	vendor := int(le.Uint16(body[16:]))
	formats := int(body[21])
	x.minKey = body[26]
	maxKey := body[27]
	scr := body[32+vendor+pad4(vendor)+8*formats:]
	x.root = le.Uint32(scr)
	x.w, x.h = int(le.Uint16(scr[20:])), int(le.Uint16(scr[22:]))
	// XTEST.
	name := "XTEST"
	req := make([]byte, 8+len(name)+pad4(len(name)))
	req[0] = 98
	le.PutUint16(req[2:], uint16(len(req)/4))
	le.PutUint16(req[4:], uint16(len(name)))
	copy(req[8:], name)
	rep, err := x.call(req)
	if err != nil {
		c.Close()
		return nil, err
	}
	if rep[8] == 0 {
		c.Close()
		return nil, errors.New("the X server has no XTEST")
	}
	x.xtest = rep[9]
	// The keyboard.
	count := int(maxKey) - int(x.minKey) + 1
	rep, err = x.call([]byte{101, 0, 2, 0, x.minKey, byte(count), 0, 0})
	if err != nil {
		c.Close()
		return nil, err
	}
	x.perKey = int(rep[1])
	n := int(le.Uint32(rep[4:]))
	for i := 0; i < n; i++ {
		x.keysyms = append(x.keysyms, le.Uint32(rep[32+4*i:]))
	}
	return x, nil
}

func (x *xconn) close() { x.c.Close() }

// send writes a request that has no reply.
func (x *xconn) send(req []byte) error {
	x.seq++
	x.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := x.c.Write(req)
	return err
}

// call writes a request and reads its reply (skipping errors and events from before).
func (x *xconn) call(req []byte) ([]byte, error) {
	if err := x.send(req); err != nil {
		return nil, err
	}
	x.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer x.c.SetReadDeadline(time.Time{})
	for {
		pkt := make([]byte, 32)
		if _, err := io.ReadFull(x.c, pkt); err != nil {
			return nil, err
		}
		switch pkt[0] {
		case 1:
			extra := make([]byte, int(le.Uint32(pkt[4:]))*4)
			if _, err := io.ReadFull(x.c, extra); err != nil {
				return nil, err
			}
			if le.Uint16(pkt[2:]) == x.seq {
				return append(pkt, extra...), nil
			}
		case 0:
			if le.Uint16(pkt[2:]) == x.seq {
				return nil, fmt.Errorf("X error %d", pkt[1])
			}
		}
	}
}

// fake sends one XTEST input: 2/3 key press/release, 4/5 button press/release, 6 motion.
func (x *xconn) fake(typ, detail byte, rx, ry int) error {
	req := make([]byte, 36)
	req[0], req[1] = x.xtest, 2
	le.PutUint16(req[2:], 9)
	req[4], req[5] = typ, detail
	le.PutUint32(req[12:], x.root)
	le.PutUint16(req[24:], uint16(int16(rx)))
	le.PutUint16(req[26:], uint16(int16(ry)))
	return x.send(req)
}

func (x *xconn) move(px, py int) error { return x.fake(6, 0, px, py) }

func (x *xconn) button(b byte, down bool) error {
	if down {
		return x.fake(4, b, 0, 0)
	}
	return x.fake(5, b, 0, 0)
}

// keycode finds the key for a keysym and whether it takes shift.
func (x *xconn) keycode(sym uint32) (byte, bool, bool) {
	for col := 0; col < min(x.perKey, 2); col++ {
		for i := 0; i*x.perKey+col < len(x.keysyms); i++ {
			if x.keysyms[i*x.perKey+col] == sym {
				return x.minKey + byte(i), col == 1, true
			}
		}
	}
	return 0, false, false
}

// key types one keysym, with shift (and control) held as it needs.
func (x *xconn) key(sym uint32, ctrl bool) {
	kc, shift, ok := x.keycode(sym)
	if !ok {
		return
	}
	var mods []byte
	if shift {
		if k, _, ok := x.keycode(0xffe1); ok {
			mods = append(mods, k)
		}
	}
	if ctrl {
		if k, _, ok := x.keycode(0xffe3); ok {
			mods = append(mods, k)
		}
	}
	for _, m := range mods {
		x.fake(2, m, 0, 0)
	}
	x.fake(2, kc, 0, 0)
	x.fake(3, kc, 0, 0)
	for _, m := range mods {
		x.fake(3, m, 0, 0)
	}
}

// typeBytes types what the on-screen keyboard sent: characters, Return, Backspace, Tab,
// Escape, the arrows, and control letters.
func (x *xconn) typeBytes(b []byte) {
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == 0x1b && i+2 < len(b) && b[i+1] == '[':
			if sym, ok := map[byte]uint32{'A': 0xff52, 'B': 0xff54, 'C': 0xff53, 'D': 0xff51}[b[i+2]]; ok {
				x.key(sym, false)
			}
			i += 2
		case c == 0x1b:
			x.key(0xff1b, false)
		case c == '\r' || c == '\n':
			x.key(0xff0d, false)
		case c == 0x7f || c == 0x08:
			x.key(0xff08, false)
		case c == '\t':
			x.key(0xff09, false)
		case c >= 1 && c <= 26:
			x.key(uint32('a'+c-1), true)
		case c >= 0x20 && c < 0x7f:
			x.key(uint32(c), false)
		}
	}
}

// fillScreen makes each top-level window that's shown (and not a menu or a tooltip) fill
// the screen once.
func (x *xconn) fillScreen() {
	req := make([]byte, 8)
	req[0] = 15
	le.PutUint16(req[2:], 2)
	le.PutUint32(req[4:], x.root)
	rep, err := x.call(req)
	if err != nil {
		return
	}
	n := int(le.Uint16(rep[16:]))
	for i := 0; i < n; i++ {
		w := le.Uint32(rep[32+4*i:])
		if x.maximised[w] {
			continue
		}
		attr := make([]byte, 8)
		attr[0] = 3
		le.PutUint16(attr[2:], 2)
		le.PutUint32(attr[4:], w)
		a, err := x.call(attr)
		if err != nil || a[26] != 2 || a[27] != 0 { // not viewable, or override-redirect
			continue
		}
		x.maximised[w] = true
		cfg := make([]byte, 28)
		cfg[0] = 12
		le.PutUint16(cfg[2:], 7)
		le.PutUint32(cfg[4:], w)
		le.PutUint16(cfg[8:], 0xf) // x, y, width, height
		le.PutUint32(cfg[12:], 0)
		le.PutUint32(cfg[16:], 0)
		le.PutUint32(cfg[20:], uint32(x.w))
		le.PutUint32(cfg[24:], uint32(x.h))
		x.send(cfg)
	}
}
