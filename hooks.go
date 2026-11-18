//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	pSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
)

type kbdLLHook struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type msLLHook struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

const (
	whKeyboardLL = 13
	whMouseLL    = 14
	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
	wmLBDown     = 0x0201
	wmLBUp       = 0x0202
	wmRBDown     = 0x0204
	wmRBUp       = 0x0205
	wmMBDown     = 0x0207
	wmMBUp       = 0x0208
	wmXBDown     = 0x020B
	wmXBUp       = 0x020C
	wmHookUpdate = wmApp + 4
)

var (
	kbHook, msHook uintptr
	kbHookCb       = syscall.NewCallback(kbHookProc)
	msHookCb       = syscall.NewCallback(msHookProc)

	// hook-thread state (only touched on the UI thread)
	physMods    int
	physDown    = map[uint16]bool{}
	consumedKey = map[uint16]consumed{} // vk -> what its down event triggered
	consumedBtn = map[string]consumed{}
)

type consumed struct {
	m        Macro
	handled  bool
	suppress bool
}

func requestHookUpdate() {
	if trayHwnd != 0 {
		pPostMessageW.Call(trayHwnd, wmHookUpdate, 0, 0)
	}
}

// updateHooks installs/removes the low-level hooks as needed. UI thread only.
func updateHooks() {
	kb, ms := engine.hookNeeds()
	if capturer.active() || recorder.active() {
		kb, ms = true, true
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	if kb && kbHook == 0 {
		kbHook, _, _ = pSetWindowsHookExW.Call(whKeyboardLL, kbHookCb, inst, 0)
		if kbHook == 0 {
			logf("Klavye kancası kurulamadı")
		}
	} else if !kb && kbHook != 0 {
		pUnhookWindowsHookEx.Call(kbHook)
		kbHook = 0
		physDown = map[uint16]bool{}
		consumedKey = map[uint16]consumed{}
	}
	if ms && msHook == 0 {
		msHook, _, _ = pSetWindowsHookExW.Call(whMouseLL, msHookCb, inst, 0)
		if msHook == 0 {
			logf("Fare kancası kurulamadı")
		}
	} else if !ms && msHook != 0 {
		pUnhookWindowsHookEx.Call(msHook)
		msHook = 0
		consumedBtn = map[string]consumed{}
	}
}

func kbHookProc(code uintptr, wparam uintptr, lparam uintptr) uintptr {
	if int32(code) == 0 {
		k := *(**kbdLLHook)(unsafe.Pointer(&lparam))
		if k.ExtraInfo != injectMagic {
			if handleKey(uint16(k.VkCode), wparam == wmKeyDown || wparam == wmSysKeyDown) {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, code, wparam, lparam)
	return r
}

// handleKey returns true to swallow the event.
func handleKey(vk uint16, down bool) bool {
	recorder.key(vk, down)
	if b := modBit(vk); b != 0 {
		if down {
			physMods |= b
		} else {
			physMods &^= b
			// keep the bit if the other side is still held
			for v := range physDown {
				if v != vk && modBit(v) == b {
					physMods |= b
				}
			}
		}
		if down {
			physDown[vk] = true
		} else {
			delete(physDown, vk)
		}
		return false
	}
	if down {
		if physDown[vk] { // auto-repeat
			if c, ok := consumedKey[vk]; ok {
				return c.suppress
			}
			return false
		}
		physDown[vk] = true
		trig := comboString(physMods, vk)
		if capturer.offer(trig) {
			consumedKey[vk] = consumed{handled: true, suppress: true}
			return true
		}
		if !engine.has(trig) {
			return false
		}
		m, handled, suppress := engine.down(trig)
		if handled {
			consumedKey[vk] = consumed{m: m, handled: true, suppress: suppress}
		}
		return suppress
	}
	delete(physDown, vk)
	c, ok := consumedKey[vk]
	if !ok {
		return false
	}
	delete(consumedKey, vk)
	if c.handled && c.m.ID != "" {
		engine.up(c.m)
	}
	return c.suppress
}

func msHookProc(code uintptr, wparam uintptr, lparam uintptr) uintptr {
	if int32(code) == 0 && wparam != 0x0200 { // skip WM_MOUSEMOVE fast
		m := *(**msLLHook)(unsafe.Pointer(&lparam))
		if m.ExtraInfo != injectMagic {
			if handleMouse(uint32(wparam), m.MouseData) {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, code, wparam, lparam)
	return r
}

func handleMouse(msg uint32, data uint32) bool {
	var btn string
	var down bool
	switch msg {
	case wmLBDown, wmLBUp:
		btn, down = "left", msg == wmLBDown
	case wmRBDown, wmRBUp:
		btn, down = "right", msg == wmRBDown
	case wmMBDown, wmMBUp:
		btn, down = "middle", msg == wmMBDown
	case wmXBDown, wmXBUp:
		down = msg == wmXBDown
		if data>>16 == 1 {
			btn = "mouse4"
		} else {
			btn = "mouse5"
		}
	default:
		return false
	}
	recorder.button(btn, down)
	if btn == "left" || btn == "right" { // never usable as triggers
		return false
	}
	if down {
		if capturer.offer(btn) {
			consumedBtn[btn] = consumed{handled: true, suppress: true}
			return true
		}
		if !engine.has(btn) {
			return false
		}
		m, handled, suppress := engine.down(btn)
		if handled {
			consumedBtn[btn] = consumed{m: m, handled: true, suppress: suppress}
		}
		return suppress
	}
	c, ok := consumedBtn[btn]
	if !ok {
		return false
	}
	delete(consumedBtn, btn)
	if c.handled && c.m.ID != "" {
		engine.up(c.m)
	}
	return c.suppress
}

// ---------- trigger capture (GUI "Yakala" button) ----------

type triggerCapture struct {
	mu sync.Mutex
	ch chan string
}

var capturer = &triggerCapture{}

func (c *triggerCapture) active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ch != nil
}

func (c *triggerCapture) offer(t string) bool {
	c.mu.Lock()
	ch := c.ch
	c.ch = nil
	c.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- t
	return true
}

// wait arms the capture and blocks until a trigger arrives or timeout.
func (c *triggerCapture) wait(timeout time.Duration) (string, bool) {
	ch := make(chan string, 1)
	c.mu.Lock()
	c.ch = ch
	c.mu.Unlock()
	mgr.setCapture(true)
	requestHookUpdate()
	defer func() {
		c.mu.Lock()
		c.ch = nil
		c.mu.Unlock()
		mgr.setCapture(false)
		requestHookUpdate()
	}()
	select {
	case t := <-ch:
		return t, true
	case <-time.After(timeout):
		return "", false
	}
}

// ---------- recorder ----------

type recEvent struct {
	at   time.Time
	key  uint16
	btn  string
	down bool
}

type macroRecorder struct {
	mu        sync.Mutex
	on        bool
	withMouse bool
	events    []recEvent
}

var recorder = &macroRecorder{}

func (r *macroRecorder) active() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.on }

func (r *macroRecorder) start(withMouse bool) {
	r.mu.Lock()
	r.on, r.withMouse, r.events = true, withMouse, nil
	r.mu.Unlock()
	requestHookUpdate()
}

func (r *macroRecorder) key(vk uint16, down bool) {
	r.mu.Lock()
	if r.on {
		if n := len(r.events); !(down && n > 0 && r.events[n-1].key == vk && r.events[n-1].down) { // drop auto-repeat
			r.events = append(r.events, recEvent{at: time.Now(), key: vk, down: down})
		}
	}
	r.mu.Unlock()
}

func (r *macroRecorder) button(b string, down bool) {
	r.mu.Lock()
	if r.on && r.withMouse {
		r.events = append(r.events, recEvent{at: time.Now(), btn: b, down: down})
	}
	r.mu.Unlock()
}

// stop ends recording and converts events into actions.
func (r *macroRecorder) stop(withDelays bool) []Action {
	r.mu.Lock()
	ev := r.events
	r.on, r.events = false, nil
	r.mu.Unlock()
	requestHookUpdate()

	// drop the final left click (that was the "stop" button in the GUI)
	for len(ev) > 0 && ev[len(ev)-1].btn == "left" {
		ev = ev[:len(ev)-1]
	}
	// drop releases whose press happened before recording started
	downK := map[uint16]bool{}
	downB := map[string]bool{}
	var clean []recEvent
	for _, e := range ev {
		if e.btn != "" {
			if !e.down && !downB[e.btn] {
				continue
			}
			downB[e.btn] = e.down
		} else {
			if !e.down && !downK[e.key] {
				continue
			}
			downK[e.key] = e.down
		}
		clean = append(clean, e)
	}
	var out []Action
	var last time.Time
	for _, e := range clean {
		if withDelays && !last.IsZero() {
			if d := int(e.at.Sub(last) / time.Millisecond); d > 0 {
				out = append(out, Action{Type: "delay", Ms: d})
			}
		}
		last = e.at
		if e.btn != "" {
			t := "mouseup"
			if e.down {
				t = "mousedown"
			}
			out = append(out, Action{Type: t, Button: e.btn})
		} else {
			t := "keyup"
			if e.down {
				t = "keydown"
			}
			out = append(out, Action{Type: t, Keys: vkName(e.key)})
		}
	}
	// release anything still held at the end
	for k, d := range downK {
		if d {
			out = append(out, Action{Type: "keyup", Keys: vkName(k)})
		}
	}
	for b, d := range downB {
		if d {
			out = append(out, Action{Type: "mouseup", Button: b})
		}
	}
	return out
}
