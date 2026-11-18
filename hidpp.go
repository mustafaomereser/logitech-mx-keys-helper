//go:build windows

package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// HID++ 2.0 over Bluetooth LE: report 0x11 (20 bytes), device index 0xFF.
const (
	reportLong = 0x11
	devIndexBT = 0xFF
	swID       = 0x0B

	featRoot        = 0x0000
	featFeatureSet  = 0x0001
	featDeviceName  = 0x0005
	featBattery     = 0x1000
	featUnifiedBatt = 0x1004
	featChangeHost  = 0x1814
	featBacklight2  = 0x1982
	featReprogV4    = 0x1B04
	featFnInversion = 0x40A3
)

var featureNames = map[uint16]string{
	0x0000: "Root", 0x0001: "FeatureSet", 0x0003: "DeviceFwVersion", 0x0005: "DeviceName",
	0x0007: "DeviceFriendlyName", 0x0020: "ConfigChange", 0x1000: "BatteryStatus",
	0x1004: "UnifiedBattery", 0x1814: "ChangeHost", 0x1815: "HostsInfo", 0x1982: "Backlight2",
	0x1B04: "ReprogControlsV4", 0x1BC0: "ReportHIDUsage", 0x1C00: "PersistentRemappableAction",
	0x40A3: "FnInversion(v3)", 0x4100: "Encryption", 0x4220: "LockKeyState", 0x4521: "DisableKeys",
	0x4522: "DisableKeysByUsage", 0x4531: "MultiPlatform", 0x4540: "KeyboardLayout", 0x00C2: "DFUControl",
	0x00D0: "DFU", 0x1802: "DeviceReset", 0x1803: "GPIOAccess", 0x1805: "OOBState", 0x1830: "PowerModes",
	0x1891: "BLEPro", 0x18A1: "LEDSW", 0x1DF3: "EQuad", 0x1E00: "EnableHidden", 0x1EB0: "TDE",
	0x0011: "PropertyAccess", 0x0021: "CryptoID", 0x0102: "ReprogControlsV4?",
}

type hidppMsg struct {
	feat byte
	fn   byte
	sw   byte
	p    []byte // 16 bytes of params
}

type hidppDevice struct {
	dev      *hidDevice
	mu       sync.Mutex // serializes requests
	pending  chan []byte
	pmu      sync.Mutex
	waitKey  [2]byte
	features map[uint16]byte
	fmu      sync.Mutex
	onEvent  func(hidppMsg)
	dead     chan struct{}
}

var errHIDPP = errors.New("hid++ error")

func newHIDPP(d *hidDevice, onEvent func(hidppMsg)) *hidppDevice {
	h := &hidppDevice{dev: d, features: map[uint16]byte{}, onEvent: onEvent, dead: make(chan struct{})}
	go h.readLoop()
	return h
}

func (h *hidppDevice) readLoop() {
	defer close(h.dead)
	buf := make([]byte, 64)
	for {
		n, err := h.dev.Read(buf)
		if err != nil {
			logf("HID++ okuma bitti: %v", err)
			return
		}
		if n < 7 || (buf[0] != 0x11 && buf[0] != 0x10) {
			continue
		}
		msg := append([]byte(nil), buf[:n]...)
		// error reply: [rid, dev, 0xFF, feat, fn|sw, code]
		h.pmu.Lock()
		ch, key := h.pending, h.waitKey
		h.pmu.Unlock()
		if ch != nil {
			if msg[2] == 0xFF && msg[3] == key[0] && msg[4] == key[1] {
				select {
				case ch <- msg:
				default:
				}
				continue
			}
			if msg[2] == key[0] && msg[3] == key[1] {
				select {
				case ch <- msg:
				default:
				}
				continue
			}
		}
		if msg[3]&0x0F == 0 && h.onEvent != nil { // swid 0 => notification
			p := make([]byte, 16)
			copy(p, msg[4:])
			h.onEvent(hidppMsg{feat: msg[2], fn: msg[3] >> 4, p: p})
		}
	}
}

// request sends feature function and waits for a reply.
func (h *hidppDevice) request(featIdx byte, fn byte, params ...byte) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]byte, 20)
	out[0], out[1], out[2], out[3] = reportLong, devIndexBT, featIdx, (fn<<4)|swID
	copy(out[4:], params)
	ch := make(chan []byte, 1)
	h.pmu.Lock()
	h.pending, h.waitKey = ch, [2]byte{featIdx, out[3]}
	h.pmu.Unlock()
	defer func() { h.pmu.Lock(); h.pending = nil; h.pmu.Unlock() }()
	if err := h.dev.Write(out, 1500); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r[2] == 0xFF {
			return nil, fmt.Errorf("%w: feat %d fn %d kod 0x%02X", errHIDPP, featIdx, fn, r[5])
		}
		p := make([]byte, 16)
		copy(p, r[4:])
		return p, nil
	case <-time.After(2 * time.Second):
		return nil, errors.New("yanıt zaman aşımı")
	case <-h.dead:
		return nil, errors.New("cihaz kapandı")
	}
}

func (h *hidppDevice) cached(id uint16) (byte, bool) {
	h.fmu.Lock()
	defer h.fmu.Unlock()
	v, ok := h.features[id]
	return v, ok
}

func (h *hidppDevice) resetFeatures() {
	h.fmu.Lock()
	h.features = map[uint16]byte{}
	h.fmu.Unlock()
}

func (h *hidppDevice) featureIndex(id uint16) (byte, error) {
	if idx, ok := h.cached(id); ok {
		if idx == 0 && id != 0 {
			return 0, errors.New("özellik yok")
		}
		return idx, nil
	}
	p, err := h.request(0, 0, byte(id>>8), byte(id))
	if err != nil {
		return 0, err
	}
	h.fmu.Lock()
	h.features[id] = p[0]
	h.fmu.Unlock()
	if p[0] == 0 && id != 0 {
		return 0, errors.New("özellik yok")
	}
	return p[0], nil
}

func (h *hidppDevice) has(id uint16) bool {
	_, err := h.featureIndex(id)
	return err == nil
}

func (h *hidppDevice) call(id uint16, fn byte, params ...byte) ([]byte, error) {
	idx, err := h.featureIndex(id)
	if err != nil {
		return nil, fmt.Errorf("0x%04X: %w", id, err)
	}
	return h.request(idx, fn, params...)
}

func (h *hidppDevice) ping() error {
	p, err := h.request(0, 1, 0, 0, 0x5A)
	if err != nil {
		return err
	}
	if p[2] != 0x5A {
		return errors.New("ping eşleşmedi")
	}
	return nil
}

func (h *hidppDevice) deviceName() string {
	p, err := h.call(featDeviceName, 0)
	if err != nil {
		return ""
	}
	n := int(p[0])
	var sb strings.Builder
	for off := 0; off < n && off < 64; off += 16 {
		q, err := h.call(featDeviceName, 1, byte(off))
		if err != nil {
			break
		}
		for _, c := range q {
			if c == 0 || sb.Len() >= n {
				break
			}
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

type featureEntry struct {
	idx  byte
	id   uint16
	typ  byte
	name string
}

func (h *hidppDevice) listFeatures() []featureEntry {
	p, err := h.call(featFeatureSet, 0)
	if err != nil {
		return nil
	}
	cnt := int(p[0])
	out := []featureEntry{{0, 0, 0, "Root"}}
	for i := 1; i <= cnt; i++ {
		q, err := h.call(featFeatureSet, 1, byte(i))
		if err != nil {
			continue
		}
		id := uint16(q[0])<<8 | uint16(q[1])
		name := featureNames[id]
		if name == "" {
			name = "?"
		}
		out = append(out, featureEntry{byte(i), id, q[2], name})
	}
	return out
}

// ---------- battery ----------

type batteryState struct {
	Percent  int  `json:"percent"`
	Charging bool `json:"charging"`
	Full     bool `json:"full"`
	Known    bool `json:"known"`
}

func parseUnifiedBattery(p []byte) batteryState {
	return batteryState{Percent: int(p[0]), Charging: p[2] == 1 || p[2] == 2, Full: p[2] == 3, Known: true}
}

func (h *hidppDevice) battery() (batteryState, error) {
	if h.has(featUnifiedBatt) {
		p, err := h.call(featUnifiedBatt, 1)
		if err != nil {
			return batteryState{}, err
		}
		return parseUnifiedBattery(p), nil
	}
	p, err := h.call(featBattery, 0)
	if err != nil {
		return batteryState{}, err
	}
	return batteryState{Percent: int(p[0]), Charging: p[2] >= 1 && p[2] <= 3, Full: p[2] == 3, Known: true}, nil
}

// ---------- backlight (0x1982 Backlight2) ----------
// Layout as used by Solaar: enabled, options, supported, effects(2), level, dho(2), dhi(2), dpow(2) (little-endian)

type backlight struct {
	Enabled   byte
	Options   byte
	Supported byte
	Effects   uint16
	Level     byte
	DHO       uint16 // duration hands out (x5 sn)
	DHI       uint16 // duration hands in
	DPow      uint16 // duration powered
	Levels    int
}

func (b backlight) Mode() int { return int(b.Options>>3) & 3 }

func (h *hidppDevice) getBacklight() (backlight, error) {
	p, err := h.call(featBacklight2, 0)
	if err != nil {
		return backlight{}, err
	}
	b := backlight{
		Enabled: p[0], Options: p[1], Supported: p[2],
		Effects: uint16(p[3]) | uint16(p[4])<<8, Level: p[5],
		DHO: uint16(p[6]) | uint16(p[7])<<8, DHI: uint16(p[8]) | uint16(p[9])<<8, DPow: uint16(p[10]) | uint16(p[11])<<8,
	}
	if q, err := h.call(featBacklight2, 2); err == nil {
		b.Levels = int(q[0])
	}
	return b, nil
}

func (h *hidppDevice) setBacklight(b backlight) error {
	b.Options = (b.Options & 0x07) | byte(b.Mode()<<3)
	_, err := h.call(featBacklight2, 1, b.Enabled, b.Options, 0xFF, b.Level,
		byte(b.DHO), byte(b.DHO>>8), byte(b.DHI), byte(b.DHI>>8), byte(b.DPow), byte(b.DPow>>8))
	return err
}

// mode: "off", "auto", "manual"
func (h *hidppDevice) applyBacklight(mode string, level int, handsOutSec, handsInSec, poweredSec int) error {
	b, err := h.getBacklight()
	if err != nil {
		return err
	}
	switch mode {
	case "off":
		b.Enabled = 0
	case "auto":
		b.Enabled = 1
		b.Options = (b.Options & 0x07) | (1 << 3)
	case "manual":
		b.Enabled = 1
		b.Options = (b.Options & 0x07) | (3 << 3)
		if level >= 0 {
			if b.Levels > 0 && level > b.Levels-1 {
				level = b.Levels - 1
			}
			b.Level = byte(level)
		}
	}
	if handsOutSec > 0 {
		b.DHO = uint16(handsOutSec / 5)
	}
	if handsInSec > 0 {
		b.DHI = uint16(handsInSec / 5)
	}
	if poweredSec > 0 {
		b.DPow = uint16(poweredSec / 5)
	}
	return h.setBacklight(b)
}

// ---------- reprogrammable controls (0x1B04) ----------

type controlInfo struct {
	CID, TID uint16
	Flags    byte
	Pos      byte
	Group    byte
}

func (c controlInfo) Divertable() bool { return c.Flags&0x20 != 0 }

var cidNames = map[uint16]string{
	0x00C3: "Gesture", 0x00C7: "Brightness Down", 0x00C8: "Brightness Up", 0x00D4: "Show Desktop",
	0x00D7: "Switch App", 0x00D1: "Easy-Switch 1", 0x00D2: "Easy-Switch 2", 0x00D3: "Easy-Switch 3",
	0x00E0: "Mission Control/Task View", 0x00E3: "Show Desktop", 0x00E4: "Lock", 0x00E6: "Calculator",
	0x0103: "Dictation?", 0x0108: "Emoji?", 0x010A: "Screen Capture?", 0x011C: "Mic Mute?",
	0x0110: "Backlight Up?", 0x0111: "Backlight Down?", 0x00E7: "Screen Lock", 0x011E: "Do Not Disturb?",
}

func cidName(c uint16) string {
	if n, ok := cidNames[c]; ok {
		return n
	}
	return "?"
}

func (h *hidppDevice) listControls() ([]controlInfo, error) {
	p, err := h.call(featReprogV4, 0)
	if err != nil {
		return nil, err
	}
	var out []controlInfo
	for i := 0; i < int(p[0]); i++ {
		q, err := h.call(featReprogV4, 1, byte(i))
		if err != nil {
			continue
		}
		out = append(out, controlInfo{
			CID: uint16(q[0])<<8 | uint16(q[1]), TID: uint16(q[2])<<8 | uint16(q[3]),
			Flags: q[4], Pos: q[5], Group: q[6],
		})
	}
	return out, nil
}

func (h *hidppDevice) setDivert(cid uint16, on bool) error {
	flags := byte(0x02) // dvalid
	if on {
		flags |= 0x01
	}
	_, err := h.call(featReprogV4, 3, byte(cid>>8), byte(cid), flags, 0, 0)
	return err
}

var pidRe = regexp.MustCompile(`pid&([0-9a-f]{4})`)

func pidFromPath(path string) string {
	m := pidRe.FindStringSubmatch(strings.ToLower(path))
	if m == nil {
		return ""
	}
	return m[1]
}
