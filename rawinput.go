//go:build windows

package main

import (
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type rawInputDevice struct {
	UsagePage uint16
	Usage     uint16
	Flags     uint32
	Target    uintptr
}

type rawInputHeader struct {
	Type   uint32
	Size   uint32
	Device uintptr
	WParam uintptr
}

type rawKeyboard struct {
	MakeCode  uint16
	Flags     uint16
	Reserved  uint16
	VKey      uint16
	Message   uint32
	ExtraInfo uint32
}

const (
	ridInput        = 0x10000003
	ridiDeviceName  = 0x20000007
	rimTypeKeyboard = 1
	rimTypeHID      = 2
	ridevInputSink  = 0x00000100
	ridevDevNotify  = 0x00002000
	riKeyBreak      = 0x01
)

func registerRawInput(hwnd uintptr) error {
	devs := []rawInputDevice{
		{UsagePage: 0x01, Usage: 0x06, Flags: ridevInputSink | ridevDevNotify, Target: hwnd}, // keyboard
		{UsagePage: 0x0C, Usage: 0x01, Flags: ridevInputSink, Target: hwnd},                  // consumer control
	}
	r, _, e := pRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&devs[0])), uintptr(len(devs)), unsafe.Sizeof(devs[0]))
	if r == 0 {
		return e
	}
	return nil
}

var (
	devNameMu    sync.Mutex
	devNameCache = map[uintptr]string{}
)

func rawDeviceName(h uintptr) string {
	devNameMu.Lock()
	if n, ok := devNameCache[h]; ok {
		devNameMu.Unlock()
		return n
	}
	devNameMu.Unlock()
	var size uint32
	pGetRawInputDeviceInfoW.Call(h, ridiDeviceName, 0, uintptr(unsafe.Pointer(&size)))
	name := ""
	if size > 0 && size < 4096 {
		buf := make([]uint16, size+1)
		r, _, _ := pGetRawInputDeviceInfoW.Call(h, ridiDeviceName, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		if int32(r) > 0 {
			name = strings.ToLower(syscall.UTF16ToString(buf))
		}
	}
	devNameMu.Lock()
	devNameCache[h] = name
	devNameMu.Unlock()
	return name
}

func forgetRawDevice(h uintptr) {
	devNameMu.Lock()
	delete(devNameCache, h)
	devNameMu.Unlock()
}

var (
	seenMu sync.Mutex
	seenMX = map[string]bool{}
)

func rawSeenDevices() []string {
	seenMu.Lock()
	defer seenMu.Unlock()
	var out []string
	for k := range seenMX {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isMXDevice(name string) bool {
	if name == "" {
		return false
	}
	pid := mgr.currentPID()
	ok := strings.Contains(name, "vid&02046d_pid&"+pid) || strings.Contains(name, "vid_046d&pid_"+pid)
	if ok {
		seenMu.Lock()
		seenMX[name] = true
		seenMu.Unlock()
	}
	return ok
}

func isMediaVK(vk uint16) bool { return vk >= 0xAD && vk <= 0xB7 }

const vkVolumeMute = 0xAD

func handleRawInput(lparam uintptr) {
	var size uint32
	hdrSize := unsafe.Sizeof(rawInputHeader{})
	pGetRawInputData.Call(lparam, ridInput, 0, uintptr(unsafe.Pointer(&size)), hdrSize)
	if size == 0 || size > 4096 {
		return
	}
	buf := make([]byte, size)
	r, _, _ := pGetRawInputData.Call(lparam, ridInput, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), hdrSize)
	if int32(r) <= 0 {
		return
	}
	hdr := (*rawInputHeader)(unsafe.Pointer(&buf[0]))
	if hdr.Device == 0 { // injected (SendInput) – ignore
		return
	}
	if !isMXDevice(rawDeviceName(hdr.Device)) {
		return
	}
	body := buf[hdrSize:]
	switch hdr.Type {
	case rimTypeKeyboard:
		if len(body) < int(unsafe.Sizeof(rawKeyboard{})) {
			return
		}
		kb := (*rawKeyboard)(unsafe.Pointer(&body[0]))
		if kb.Flags&riKeyBreak != 0 || kb.VKey == 0 || kb.VKey == 0xFF {
			return
		}
		if isMediaVK(kb.VKey) {
			detector.onMediaKey(kb.VKey == vkVolumeMute)
		} else {
			detector.onNormalKey()
		}
	case rimTypeHID:
		if len(body) < 8 {
			return
		}
		sizeHid := *(*uint32)(unsafe.Pointer(&body[0]))
		count := *(*uint32)(unsafe.Pointer(&body[4]))
		data := body[8:]
		for i := uint32(0); i < count; i++ {
			off := int(i * sizeHid)
			if off+int(sizeHid) > len(data) || sizeHid < 2 {
				break
			}
			rep := data[off : off+int(sizeHid)]
			pressed, mute := false, false
			for j := 1; j+1 < len(rep); j += 2 {
				u := uint16(rep[j]) | uint16(rep[j+1])<<8
				if u != 0 {
					pressed = true
					if u == 0xE2 {
						mute = true
					}
				}
			}
			if pressed {
				detector.onMediaKey(mute)
			}
		}
	}
}

// ---------- stuck-keyboard detector ----------
//
// Failure signature: after the keyboard wakes/reconnects, only media (consumer) keys
// arrive from it while normal keys never do.

type stuckDetector struct {
	mu            sync.Mutex
	sessionStart  time.Time
	lastActivity  time.Time
	normalSince   int
	mediaSince    int
	lastMedia     time.Time
	muteTimes     []time.Time
	lastPrompt    time.Time
	repairPending bool
}

var detector = &stuckDetector{}

func (d *stuckDetector) newSessionLocked(now time.Time) {
	d.sessionStart = now
	d.normalSince = 0
	d.mediaSince = 0
}

func (d *stuckDetector) touchLocked(now time.Time) {
	if d.lastActivity.IsZero() || now.Sub(d.lastActivity) > 20*time.Second {
		d.newSessionLocked(now)
	}
	d.lastActivity = now
}

func (d *stuckDetector) onReconnect() {
	d.mu.Lock()
	d.newSessionLocked(time.Now())
	d.mu.Unlock()
}

func (d *stuckDetector) onDisconnect() {}

func (d *stuckDetector) onNormalKey() {
	now := time.Now()
	d.mu.Lock()
	d.touchLocked(now)
	d.normalSince++
	pending := d.repairPending
	d.repairPending = false
	d.mu.Unlock()
	if pending {
		repairer.markSuccess()
	}
}

func (d *stuckDetector) onMediaKey(isMute bool) {
	now := time.Now()
	d.mu.Lock()
	if now.Sub(d.lastMedia) < 80*time.Millisecond { // same press seen via two paths
		d.mu.Unlock()
		return
	}
	d.lastMedia = now
	d.touchLocked(now)
	d.mediaSince++

	// gesture: Mute x3 within 2.5 s => repair right away
	if isMute {
		d.muteTimes = append(d.muteTimes, now)
		var keep []time.Time
		for _, t := range d.muteTimes {
			if now.Sub(t) < 2500*time.Millisecond {
				keep = append(keep, t)
			}
		}
		d.muteTimes = keep
		if len(keep) >= 3 {
			d.muteTimes = nil
			d.repairPending = true
			d.mu.Unlock()
			sendKeys([]uint16{vkVolumeMute}) // undo the odd toggle
			go repairer.repair("Mute x3")
			return
		}
	}

	c := getConfig()
	suspicious := d.normalSince == 0 && d.mediaSince >= c.Repair.MediaPressesToSuspect && now.Sub(d.sessionStart) < 10*time.Minute
	canPrompt := now.Sub(d.lastPrompt) > 90*time.Second
	mode := c.Repair.Auto
	if suspicious && canPrompt && mode != "off" {
		d.lastPrompt = now
		d.repairPending = true
		d.mu.Unlock()
		if mode == "auto" {
			go repairer.repair("otomatik algılama")
		} else {
			notifyAction("MX Keys Mini takılmış olabilir",
				"Sadece ses tuşları geliyor. Onarmak için bu bildirime tıkla ya da Mute tuşuna 3 kez bas.",
				func() { go repairer.repair("bildirim") })
		}
		return
	}
	d.mu.Unlock()
}
