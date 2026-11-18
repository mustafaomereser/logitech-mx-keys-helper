//go:build windows

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Action struct {
	Type    string            `json:"type"`              // text | keys | keydown | keyup | click | mousedown | mouseup | move | scroll | run | shell | url | http | backlight | gamemode | repair | delay
	Text    string            `json:"text,omitempty"`    // text
	Keys    string            `json:"keys,omitempty"`    // keys / keydown / keyup: "ctrl+shift+esc", "space"
	Button  string            `json:"button,omitempty"`  // click / mousedown / mouseup: left | right | middle | mouse4 | mouse5
	HoldMs  int               `json:"hold_ms,omitempty"` // keys / click: how long to hold before releasing
	Count   int               `json:"count,omitempty"`   // click: 2 = double click
	X       int               `json:"x,omitempty"`       // move: screen pixel (or delta when Rel)
	Y       int               `json:"y,omitempty"`       // move
	Rel     bool              `json:"rel,omitempty"`     // move: relative to the current position
	Amount  int               `json:"amount,omitempty"`  // scroll: notches, positive = up / right
	Horiz   bool              `json:"horiz,omitempty"`   // scroll: horizontal wheel
	Cmd     string            `json:"cmd,omitempty"`     // run / shell
	Args    string            `json:"args,omitempty"`    // run
	URL     string            `json:"url,omitempty"`     // url / http
	Method  string            `json:"method,omitempty"`  // http
	Body    string            `json:"body,omitempty"`    // http
	Headers map[string]string `json:"headers,omitempty"` // http
	Value   string            `json:"value,omitempty"`   // backlight: up|down|toggle|auto|off|0..N; gamemode: toggle|on|off
	Ms      int               `json:"ms,omitempty"`      // delay
	MsMax   int               `json:"ms_max,omitempty"`  // delay: when > ms, wait a random time in [ms, ms_max]
}

// GameMode blocks keys that throw you out of a game.
type GameMode struct {
	Enabled     bool     `json:"enabled"`       // on regardless of the foreground app
	Apps        []string `json:"apps"`          // turns on automatically while one of these is in the foreground
	BlockWin    bool     `json:"block_win"`     // Windows keys
	BlockAltTab bool     `json:"block_alt_tab"` // Alt+Tab
	BlockAltF4  bool     `json:"block_alt_f4"`  // Alt+F4
}

func (g GameMode) configured() bool {
	return (g.Enabled || len(g.Apps) > 0) && (g.BlockWin || g.BlockAltTab || g.BlockAltF4)
}

// Macro: trigger + mode + actions.
//
//	trigger: "ctrl+alt+k", "f13", "mouse4", "mouse5", "middle", "mx:0x0108"
//	mode:    press  = run once (repeat times) on key down
//	         hold   = repeat actions every interval_ms while the trigger is held
//	         toggle = first press starts repeating, second press stops
type Macro struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Trigger     string   `json:"trigger"`
	Mode        string   `json:"mode"`
	IntervalMs  int      `json:"interval_ms,omitempty"`
	Repeat      int      `json:"repeat,omitempty"`
	App         string   `json:"app,omitempty"`
	Passthrough bool     `json:"passthrough,omitempty"`
	Actions     []Action `json:"actions"`
}

// legacy v1 format: "macros": { "0x0108": [ { "app": "", "actions": [...] } ] }
type legacyMacroRule struct {
	App     string   `json:"app,omitempty"`
	Actions []Action `json:"actions"`
}

type BacklightRule struct {
	From  string   `json:"from,omitempty"` // "22:00"
	To    string   `json:"to,omitempty"`   // "07:00"
	Apps  []string `json:"apps,omitempty"` // foreground exe names
	Mode  string   `json:"mode"`           // off | auto | manual
	Level int      `json:"level,omitempty"`
}

func parseHM(s string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return 0, false
	}
	return h*60 + m, true
}

func (r BacklightRule) matches(nowMin int, fg string) bool {
	if r.From != "" && r.To != "" {
		f, ok1 := parseHM(r.From)
		t, ok2 := parseHM(r.To)
		if !ok1 || !ok2 {
			return false
		}
		in := false
		if f <= t {
			in = nowMin >= f && nowMin < t
		} else {
			in = nowMin >= f || nowMin < t
		}
		if !in {
			return false
		}
	}
	if len(r.Apps) > 0 {
		hit := false
		for _, a := range r.Apps {
			if strings.EqualFold(a, fg) {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	return r.From != "" || len(r.Apps) > 0
}

type Config struct {
	DeviceNameMatch string `json:"device_name_match"`
	DevicePID       string `json:"device_pid"`
	PollSeconds     int    `json:"poll_seconds"`
	LowBatteryWarn  int    `json:"low_battery_warn_percent"`
	Repair          struct {
		Auto                  string `json:"auto"` // ask | auto | off
		MediaPressesToSuspect int    `json:"media_presses_to_suspect"`
	} `json:"repair"`
	Backlight struct {
		Default              string          `json:"default"` // leave | auto | off | manual
		DefaultLevel         int             `json:"default_level"`
		LowBatteryOffPercent int             `json:"low_battery_off_percent"`
		HandsOutSec          int             `json:"hands_out_seconds"`
		HandsInSec           int             `json:"hands_in_seconds"`
		PoweredSec           int             `json:"powered_seconds"`
		Rules                []BacklightRule `json:"rules"`
	} `json:"backlight"`
	Macros    []Macro  `json:"macros"`
	StopCombo string   `json:"stop_all_combo"`
	GameMode  GameMode `json:"game_mode"`
	Update    struct {
		Mode string `json:"mode"` // ask | auto | off
	} `json:"update"`
}

// mxTriggerCIDs returns the MX key CIDs used by enabled macros.
func (c *Config) mxTriggerCIDs() map[uint16]bool {
	out := map[uint16]bool{}
	for _, m := range c.Macros {
		t := normalizeTrigger(m.Trigger)
		if !m.Enabled || !strings.HasPrefix(t, "mx:") {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimPrefix(t, "mx:0x"), 16, 16); err == nil {
			out[uint16(n)] = true
		}
	}
	return out
}

const defaultConfig = `{
  "device_name_match": "MX Keys",
  "device_pid": "",
  "poll_seconds": 10,
  "low_battery_warn_percent": 10,
  "stop_all_combo": "ctrl+alt+pause",
  "game_mode": { "enabled": false, "apps": [], "block_win": true, "block_alt_tab": false, "block_alt_f4": false },
  "repair": { "auto": "ask", "media_presses_to_suspect": 3 },
  "backlight": {
    "default": "leave",
    "default_level": 4,
    "low_battery_off_percent": 15,
    "hands_out_seconds": 0,
    "hands_in_seconds": 0,
    "powered_seconds": 0,
    "rules": [ { "from": "23:00", "to": "07:00", "mode": "manual", "level": 1 } ]
  },
  "macros": [
    {
      "id": "ornek-turbo",
      "name": "Turbo sol tık (örnek)",
      "enabled": false,
      "trigger": "mouse5",
      "mode": "hold",
      "interval_ms": 30,
      "actions": [ { "type": "click", "button": "left", "hold_ms": 8 } ]
    }
  ]
}
`

var (
	cfgMu    sync.RWMutex
	cfg      *Config
	cfgMtime time.Time
)

func appDir() string {
	d := os.Getenv("APPDATA")
	if d == "" {
		d, _ = os.UserConfigDir()
	}
	p := filepath.Join(d, "MXHelper")
	os.MkdirAll(p, 0755)
	return p
}

func configPath() string { return filepath.Join(appDir(), "config.json") }

func getConfig() *Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg
}

func applyDefaults(c *Config) {
	if c.PollSeconds == 0 {
		c.PollSeconds = 10
	}
	if c.Repair.Auto == "" {
		c.Repair.Auto = "ask"
	}
	if c.Repair.MediaPressesToSuspect < 1 {
		c.Repair.MediaPressesToSuspect = 3
	}
	if c.Macros == nil {
		c.Macros = []Macro{}
	}
	if c.StopCombo == "" {
		c.StopCombo = "ctrl+alt+pause"
	}
	if c.Update.Mode == "" {
		c.Update.Mode = "ask"
	}
	if c.GameMode.Apps == nil {
		c.GameMode.Apps = []string{}
	}
	for i := range c.Macros {
		m := &c.Macros[i]
		if m.ID == "" {
			m.ID = newID()
		}
		if m.Mode == "" {
			m.Mode = "press"
		}
		if m.Actions == nil {
			m.Actions = []Action{}
		}
	}
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// parseConfig accepts the current format and migrates v1 (map of CID -> rules).
func parseConfig(data []byte) (*Config, error) {
	var c Config
	var raw struct {
		Macros json.RawMessage `json:"macros"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw.Macros))
	if strings.HasPrefix(trimmed, "{") {
		var legacy map[string][]legacyMacroRule
		if err := json.Unmarshal(raw.Macros, &legacy); err != nil {
			return nil, err
		}
		var tmp map[string]json.RawMessage
		json.Unmarshal(data, &tmp)
		delete(tmp, "macros")
		rest, _ := json.Marshal(tmp)
		if err := json.Unmarshal(rest, &c); err != nil {
			return nil, err
		}
		for cid, rules := range legacy {
			for _, r := range rules {
				name := "MX " + cid
				if r.App != "" {
					name += " (" + r.App + ")"
				}
				c.Macros = append(c.Macros, Macro{Name: name, Enabled: true, Trigger: "mx:" + cid, Mode: "press", App: r.App, Actions: r.Actions})
			}
		}
	} else if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	applyDefaults(&c)
	return &c, nil
}

// saveConfig writes the config and makes it current.
func saveConfig(c *Config) error {
	applyDefaults(c)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath(), b, 0644); err != nil {
		return err
	}
	st, _ := os.Stat(configPath())
	cfgMu.Lock()
	cfg = c
	if st != nil {
		cfgMtime = st.ModTime()
	}
	cfgMu.Unlock()
	onConfigChanged()
	return nil
}

func onConfigChanged() {
	engine.rebuild()
	requestHookUpdate()
	go func() {
		mgr.applyDiversions()
		mgr.mu.Lock()
		mgr.lastApplied = ""
		mgr.mu.Unlock()
		mgr.applyBacklightRules(true)
	}()
}

func loadConfig() error {
	p := configPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		os.WriteFile(p, []byte(defaultConfig), 0644)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	c, err := parseConfig(data)
	if err != nil {
		if getConfig() == nil {
			var empty Config
			applyDefaults(&empty)
			cfgMu.Lock()
			cfg = &empty
			cfgMu.Unlock()
		}
		return fmt.Errorf("config.json okunamadı: %w", err)
	}
	st, _ := os.Stat(p)
	cfgMu.Lock()
	cfg = c
	if st != nil {
		cfgMtime = st.ModTime()
	}
	cfgMu.Unlock()
	return nil
}

func watchConfig() {
	for {
		time.Sleep(2 * time.Second)
		st, err := os.Stat(configPath())
		if err != nil {
			continue
		}
		cfgMu.RLock()
		changed := !st.ModTime().Equal(cfgMtime)
		cfgMu.RUnlock()
		if !changed {
			continue
		}
		if err := loadConfig(); err != nil {
			cfgMu.Lock()
			cfgMtime = st.ModTime()
			cfgMu.Unlock()
			notify("Ayar dosyasında hata", err.Error())
			logf("%v", err)
			continue
		}
		logf("Ayarlar yeniden yüklendi")
		notify("MX Helper", "Ayarlar yeniden yüklendi.")
		onConfigChanged()
	}
}

// ---------- persistent state ----------

type State struct {
	LastWorkingLevel int            `json:"last_working_level"`
	Successes        map[string]int `json:"successes"`
	Backlight        *savedBacklight `json:"backlight,omitempty"`
}

// savedBacklight is the user's last manual backlight choice. It survives restarts
// and is dropped when the active rule changes (RuleKey no longer matches).
type savedBacklight struct {
	Mode    string `json:"mode"`
	Level   int    `json:"level"`
	RuleKey string `json:"rule_key"`
}

var stateMu sync.Mutex

func loadState() State {
	stateMu.Lock()
	defer stateMu.Unlock()
	var s State
	if b, err := os.ReadFile(filepath.Join(appDir(), "state.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func saveState(s State) {
	stateMu.Lock()
	defer stateMu.Unlock()
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(appDir(), "state.json"), b, 0644)
}

// ---------- log ----------

var logMu sync.Mutex

func logPath() string { return filepath.Join(appDir(), "log.txt") }

func logf(format string, a ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	p := logPath()
	if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}
