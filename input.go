//go:build windows

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unsafe"
)

// injectMagic marks events we inject so our own hooks ignore them.
const injectMagic = 0x4D584831 // "MXH1"

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type mouseInput struct {
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type input struct {
	Type uint32
	_    uint32
	U    [32]byte
}

const (
	inputMouse        = 0
	inputKeyboard     = 1
	keyeventfExtended = 0x0001
	keyeventfKeyUp    = 0x0002
	keyeventfUnicode  = 0x0004
)

func kbInput(k keybdInput) input {
	k.ExtraInfo = injectMagic
	in := input{Type: inputKeyboard}
	*(*keybdInput)(unsafe.Pointer(&in.U[0])) = k
	return in
}

func msInput(m mouseInput) input {
	m.ExtraInfo = injectMagic
	in := input{Type: inputMouse}
	*(*mouseInput)(unsafe.Pointer(&in.U[0])) = m
	return in
}

func sendInputs(in []input) {
	if len(in) == 0 {
		return
	}
	pSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

var extendedVKs = map[uint16]bool{
	0x21: true, 0x22: true, 0x23: true, 0x24: true, 0x25: true, 0x26: true, 0x27: true, 0x28: true,
	0x2D: true, 0x2E: true, 0x5B: true, 0x5C: true, 0x5D: true, 0xA3: true, 0xA5: true, 0x6F: true, 0x2C: true,
	0x90: true,
}

func keyIn(vk uint16, up bool) input {
	var fl uint32
	if extendedVKs[vk] {
		fl |= keyeventfExtended
	}
	if up {
		fl |= keyeventfKeyUp
	}
	return kbInput(keybdInput{Vk: vk, Flags: fl})
}

func keyDown(vk uint16) { sendInputs([]input{keyIn(vk, false)}) }
func keyUp(vk uint16)   { sendInputs([]input{keyIn(vk, true)}) }

// sendKeys presses vks in order then releases in reverse (a chord).
func sendKeys(vks []uint16) {
	var in []input
	for _, v := range vks {
		in = append(in, keyIn(v, false))
	}
	for i := len(vks) - 1; i >= 0; i-- {
		in = append(in, keyIn(vks[i], true))
	}
	sendInputs(in)
}

func typeText(s string) {
	var in []input
	for _, c := range utf16.Encode([]rune(s)) {
		if c == '\n' {
			in = append(in, keyIn(0x0D, false), keyIn(0x0D, true))
			continue
		}
		in = append(in,
			kbInput(keybdInput{Scan: c, Flags: keyeventfUnicode}),
			kbInput(keybdInput{Scan: c, Flags: keyeventfUnicode | keyeventfKeyUp}))
	}
	sendInputs(in)
}

// ---------- mouse ----------

type mouseButton struct {
	down, up uint32
	data     uint32
}

var mouseButtons = map[string]mouseButton{
	"left":   {0x0002, 0x0004, 0},
	"right":  {0x0008, 0x0010, 0},
	"middle": {0x0020, 0x0040, 0},
	"mouse4": {0x0080, 0x0100, 1},
	"mouse5": {0x0080, 0x0100, 2},
}

func normButton(b string) string {
	switch strings.ToLower(b) {
	case "", "left", "sol", "lmb":
		return "left"
	case "right", "sag", "sağ", "rmb":
		return "right"
	case "middle", "orta", "mbutton", "mouse3":
		return "middle"
	case "mouse4", "x1", "xbutton1":
		return "mouse4"
	case "mouse5", "x2", "xbutton2":
		return "mouse5"
	}
	return "left"
}

func mouseDown(b string) {
	mb := mouseButtons[normButton(b)]
	sendInputs([]input{msInput(mouseInput{Flags: mb.down, MouseData: mb.data})})
}

func mouseUp(b string) {
	mb := mouseButtons[normButton(b)]
	sendInputs([]input{msInput(mouseInput{Flags: mb.up, MouseData: mb.data})})
}

const (
	mouseeventfMove        = 0x0001
	mouseeventfWheel       = 0x0800
	mouseeventfHWheel      = 0x1000
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000
)

func sysMetric(i int) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int32(r)
}

// mouseMove moves the cursor to screen pixel (x, y), or by (x, y) when rel is set.
func mouseMove(x, y int, rel bool) {
	if rel {
		sendInputs([]input{msInput(mouseInput{Dx: int32(x), Dy: int32(y), Flags: mouseeventfMove})})
		return
	}
	// absolute coordinates are normalized to 0..65535 across the virtual desktop
	vx, vy := sysMetric(76), sysMetric(77) // SM_XVIRTUALSCREEN, SM_YVIRTUALSCREEN
	vw, vh := sysMetric(78), sysMetric(79) // SM_CXVIRTUALSCREEN, SM_CYVIRTUALSCREEN
	if vw <= 1 || vh <= 1 {
		return
	}
	nx := (int64(int32(x)-vx)*65535 + int64(vw-1)/2) / int64(vw-1)
	ny := (int64(int32(y)-vy)*65535 + int64(vh-1)/2) / int64(vh-1)
	sendInputs([]input{msInput(mouseInput{Dx: int32(nx), Dy: int32(ny), Flags: mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk})})
}

// mouseScroll turns the wheel by n notches (positive = up / right).
func mouseScroll(n int, horiz bool) {
	fl := uint32(mouseeventfWheel)
	if horiz {
		fl = mouseeventfHWheel
	}
	sendInputs([]input{msInput(mouseInput{MouseData: uint32(int32(n * 120)), Flags: fl})})
}

func cursorPos() (int, int) {
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return int(pt.X), int(pt.Y)
}

// ---------- key names ----------

var keyNames = map[string]uint16{
	"ctrl": 0x11, "control": 0x11, "shift": 0x10, "alt": 0x12, "win": 0x5B, "lwin": 0x5B, "rwin": 0x5C,
	"lctrl": 0xA2, "rctrl": 0xA3, "lshift": 0xA0, "rshift": 0xA1, "lalt": 0xA4, "ralt": 0xA5, "altgr": 0xA5,
	"enter": 0x0D, "return": 0x0D, "tab": 0x09, "esc": 0x1B, "escape": 0x1B, "space": 0x20, "backspace": 0x08,
	"delete": 0x2E, "del": 0x2E, "insert": 0x2D, "ins": 0x2D, "home": 0x24, "end": 0x23, "pgup": 0x21, "pageup": 0x21,
	"pgdn": 0x22, "pagedown": 0x22, "up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"printscreen": 0x2C, "prtsc": 0x2C, "capslock": 0x14, "numlock": 0x90, "scrolllock": 0x91, "pause": 0x13,
	"menu": 0x5D, "apps": 0x5D,
	"mute": 0xAD, "voldown": 0xAE, "volup": 0xAF, "next": 0xB0, "prev": 0xB1, "stop": 0xB2, "playpause": 0xB3,
	"plus": 0xBB, "minus": 0xBD, "comma": 0xBC, "period": 0xBE,
	"browserback": 0xA6, "browserfwd": 0xA7, "browserrefresh": 0xA8, "browserhome": 0xAC,
	"mail": 0xB4, "calc": 0xB7, "mycomputer": 0xB6,
	"num0": 0x60, "num1": 0x61, "num2": 0x62, "num3": 0x63, "num4": 0x64, "num5": 0x65, "num6": 0x66,
	"num7": 0x67, "num8": 0x68, "num9": 0x69, "nummul": 0x6A, "numadd": 0x6B, "numsub": 0x6D, "numdec": 0x6E, "numdiv": 0x6F,
}

var vkNames = func() map[uint16]string {
	pref := []string{"ctrl", "shift", "alt", "win", "rwin", "lctrl", "rctrl", "lshift", "rshift", "lalt", "ralt", "enter", "tab", "esc",
		"space", "backspace", "delete", "insert", "home", "end", "pgup", "pgdn", "up", "down", "left", "right", "printscreen",
		"capslock", "numlock", "scrolllock", "pause", "menu", "mute", "voldown", "volup", "next", "prev", "stop", "playpause",
		"plus", "minus", "comma", "period", "num0", "num1", "num2", "num3", "num4", "num5", "num6", "num7", "num8", "num9",
		"nummul", "numadd", "numsub", "numdec", "numdiv", "browserback", "browserfwd", "browserrefresh", "browserhome",
		"mail", "calc", "mycomputer"}
	m := map[uint16]string{}
	for _, n := range pref {
		if _, ok := m[keyNames[n]]; !ok {
			m[keyNames[n]] = n
		}
	}
	return m
}()

func vkName(vk uint16) string {
	if n, ok := vkNames[vk]; ok {
		return n
	}
	switch {
	case vk >= 'A' && vk <= 'Z':
		return string(rune(vk - 'A' + 'a'))
	case vk >= '0' && vk <= '9':
		return string(rune(vk))
	case vk >= 0x70 && vk <= 0x87:
		return fmt.Sprintf("f%d", vk-0x70+1)
	}
	return fmt.Sprintf("vk%02x", vk)
}

func parseKey(part string) (uint16, bool) {
	part = strings.ToLower(strings.TrimSpace(part))
	if part == "" {
		return 0, false
	}
	if v, ok := keyNames[part]; ok {
		return v, true
	}
	if len(part) == 1 {
		c := part[0]
		if c >= 'a' && c <= 'z' {
			return uint16(c - 'a' + 'A'), true
		}
		if c >= '0' && c <= '9' {
			return uint16(c), true
		}
	}
	if part[0] == 'f' {
		if n, err := strconv.Atoi(part[1:]); err == nil && n >= 1 && n <= 24 {
			return uint16(0x70 + n - 1), true
		}
	}
	if strings.HasPrefix(part, "vk") {
		if n, err := strconv.ParseUint(strings.TrimPrefix(part, "vk"), 16, 16); err == nil {
			return uint16(n), true
		}
	}
	return 0, false
}

func parseCombo(s string) []uint16 {
	var out []uint16
	for _, part := range strings.Split(strings.ReplaceAll(s, " ", ""), "+") {
		if part == "" {
			continue
		}
		if v, ok := parseKey(part); ok {
			out = append(out, v)
		} else {
			logf("Bilinmeyen tuş adı: %q", part)
		}
	}
	return out
}

// modifier bits
const (
	modCtrl = 1 << iota
	modAlt
	modShift
	modWin
)

func modBit(vk uint16) int {
	switch vk {
	case 0x11, 0xA2, 0xA3:
		return modCtrl
	case 0x12, 0xA4, 0xA5:
		return modAlt
	case 0x10, 0xA0, 0xA1:
		return modShift
	case 0x5B, 0x5C:
		return modWin
	}
	return 0
}

// modNames lists the held modifiers in canonical order: ctrl, alt, shift, win.
func modNames(mods int) []string {
	var parts []string
	if mods&modCtrl != 0 {
		parts = append(parts, "ctrl")
	}
	if mods&modAlt != 0 {
		parts = append(parts, "alt")
	}
	if mods&modShift != 0 {
		parts = append(parts, "shift")
	}
	if mods&modWin != 0 {
		parts = append(parts, "win")
	}
	return parts
}

func comboString(mods int, vk uint16) string {
	return strings.Join(append(modNames(mods), vkName(vk)), "+")
}

// comboStringMulti is comboString for several non-modifier keys (a chord such
// as "ctrl+a+s"); the keys are sorted by name so the order of pressing does not matter.
func comboStringMulti(mods int, vks []uint16) string {
	names := make([]string, 0, len(vks))
	seen := map[string]bool{}
	for _, v := range vks {
		if n := vkName(v); !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(append(modNames(mods), names...), "+")
}

// normalizeTrigger returns the canonical form of a trigger string:
//
//	"mx:0x0108", "mouse4", "mouse5", "middle", or "ctrl+alt+k"
func normalizeTrigger(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return ""
	}
	if strings.HasPrefix(t, "mx:") {
		v := strings.TrimPrefix(strings.TrimPrefix(t, "mx:"), "0x")
		n, err := strconv.ParseUint(v, 16, 16)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("mx:0x%04x", n)
	}
	mods := 0
	var mains []uint16
	mouse := ""
	for _, part := range strings.Split(strings.ReplaceAll(t, " ", ""), "+") {
		if mb := mouseTrigName(part); mb != "" {
			if mouse != "" && mouse != mb {
				return "" // one mouse button per trigger
			}
			mouse = mb
			continue
		}
		vk, ok := parseKey(part)
		if !ok {
			return ""
		}
		if b := modBit(vk); b != 0 {
			mods |= b
		} else {
			mains = append(mains, vk)
		}
	}
	if mouse != "" {
		// left/right click and the wheel only together with a key, so the mouse keeps working
		if mods == 0 && len(mains) == 0 && !mouseAlone(mouse) {
			return ""
		}
		if mods == 0 && len(mains) == 0 {
			return mouse
		}
		return comboStringMulti(mods, mains) + "+" + mouse
	}
	switch len(mains) {
	case 0:
		return ""
	case 1:
		return comboString(mods, mains[0])
	}
	return comboStringMulti(mods, mains)
}

// Mouse parts of a trigger. They always come last: "ctrl+mouseleft", "q+mouse5".
var mouseAliases = map[string]string{
	"mouseleft": "mouseleft", "lbutton": "mouseleft", "mouse1": "mouseleft",
	"mouseright": "mouseright", "rbutton": "mouseright", "mouse2": "mouseright",
	"middle": "middle", "mouse3": "middle", "mbutton": "middle",
	"mouse4": "mouse4", "x1": "mouse4", "xbutton1": "mouse4",
	"mouse5": "mouse5", "x2": "mouse5", "xbutton2": "mouse5",
	"wheelup": "wheelup", "wheeldown": "wheeldown", "wheelleft": "wheelleft", "wheelright": "wheelright",
}

func mouseTrigName(part string) string { return mouseAliases[part] }

func mouseAlone(m string) bool { return m == "middle" || m == "mouse4" || m == "mouse5" }

// triggerMouse returns the mouse part of a normalized trigger, or "".
func triggerMouse(t string) string {
	if i := strings.LastIndexByte(t, '+'); i >= 0 {
		t = t[i+1:]
	}
	return mouseAliases[t]
}
