//go:build windows

package main

import (
	"math/rand/v2"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------- macro engine ----------

type macroEngine struct {
	mu        sync.Mutex
	byTrigger map[string][]Macro
	running   map[string]*runner // by macro ID
	needKB    bool
	needMouse bool
	stopCombo string
}

var engine = &macroEngine{byTrigger: map[string][]Macro{}, running: map[string]*runner{}}

func (e *macroEngine) rebuild() {
	c := getConfig()
	by := map[string][]Macro{}
	needKB, needMouse := false, false
	for _, m := range c.Macros {
		t := normalizeTrigger(m.Trigger)
		if !m.Enabled || t == "" || len(m.Actions) == 0 {
			continue
		}
		by[t] = append(by[t], m)
		switch {
		case strings.HasPrefix(t, "mx:"):
		case t == "mouse4" || t == "mouse5" || t == "middle":
			needMouse = true
		default:
			needKB = true
		}
	}
	e.mu.Lock()
	e.byTrigger = by
	e.needKB = needKB || len(by) > 0 || c.GameMode.configured() // keyboard hook also serves the stop-all combo and game mode
	e.needMouse = needMouse
	e.stopCombo = normalizeTrigger(c.StopCombo)
	e.mu.Unlock()
}

func (e *macroEngine) hookNeeds() (kb, mouse bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.needKB, e.needMouse
}

func (e *macroEngine) has(trigger string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.byTrigger[trigger]) > 0 || (trigger != "" && trigger == e.stopCombo)
}

// pick returns the macro for a trigger, preferring one bound to the foreground app.
func (e *macroEngine) pick(trigger string) (Macro, bool) {
	e.mu.Lock()
	list := e.byTrigger[trigger]
	e.mu.Unlock()
	if len(list) == 0 {
		return Macro{}, false
	}
	needFG := false
	for _, m := range list {
		if m.App != "" {
			needFG = true
		}
	}
	fg := ""
	if needFG {
		fg = foregroundProcessName()
	}
	for _, m := range list {
		if m.App != "" && strings.EqualFold(m.App, fg) {
			return m, true
		}
	}
	for _, m := range list {
		if m.App == "" {
			return m, true
		}
	}
	return Macro{}, false
}

// down handles a trigger press. Returns the macro, whether it was handled and whether to swallow the key.
func (e *macroEngine) down(trigger string) (Macro, bool, bool) {
	e.mu.Lock()
	stop := e.stopCombo
	e.mu.Unlock()
	if trigger == stop && stop != "" {
		e.stopAll()
		notify("MX Helper", "Tüm makrolar durduruldu.")
		return Macro{}, true, true
	}
	m, ok := e.pick(trigger)
	if !ok {
		return Macro{}, false, false
	}
	e.mu.Lock()
	r := e.running[m.ID]
	e.mu.Unlock()
	switch m.Mode {
	case "hold":
		if r == nil {
			e.start(m, true)
		}
	case "toggle":
		if r != nil {
			r.stop()
		} else {
			e.start(m, true)
		}
	default:
		if r == nil {
			e.start(m, false)
		}
	}
	return m, true, !m.Passthrough
}

func (e *macroEngine) up(m Macro) {
	if m.Mode != "hold" {
		return
	}
	e.mu.Lock()
	r := e.running[m.ID]
	e.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

func (e *macroEngine) start(m Macro, loop bool) {
	r := &runner{m: m, quit: make(chan struct{}), keys: map[uint16]bool{}, buttons: map[string]bool{}}
	e.mu.Lock()
	e.running[m.ID] = r
	e.mu.Unlock()
	go func() {
		r.run(loop)
		e.mu.Lock()
		if e.running[m.ID] == r {
			delete(e.running, m.ID)
		}
		e.mu.Unlock()
	}()
}

func (e *macroEngine) stopAll() {
	e.mu.Lock()
	rs := make([]*runner, 0, len(e.running))
	for _, r := range e.running {
		rs = append(rs, r)
	}
	e.mu.Unlock()
	for _, r := range rs {
		r.stop()
	}
}

func (e *macroEngine) runningIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for id := range e.running {
		out = append(out, id)
	}
	return out
}

// ---------- runner ----------

type runner struct {
	m       Macro
	quit    chan struct{}
	once    sync.Once
	keys    map[uint16]bool
	buttons map[string]bool
}

func (r *runner) stop() { r.once.Do(func() { close(r.quit) }) }

func (r *runner) stopped() bool {
	select {
	case <-r.quit:
		return true
	default:
		return false
	}
}

// sleep returns false if stopped during the wait.
func (r *runner) sleep(ms int) bool {
	if ms <= 0 {
		return !r.stopped()
	}
	t := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-r.quit:
		return false
	}
}

func (r *runner) run(loop bool) {
	defer r.releaseAll()
	iv := r.m.IntervalMs
	if iv < 1 {
		iv = 1
	}
	times := r.m.Repeat
	if times < 1 {
		times = 1
	}
	for i := 0; loop || i < times; i++ {
		for _, a := range r.m.Actions {
			if r.stopped() {
				return
			}
			r.do(a)
		}
		if loop || i < times-1 {
			if !r.sleep(iv) {
				return
			}
		}
	}
}

func (r *runner) releaseAll() {
	for vk := range r.keys {
		keyUp(vk)
	}
	for b := range r.buttons {
		mouseUp(b)
	}
}

func (r *runner) do(a Action) {
	switch strings.ToLower(a.Type) {
	case "keys":
		vks := parseCombo(a.Keys)
		if a.HoldMs > 0 {
			var in []input
			for _, v := range vks {
				in = append(in, keyIn(v, false))
				r.keys[v] = true
			}
			sendInputs(in)
			r.sleep(a.HoldMs)
			in = in[:0]
			for i := len(vks) - 1; i >= 0; i-- {
				in = append(in, keyIn(vks[i], true))
				delete(r.keys, vks[i])
			}
			sendInputs(in)
		} else {
			sendKeys(vks)
		}
	case "keydown":
		for _, v := range parseCombo(a.Keys) {
			keyDown(v)
			r.keys[v] = true
		}
	case "keyup":
		vks := parseCombo(a.Keys)
		for i := len(vks) - 1; i >= 0; i-- {
			keyUp(vks[i])
			delete(r.keys, vks[i])
		}
	case "click":
		b := normButton(a.Button)
		n := a.Count
		if n < 1 {
			n = 1
		}
		for i := 0; i < n && !r.stopped(); i++ {
			if i > 0 {
				r.sleep(30)
			}
			mouseDown(b)
			r.buttons[b] = true
			if a.HoldMs > 0 {
				r.sleep(a.HoldMs)
			}
			mouseUp(b)
			delete(r.buttons, b)
		}
	case "move":
		mouseMove(a.X, a.Y, a.Rel)
	case "scroll":
		mouseScroll(a.Amount, a.Horiz)
	case "mousedown":
		b := normButton(a.Button)
		mouseDown(b)
		r.buttons[b] = true
	case "mouseup":
		b := normButton(a.Button)
		mouseUp(b)
		delete(r.buttons, b)
	case "delay":
		r.sleep(delayMs(a))
	default:
		runSimpleAction(a)
	}
}

func runSimpleAction(a Action) {
	switch strings.ToLower(a.Type) {
	case "text":
		typeText(a.Text)
	case "keys":
		sendKeys(parseCombo(a.Keys))
	case "delay":
		time.Sleep(time.Duration(delayMs(a)) * time.Millisecond)
	case "move":
		mouseMove(a.X, a.Y, a.Rel)
	case "scroll":
		mouseScroll(a.Amount, a.Horiz)
	case "gamemode":
		switch strings.ToLower(a.Value) {
		case "on":
			setGameModeEnabled(true)
		case "off":
			setGameModeEnabled(false)
		default:
			setGameModeEnabled(!getConfig().GameMode.Enabled)
		}
	case "run":
		if err := shellExecute("open", a.Cmd, a.Args, 1); err != nil {
			logf("Çalıştırılamadı %q: %v", a.Cmd, err)
		}
	case "url":
		shellExecute("open", a.URL, "", 1)
	case "shell":
		cmd := exec.Command("cmd.exe", "/C", a.Cmd)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if err := cmd.Start(); err != nil {
			logf("Komut hatası: %v", err)
		}
	case "http":
		go func() {
			m := a.Method
			if m == "" {
				m = "GET"
			}
			req, err := http.NewRequest(strings.ToUpper(m), a.URL, strings.NewReader(a.Body))
			if err != nil {
				logf("HTTP hatası: %v", err)
				return
			}
			for k, v := range a.Headers {
				req.Header.Set(k, v)
			}
			cl := &http.Client{Timeout: 10 * time.Second}
			resp, err := cl.Do(req)
			if err != nil {
				logf("HTTP hatası: %v", err)
				notify("HTTP makrosu başarısız", err.Error())
				return
			}
			resp.Body.Close()
			logf("HTTP %s %s -> %d", m, a.URL, resp.StatusCode)
		}()
	case "backlight":
		switch strings.ToLower(a.Value) {
		case "up":
			mgr.stepBacklight(1)
		case "down":
			mgr.stepBacklight(-1)
		case "toggle":
			mgr.toggleBacklight()
		case "auto":
			mgr.setBacklightOverride("auto", 0)
		case "off":
			mgr.setBacklightOverride("off", 0)
		default:
			if n, err := strconv.Atoi(a.Value); err == nil {
				mgr.setBacklightOverride("manual", n)
			}
		}
	case "repair":
		go repairer.repair("makro")
	default:
		logf("Bilinmeyen eylem türü: %q", a.Type)
	}
}

// delayMs returns the wait for a delay action; ms_max > ms picks a random value in between.
func delayMs(a Action) int {
	if a.MsMax > a.Ms {
		return a.Ms + rand.IntN(a.MsMax-a.Ms+1)
	}
	return a.Ms
}

// testMacro runs a macro once (loop modes run for 2 seconds) after a short delay.
func testMacro(m Macro) {
	time.Sleep(2 * time.Second)
	r := &runner{m: m, quit: make(chan struct{}), keys: map[uint16]bool{}, buttons: map[string]bool{}}
	if m.Mode == "hold" || m.Mode == "toggle" {
		go func() { time.Sleep(2 * time.Second); r.stop() }()
		r.run(true)
	} else {
		r.run(false)
	}
}

// ---------- MX diverted keys ----------

type mxEv struct {
	cid  uint16
	down bool
}

var mxQueue = make(chan mxEv, 64)

func init() {
	go func() {
		for e := range mxQueue {
			if e.down {
				mxKeyDown(e.cid)
			} else {
				mxKeyUp(e.cid)
			}
		}
	}()
}

func queueMX(cid uint16, down bool) {
	select {
	case mxQueue <- mxEv{cid, down}:
	default:
	}
}

var (
	mxHeldMu sync.Mutex
	mxHeld   = map[uint16]Macro{}
)

func mxKeyDown(cid uint16) {
	t := normalizeTrigger("mx:0x" + strconv.FormatUint(uint64(cid), 16))
	if capturer.offer(t) {
		return
	}
	m, ok, _ := engine.down(t)
	if !ok {
		return
	}
	mxHeldMu.Lock()
	mxHeld[cid] = m
	mxHeldMu.Unlock()
}

func mxKeyUp(cid uint16) {
	mxHeldMu.Lock()
	m, ok := mxHeld[cid]
	delete(mxHeld, cid)
	mxHeldMu.Unlock()
	if ok {
		engine.up(m)
	}
}
