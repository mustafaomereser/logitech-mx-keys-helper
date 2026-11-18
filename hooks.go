//go:build windows

package main

import (
	"runtime"
	"sync"
	"sync/atomic"
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

// consumed remembers what a key/button down started, so its up goes to the same trigger.
type consumed struct {
	trigger  string // "" = eaten by trigger capture
	suppress bool
}

var (
	pGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	pPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	pPeekMessageW       = user32.NewProc("PeekMessageW")
	hookTid             atomic.Uint32
)

// startHookThread runs the low-level hooks on their own thread with its own message
// loop. Windows silently removes a low-level hook whose thread does not answer within
// LowLevelHooksTimeout (about a second); on the UI thread that happened whenever the
// GUI (WebView2) kept it busy, after which captures and macros stopped working.
func startHookThread() {
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		var m msgT
		pPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0) // create the thread's message queue
		tid, _, _ := pGetCurrentThreadId.Call()
		hookTid.Store(uint32(tid))
		close(ready)
		updateHooks()
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			if m.Hwnd == 0 && m.Message == wmHookUpdate {
				updateHooks()
				continue
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	<-ready
}

func requestHookUpdate() {
	if tid := hookTid.Load(); tid != 0 {
		pPostThreadMessageW.Call(uintptr(tid), wmHookUpdate, 0, 0)
	}
}

// updateHooks installs/removes the low-level hooks as needed. Hook thread only.
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
	if gameBlocks(vk, physMods, down) {
		return true
	}
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
		// several normal keys held together (e.g. q+e): prefer that chord if something uses it
		if chord := chordTrigger(); chord != "" && engine.has(chord) {
			trig = chord
		}
		if capturer.offer(trig) {
			consumedKey[vk] = consumed{suppress: true}
			return true
		}
		if !engine.has(trig) {
			return false
		}
		v := vk
		suppress := engine.press(trig, func() { sendKeys([]uint16{v}) })
		consumedKey[vk] = consumed{trigger: trig, suppress: suppress}
		return suppress
	}
	delete(physDown, vk)
	c, ok := consumedKey[vk]
	if !ok {
		return false
	}
	delete(consumedKey, vk)
	if c.trigger == "" {
		return c.suppress
	}
	return engine.release(c.trigger)
}

// chordTrigger is the trigger for all non-modifier keys held right now, or ""
// when fewer than two are held.
func chordTrigger() string {
	var mains []uint16
	for v := range physDown {
		if modBit(v) == 0 {
			mains = append(mains, v)
		}
	}
	if len(mains) < 2 {
		return ""
	}
	return comboStringMulti(physMods, mains)
}

func msHookProc(code uintptr, wparam uintptr, lparam uintptr) uintptr {
	if int32(code) == 0 && wparam == 0x0200 { // WM_MOUSEMOVE: only the recorder cares
		if recMoves.Load() {
			m := *(**msLLHook)(unsafe.Pointer(&lparam))
			if m.ExtraInfo != injectMagic {
				recorder.move(int(m.Pt.X), int(m.Pt.Y))
			}
		}
	} else if int32(code) == 0 {
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

const (
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
)

// mouseTrigger: the held modifiers and keys plus the mouse part, e.g. "ctrl+q+mouse5".
func mouseTrigger(part string) string {
	var mains []uint16
	for v := range physDown {
		if modBit(v) == 0 {
			mains = append(mains, v)
		}
	}
	if physMods == 0 && len(mains) == 0 {
		return part
	}
	return comboStringMulti(physMods, mains) + "+" + part
}

func handleMouse(msg uint32, data uint32) bool {
	if msg == wmMouseWheel || msg == wmMouseHWheel {
		return handleWheel(msg, int16(data>>16))
	}
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
	part := map[string]string{"left": "mouseleft", "right": "mouseright"}[btn]
	if part == "" {
		part = btn
	}
	if down {
		trig := mouseTrigger(part)
		bare := trig == part
		if bare && !mouseAlone(part) { // plain left/right click: never a trigger, never captured
			return false
		}
		if capturer.offer(trig) {
			consumedBtn[btn] = consumed{suppress: true}
			return true
		}
		if !engine.has(trig) {
			if bare || !mouseAlone(part) || !engine.has(part) {
				return false
			}
			trig = part // e.g. shift held while using a "mouse4" macro
		}
		b := btn
		suppress := engine.press(trig, func() { mouseDown(b); mouseUp(b) })
		consumedBtn[btn] = consumed{trigger: trig, suppress: suppress}
		return suppress
	}
	c, ok := consumedBtn[btn]
	if !ok {
		return false
	}
	delete(consumedBtn, btn)
	if c.trigger == "" {
		return c.suppress
	}
	return engine.release(c.trigger)
}

// handleWheel: a wheel notch with a key held fires like a quick press and release.
func handleWheel(msg uint32, delta int16) bool {
	if delta == 0 || (physMods == 0 && len(physDown) == 0) {
		return false
	}
	part := "wheelup"
	switch {
	case msg == wmMouseWheel && delta < 0:
		part = "wheeldown"
	case msg == wmMouseHWheel && delta > 0:
		part = "wheelright"
	case msg == wmMouseHWheel:
		part = "wheelleft"
	}
	trig := mouseTrigger(part)
	if capturer.offer(trig) {
		return true
	}
	if !engine.has(trig) {
		return false
	}
	suppress := engine.press(trig, nil)
	engine.release(trig)
	return suppress
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
	move bool
	x, y int
}

type macroRecorder struct {
	mu        sync.Mutex
	on        bool
	withMouse bool
	events    []recEvent
	lastMove  time.Time
}

var (
	recorder = &macroRecorder{}
	recMoves atomic.Bool // fast check for the mouse hook
)

func (r *macroRecorder) active() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.on }

func (r *macroRecorder) start(withMouse, withMoves bool) {
	r.mu.Lock()
	r.on, r.withMouse, r.events, r.lastMove = true, withMouse, nil, time.Time{}
	r.mu.Unlock()
	recMoves.Store(withMoves)
	requestHookUpdate()
}

// move samples the cursor path at most every 16 ms.
func (r *macroRecorder) move(x, y int) {
	now := time.Now()
	r.mu.Lock()
	if r.on && now.Sub(r.lastMove) >= 16*time.Millisecond {
		r.lastMove = now
		r.events = append(r.events, recEvent{at: now, move: true, x: x, y: y})
	}
	r.mu.Unlock()
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
	recMoves.Store(false)
	requestHookUpdate()

	// trailing moves are the trip to the GUI's stop button
	for len(ev) > 0 && (ev[len(ev)-1].move || ev[len(ev)-1].btn == "left") {
		ev = ev[:len(ev)-1]
	}

	// drop the final left click (that was the "stop" button in the GUI)
	for len(ev) > 0 && ev[len(ev)-1].btn == "left" {
		ev = ev[:len(ev)-1]
	}
	// drop releases whose press happened before recording started
	downK := map[uint16]bool{}
	downB := map[string]bool{}
	var clean []recEvent
	for _, e := range ev {
		if e.move {
			clean = append(clean, e)
			continue
		}
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
		if e.move {
			out = append(out, Action{Type: "move", X: e.x, Y: e.y})
		} else if e.btn != "" {
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
