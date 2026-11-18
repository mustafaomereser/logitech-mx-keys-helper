//go:build windows

package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

//go:embed web
var webFS embed.FS

var (
	guiToken string
	guiAddr  string
)

func guiURL() string { return "http://" + guiAddr + "/?t=" + guiToken }

func startGUIServer() error {
	b := make([]byte, 24)
	rand.Read(b)
	guiToken = hex.EncodeToString(b)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	guiAddr = ln.Addr().String()
	os.WriteFile(filepath.Join(appDir(), "gui.url"), []byte(guiURL()), 0600)

	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(guiToken)) == 1 {
			http.SetCookie(w, &http.Cookie{Name: "mxh", Value: guiToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if !authed(r) {
			http.Error(w, "Bu sayfayı görev çubuğundaki MX Helper simgesinden aç.", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	api := func(pattern string, h func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			h(w, r)
		})
	}

	api("GET /api/state", apiState)
	api("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, getConfig()) })
	api("PUT /api/config", apiSaveConfig)
	api("GET /api/config/raw", func(w http.ResponseWriter, r *http.Request) {
		b, _ := os.ReadFile(configPath())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(b)
	})
	api("PUT /api/config/raw", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		c, err := parseConfig(b)
		if err != nil {
			httpErr(w, "JSON hatası: "+err.Error())
			return
		}
		if err := saveConfig(c); err != nil {
			httpErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/backlight", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mode  string `json:"mode"`
			Level int    `json:"level"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Mode == "rules" {
			mgr.mu.Lock()
			mgr.blOverride = nil
			mgr.lastApplied = ""
			mgr.mu.Unlock()
			mgr.applyBacklightRules(true)
		} else {
			mgr.mu.Lock()
			mgr.blOverride = &blTarget{Mode: req.Mode, Level: req.Level}
			mgr.lastApplied = ""
			mgr.mu.Unlock()
			mgr.applyBacklightRules(true)
		}
		apiState(w, r)
	})
	api("POST /api/repair", func(w http.ResponseWriter, r *http.Request) {
		go repairer.repair("arayüz")
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/repair/reset", func(w http.ResponseWriter, r *http.Request) {
		saveState(State{})
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/autostart", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			On bool `json:"on"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		setAutostart(req.On)
		time.Sleep(1500 * time.Millisecond)
		writeJSON(w, map[string]any{"on": autostartEnabled()})
	})
	api("POST /api/capture", func(w http.ResponseWriter, r *http.Request) {
		t, ok := capturer.wait(15 * time.Second)
		if !ok {
			httpErr(w, "Zaman aşımı: 15 saniye içinde tuşa basılmadı.")
			return
		}
		writeJSON(w, map[string]any{"trigger": t})
	})
	api("POST /api/capture/cancel", func(w http.ResponseWriter, r *http.Request) {
		capturer.offer("")
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/record/start", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mouse bool `json:"mouse"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		recorder.start(req.Mouse)
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/record/stop", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Delays bool `json:"delays"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, map[string]any{"actions": recorder.stop(req.Delays)})
	})
	api("POST /api/macro/test", func(w http.ResponseWriter, r *http.Request) {
		var m Macro
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			httpErr(w, err.Error())
			return
		}
		go testMacro(m)
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/macro/stopall", func(w http.ResponseWriter, r *http.Request) {
		engine.stopAll()
		writeJSON(w, map[string]any{"ok": true})
	})
	api("GET /api/foreground", func(w http.ResponseWriter, r *http.Request) {
		d, _ := strconv.Atoi(r.URL.Query().Get("delay"))
		if d > 0 && d <= 10 {
			time.Sleep(time.Duration(d) * time.Second)
		}
		writeJSON(w, map[string]any{"app": foregroundProcessName()})
	})
	api("GET /api/controls", func(w http.ResponseWriter, r *http.Request) {
		mgr.mu.Lock()
		ctrls := mgr.controls
		mgr.mu.Unlock()
		type item struct {
			CID        string `json:"cid"`
			Name       string `json:"name"`
			Divertable bool   `json:"divertable"`
		}
		var out []item
		for _, c := range ctrls {
			out = append(out, item{"mx:0x" + strings.ToLower(hex4(c.CID)), cidName(c.CID), c.Divertable()})
		}
		writeJSON(w, out)
	})
	api("GET /api/diag", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, mgr.diagnostics())
	})
	api("GET /api/log", func(w http.ResponseWriter, r *http.Request) {
		name := "log.txt"
		if r.URL.Query().Get("f") == "repair" {
			name = "repair.log"
		}
		b, _ := os.ReadFile(filepath.Join(appDir(), name))
		if name == "repair.log" {
			b = decodeMaybeUTF16(b)
		}
		if len(b) > 64<<10 {
			b = b[len(b)-64<<10:]
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(b)
	})

	srv := &http.Server{Handler: hostCheck(mux), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	logf("Arayüz: http://%s", guiAddr)
	return nil
}

func hex4(v uint16) string {
	const d = "0123456789ABCDEF"
	return string([]byte{d[v>>12&0xF], d[v>>8&0xF], d[v>>4&0xF], d[v&0xF]})
}

// hostCheck blocks DNS-rebinding: only accept requests addressed to our own loopback address.
func hostCheck(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != guiAddr {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+guiAddr {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func authed(r *http.Request) bool {
	if c, err := r.Cookie("mxh"); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(guiToken)) == 1 {
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decodeMaybeUTF16(b []byte) []byte {
	// PowerShell 5 appends UTF-16LE; strip NULs cheaply when it looks like UTF-16
	if len(b) >= 2 && ((b[0] == 0xFF && b[1] == 0xFE) || (len(b) > 3 && b[1] == 0 && b[3] == 0)) {
		var u []uint16
		start := 0
		if b[0] == 0xFF && b[1] == 0xFE {
			start = 2
		}
		for i := start; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return []byte(string(utf16.Decode(u)))
	}
	return b
}

type apiStateResp struct {
	Version    string         `json:"version"`
	Connected  bool           `json:"connected"`
	Name       string         `json:"name"`
	Battery    batteryState   `json:"battery"`
	Backlight  map[string]any `json:"backlight"`
	Elevated   bool           `json:"elevated"`
	Autostart  bool           `json:"autostart"`
	Repair     map[string]any `json:"repair"`
	Running    []string       `json:"running"`
	LogiApps   []string       `json:"logi_apps"`
	OverrideBL bool           `json:"backlight_override"`
}

var autostartCache struct {
	at time.Time
	on bool
}

func apiState(w http.ResponseWriter, r *http.Request) {
	conn, name, b, bl, blKnown := mgr.snapshot()
	if time.Since(autostartCache.at) > 15*time.Second {
		autostartCache.on, autostartCache.at = autostartEnabled(), time.Now()
	}
	st := loadState()
	logi := []string{}
	for _, p := range runningProcesses() {
		lp := strings.ToLower(p)
		if strings.HasPrefix(lp, "logioptionsplus") || strings.HasPrefix(lp, "lghub") {
			logi = append(logi, p)
		}
	}
	mgr.mu.Lock()
	ov := mgr.blOverride != nil
	mgr.mu.Unlock()
	writeJSON(w, apiStateResp{
		Version: version, Connected: conn, Name: name, Battery: b,
		Backlight: map[string]any{"known": blKnown, "enabled": bl.Enabled != 0, "mode": bl.Mode(), "level": bl.Level, "levels": bl.Levels},
		Elevated:  isElevated(), Autostart: autostartCache.on,
		Repair:  map[string]any{"last_working_level": st.LastWorkingLevel, "successes": st.Successes},
		Running: engine.runningIDs(), LogiApps: logi, OverrideBL: ov,
	})
}

func apiSaveConfig(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	c, err := parseConfig(b)
	if err != nil {
		httpErr(w, err.Error())
		return
	}
	for _, m := range c.Macros {
		if m.Enabled && normalizeTrigger(m.Trigger) == "" {
			httpErr(w, "\""+m.Name+"\" makrosunun tetikleyicisi geçersiz: "+m.Trigger)
			return
		}
	}
	if err := saveConfig(c); err != nil {
		httpErr(w, err.Error())
		return
	}
	writeJSON(w, getConfig())
}

// ---------- window ----------

func edgePath() string {
	for _, p := range []string{
		os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
		os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
		os.Getenv("LOCALAPPDATA") + `\Microsoft\Edge\Application\msedge.exe`,
	} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func openURLWindow(url string) {
	e := edgePath()
	if e == "" {
		shellExecute("open", url, "", 1)
		return
	}
	profile := filepath.Join(appDir(), "webview")
	args := []string{"--app=" + url, "--window-size=1180,820", "--user-data-dir=" + profile, "--no-first-run", "--disable-features=Translate"}
	if !isElevated() {
		if exec.Command(e, args...).Start() == nil {
			return
		}
		shellExecute("open", url, "", 1)
		return
	}
	// We run elevated (autostart task). Browsers should not run as admin, so start Edge
	// through Explorer, which launches it with the normal user token.
	q := func(s string) string { return `""` + s + `""` }
	cmdline := q(e)
	for _, a := range args {
		cmdline += " " + q(a)
	}
	vbs := filepath.Join(appDir(), "open-gui.vbs")
	os.WriteFile(vbs, []byte("CreateObject(\"WScript.Shell\").Run \""+cmdline+"\", 1, False\r\n"), 0644)
	if exec.Command("explorer.exe", vbs).Start() != nil {
		shellExecute("open", url, "", 1)
	}
}

func openGUI() {
	if guiAddr == "" {
		return
	}
	openURLWindow(guiURL())
}

// openRunningGUI is used by a second instance to show the first one's window.
func openRunningGUI() bool {
	b, err := os.ReadFile(filepath.Join(appDir(), "gui.url"))
	if err != nil || !strings.HasPrefix(string(b), "http://127.0.0.1:") {
		return false
	}
	openURLWindow(string(b))
	return true
}
