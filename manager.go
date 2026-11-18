//go:build windows

package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type manager struct {
	mu          sync.Mutex
	hp          *hidppDevice
	info        *hidInfo
	name        string
	pid         string
	connected   bool
	batt        batteryState
	bl          backlight
	blKnown     bool
	controls    []controlInfo
	diverted    map[uint16]bool
	pressed     map[uint16]bool
	learnMode   bool
	capturing   bool
	blOverride  *blTarget // set from tray; cleared when the active rule changes
	lastRuleKey string
	lastApplied string
	lowBattOff  bool
	wake        chan struct{}
}

type blTarget struct {
	Mode  string
	Level int
}

var mgr = &manager{diverted: map[uint16]bool{}, pressed: map[uint16]bool{}, wake: make(chan struct{}, 1)}

func (m *manager) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *manager) snapshot() (connected bool, name string, b batteryState, bl backlight, blKnown bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected, m.name, m.batt, m.bl, m.blKnown
}

func (m *manager) currentPID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pid != "" {
		return m.pid
	}
	c := getConfig()
	if c.DevicePID != "" {
		return strings.ToLower(c.DevicePID)
	}
	return "b369"
}

func (m *manager) run() {
	failures := 0
	lastBatt := time.Time{}
	for {
		c := getConfig()
		if m.hp == nil {
			if !m.open() {
				m.sleep(8 * time.Second)
				continue
			}
			failures = 0
		}
		select {
		case <-m.hp.dead:
			logf("Cihaz kapandı, yeniden aranıyor")
			m.closeDev()
			continue
		default:
		}
		err := m.hp.ping()
		if err != nil {
			failures++
			if m.isConnected() && failures >= 2 {
				m.setConnected(false)
				logf("Bağlantı koptu (%v)", err)
				detector.onDisconnect()
				trayRefresh()
			}
			if failures >= 20 { // handle went stale for a long time: reopen
				m.closeDev()
				failures = 0
			}
			m.sleep(4 * time.Second)
			continue
		}
		failures = 0
		if !m.isConnected() {
			m.setConnected(true)
			logf("Bağlandı: %s", m.name)
			m.onConnect()
			detector.onReconnect()
			trayRefresh()
			lastBatt = time.Now()
		}
		if time.Since(lastBatt) > 5*time.Minute {
			m.readBattery()
			lastBatt = time.Now()
		}
		m.applyBacklightRules(false)
		iv := time.Duration(c.PollSeconds) * time.Second
		if iv < 3*time.Second {
			iv = 3 * time.Second
		}
		m.sleep(iv)
	}
}

func (m *manager) sleep(d time.Duration) {
	select {
	case <-time.After(d):
	case <-m.wake:
	}
}

func (m *manager) dev() *hidppDevice { m.mu.Lock(); defer m.mu.Unlock(); return m.hp }

func (m *manager) isConnected() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.connected }
func (m *manager) setConnected(v bool) {
	m.mu.Lock()
	m.connected = v
	m.mu.Unlock()
	setTrayIconState(v)
}

func (m *manager) open() bool {
	c := getConfig()
	match := strings.ToLower(c.DeviceNameMatch)
	for _, info := range findHIDPP() {
		if c.DevicePID != "" && !strings.EqualFold(fmt.Sprintf("%04x", info.PID), c.DevicePID) {
			continue
		}
		d, err := openHIDDevice(info)
		if err != nil {
			continue
		}
		hp := newHIDPP(d, m.onEvent)
		name := info.Product
		if hp.ping() == nil {
			if n := hp.deviceName(); n != "" {
				name = n
			}
		}
		if match != "" && !strings.Contains(strings.ToLower(name), match) && !strings.Contains(strings.ToLower(info.Product), match) {
			d.Close()
			continue
		}
		m.mu.Lock()
		m.hp, m.info, m.name, m.pid = hp, info, name, pidFromPath(info.Path)
		if m.pid == "" {
			m.pid = fmt.Sprintf("%04x", info.PID)
		}
		m.mu.Unlock()
		logf("HID++ kanalı açıldı: %s (PID %s) %s", name, m.pid, info.Path)
		return true
	}
	return false
}

func (m *manager) closeDev() {
	m.mu.Lock()
	hp := m.hp
	m.hp = nil
	m.connected = false
	m.diverted = map[uint16]bool{}
	m.mu.Unlock()
	if hp != nil {
		hp.dev.Close()
	}
	setTrayIconState(false)
	trayRefresh()
}

func (m *manager) onConnect() {
	hp := m.hp
	hp.resetFeatures() // indexes can change after firmware update; re-resolve
	if ctrls, err := hp.listControls(); err == nil {
		m.mu.Lock()
		m.controls = ctrls
		m.mu.Unlock()
	}
	m.readBattery()
	if b, err := hp.getBacklight(); err == nil {
		m.mu.Lock()
		m.bl, m.blKnown = b, true
		m.mu.Unlock()
	}
	m.applyDiversions()
	m.applyBacklightRules(true)
}

func (m *manager) readBattery() {
	hp := m.dev()
	if hp == nil {
		return
	}
	b, err := hp.battery()
	if err != nil {
		return
	}
	m.mu.Lock()
	m.batt = b
	m.mu.Unlock()
	c := getConfig()
	if c.LowBatteryWarn > 0 && b.Percent <= c.LowBatteryWarn && !b.Charging {
		notify("MX Keys Mini pili azaldı", fmt.Sprintf("Pil %%%d. Şarja takmayı unutma.", b.Percent))
	}
	trayRefresh()
}

// wantedDiversions: keys that have macros in config, or all divertable keys in learn mode.
func (m *manager) wantedDiversions() map[uint16]bool {
	mxCIDs := getConfig().mxTriggerCIDs()
	want := map[uint16]bool{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ctl := range m.controls {
		if !ctl.Divertable() {
			continue
		}
		if m.learnMode || m.capturing || mxCIDs[ctl.CID] {
			want[ctl.CID] = true
		}
	}
	return want
}

func (m *manager) applyDiversions() {
	hp := m.dev()
	if hp == nil || !m.isConnected() {
		return
	}
	want := m.wantedDiversions()
	m.mu.Lock()
	controls := m.controls
	cur := m.diverted
	m.mu.Unlock()
	next := map[uint16]bool{}
	for _, ctl := range controls {
		if !ctl.Divertable() {
			continue
		}
		on := want[ctl.CID]
		if on != cur[ctl.CID] || on {
			if err := hp.setDivert(ctl.CID, on); err != nil {
				logf("Tuş 0x%04X yönlendirme hatası: %v", ctl.CID, err)
				continue
			}
		}
		if on {
			next[ctl.CID] = true
		}
	}
	m.mu.Lock()
	m.diverted = next
	m.mu.Unlock()
	if len(next) > 0 {
		var ks []string
		for k := range next {
			ks = append(ks, fmt.Sprintf("0x%04X", k))
		}
		sort.Strings(ks)
		logf("Yönlendirilen tuşlar: %s", strings.Join(ks, ", "))
	}
}

func (m *manager) setCapture(on bool) {
	m.mu.Lock()
	m.capturing = on
	m.mu.Unlock()
	go m.applyDiversions()
}

func (m *manager) setLearnMode(on bool) {
	m.mu.Lock()
	m.learnMode = on
	m.mu.Unlock()
	go m.applyDiversions()
}

func (m *manager) onEvent(msg hidppMsg) {
	hp := m.dev()
	if hp == nil {
		return
	}
	idx := func(id uint16) byte {
		if v, ok := hp.cached(id); ok && v != 0 {
			return v
		}
		return 0xFE
	}
	switch msg.feat {
	case idx(featReprogV4):
		if msg.fn != 0 {
			return
		}
		now := map[uint16]bool{}
		for i := 0; i+1 < 8; i += 2 {
			c := uint16(msg.p[i])<<8 | uint16(msg.p[i+1])
			if c != 0 {
				now[c] = true
			}
		}
		m.mu.Lock()
		prev := m.pressed
		m.pressed = now
		learn := m.learnMode
		m.mu.Unlock()
		for c := range now {
			if !prev[c] {
				detector.onNormalKey() // diverted keys prove the keyboard is alive
				if learn && !capturer.active() {
					notify("Tuş öğrenme", fmt.Sprintf("CID 0x%04X (%s)\nTetikleyici olarak \"mx:0x%04X\" kullan.", c, cidName(c), c))
					logf("Öğrenme: CID 0x%04X basıldı", c)
					continue
				}
				queueMX(c, true)
			}
		}
		for c := range prev {
			if !now[c] {
				queueMX(c, false)
			}
		}
	case idx(featUnifiedBatt):
		if msg.fn == 0 {
			m.mu.Lock()
			m.batt = parseUnifiedBattery(msg.p)
			m.mu.Unlock()
			trayRefresh()
		}
	case idx(featBacklight2):
		// user changed backlight with keys: refresh cached state
		go func() {
			if b, err := hp.getBacklight(); err == nil {
				m.mu.Lock()
				m.bl, m.blKnown = b, true
				m.mu.Unlock()
				trayRefresh()
			}
		}()
	}
}

// ---------- backlight rules ----------

func (m *manager) activeBacklightTarget() (key string, t *blTarget) {
	c := getConfig()
	m.mu.Lock()
	batt := m.batt
	m.mu.Unlock()
	if c.Backlight.LowBatteryOffPercent > 0 && batt.Known && !batt.Charging && batt.Percent <= c.Backlight.LowBatteryOffPercent {
		return "lowbatt", &blTarget{Mode: "off"}
	}
	nowMin := time.Now().Hour()*60 + time.Now().Minute()
	for i, r := range c.Backlight.Rules {
		if r.matches(nowMin, foregroundProcessName()) {
			return fmt.Sprintf("rule%d", i), &blTarget{Mode: r.Mode, Level: r.Level}
		}
	}
	if c.Backlight.Default != "" && c.Backlight.Default != "leave" {
		return "default", &blTarget{Mode: c.Backlight.Default, Level: c.Backlight.DefaultLevel}
	}
	return "none", nil
}

func (m *manager) applyBacklightRules(force bool) {
	hp := m.dev()
	if hp == nil || !m.isConnected() || !hp.has(featBacklight2) {
		return
	}
	key, t := m.activeBacklightTarget()
	m.mu.Lock()
	prevKey := m.lastRuleKey
	if t == nil && prevKey != "" && prevKey != "none" {
		// a rule just ended: hand control back to the keyboard's automatic mode
		t = &blTarget{Mode: "auto"}
	}
	if t == nil && m.blOverride == nil {
		m.lastRuleKey = key
		m.mu.Unlock()
		return
	}
	if key != m.lastRuleKey {
		if m.lastRuleKey != "" {
			m.blOverride = nil
		}
		m.lastRuleKey = key
		force = true
	}
	if m.blOverride != nil && key != "lowbatt" {
		t = m.blOverride
	}
	m.mu.Unlock()
	if t == nil {
		return
	}
	sig := fmt.Sprintf("%s/%d", t.Mode, t.Level)
	m.mu.Lock()
	same := sig == m.lastApplied
	m.mu.Unlock()
	if same && !force {
		return
	}
	c := getConfig()
	if err := hp.applyBacklight(t.Mode, t.Level, c.Backlight.HandsOutSec, c.Backlight.HandsInSec, c.Backlight.PoweredSec); err != nil {
		logf("Işık ayarlanamadı: %v", err)
		return
	}
	logf("Işık: %s (seviye %d) [%s]", t.Mode, t.Level, key)
	m.mu.Lock()
	m.lastApplied = sig
	m.mu.Unlock()
	if b, err := hp.getBacklight(); err == nil {
		m.mu.Lock()
		m.bl, m.blKnown = b, true
		m.mu.Unlock()
	}
	trayRefresh()
}

func (m *manager) setBacklightOverride(mode string, level int) {
	m.mu.Lock()
	m.blOverride = &blTarget{Mode: mode, Level: level}
	m.lastApplied = ""
	m.mu.Unlock()
	go m.applyBacklightRules(true)
}

func (m *manager) stepBacklight(delta int) {
	m.mu.Lock()
	b := m.bl
	m.mu.Unlock()
	lvl := int(b.Level) + delta
	if lvl < 0 {
		lvl = 0
	}
	if b.Levels > 0 && lvl > b.Levels-1 {
		lvl = b.Levels - 1
	}
	m.setBacklightOverride("manual", lvl)
}

func (m *manager) toggleBacklight() {
	m.mu.Lock()
	b := m.bl
	m.mu.Unlock()
	if b.Enabled != 0 {
		m.setBacklightOverride("off", 0)
	} else {
		m.setBacklightOverride("auto", 0)
	}
}

// ---------- diagnostics ----------

func (m *manager) diagnostics() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "MX Helper tanılama - %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, "Yönetici olarak çalışıyor: %v\n\n", isElevated())
	sb.WriteString("== Logitech HID++ kanalları ==\n")
	for _, i := range findHIDPP() {
		fmt.Fprintf(&sb, "PID %04X  page %04X usage %04X in %d out %d  %q\n  %s\n", i.PID, i.UsagePage, i.Usage, i.InLen, i.OutLen, i.Product, i.Path)
	}
	m.mu.Lock()
	hp, conn := m.hp, m.connected
	m.mu.Unlock()
	fmt.Fprintf(&sb, "\nSeçili cihaz: %s  bağlı: %v\n", m.name, conn)
	if hp != nil && conn {
		sb.WriteString("\n== Özellikler ==\n")
		for _, f := range hp.listFeatures() {
			fmt.Fprintf(&sb, "[%2d] 0x%04X %-24s tip 0x%02X\n", f.idx, f.id, f.name, f.typ)
		}
		if b, err := hp.battery(); err == nil {
			fmt.Fprintf(&sb, "\nPil: %%%d şarj:%v dolu:%v\n", b.Percent, b.Charging, b.Full)
		}
		if b, err := hp.getBacklight(); err == nil {
			fmt.Fprintf(&sb, "\nIşık: enabled=%d options=0x%02X (mod %d) supported=0x%02X effects=0x%04X level=%d/%d dho=%d dhi=%d dpow=%d (x5 sn)\n",
				b.Enabled, b.Options, b.Mode(), b.Supported, b.Effects, b.Level, b.Levels, b.DHO, b.DHI, b.DPow)
		} else {
			fmt.Fprintf(&sb, "\nIşık okunamadı: %v\n", err)
		}
		if ctrls, err := hp.listControls(); err == nil {
			sb.WriteString("\n== Tuşlar (ReprogControlsV4) ==\n")
			for _, c := range ctrls {
				fmt.Fprintf(&sb, "CID 0x%04X TID 0x%04X flags 0x%02X yönlendirilebilir:%-5v pos %d grup %d  %s\n", c.CID, c.TID, c.Flags, c.Divertable(), c.Pos, c.Group, cidName(c.CID))
			}
		}
	}
	sb.WriteString("\n== Raw input'ta görülen MX cihazları ==\n")
	for _, n := range rawSeenDevices() {
		sb.WriteString(n + "\n")
	}
	sb.WriteString("\n== Çakışabilecek Logitech yazılımları ==\n")
	for _, p := range runningProcesses() {
		lp := strings.ToLower(p)
		if strings.Contains(lp, "logi") || strings.Contains(lp, "lghub") {
			sb.WriteString(p + "\n")
		}
	}
	sb.WriteString("\n== Onarım geçmişi ==\n")
	st := loadState()
	fmt.Fprintf(&sb, "İşe yarayan son seviye: %d, başarı sayıları: %v\n", st.LastWorkingLevel, st.Successes)
	return sb.String()
}
