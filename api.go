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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"
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
			http.SetCookie(w, &http.Cookie{Name: cookieName("mxh"), Value: guiToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			// mxw=1 tells the page it runs in our own window and must draw the title bar
			win := &http.Cookie{Name: cookieName("mxw"), Value: "1", Path: "/", SameSite: http.SameSiteStrictMode}
			if r.URL.Query().Get("w") != "1" {
				win.Value, win.MaxAge = "", -1
			}
			http.SetCookie(w, win)
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
			mgr.clearBacklightOverride()
		} else {
			mgr.storeBacklightOverride(req.Mode, req.Level)
			mgr.applyBacklightRules(true) // synchronously, so the response carries the new state
		}
		apiState(w, r)
	})
	api("POST /api/repair", func(w http.ResponseWriter, r *http.Request) {
		go repairer.repair("arayüz")
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/repair/reset", func(w http.ResponseWriter, r *http.Request) {
		saveState(State{Backlight: loadState().Backlight}) // reset repair history only
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
			Moves bool `json:"moves"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		recorder.start(req.Mouse, req.Mouse && req.Moves)
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
			Pos        int    `json:"pos"` // F-key position (1..12), 0 = none
		}
		out := []item{}
		for _, c := range ctrls {
			out = append(out, item{"mx:0x" + strings.ToLower(hex4(c.CID)), cidName(c.CID), c.Divertable(), int(c.Pos)})
		}
		writeJSON(w, out)
	})
	api("GET /api/processes", func(w http.ResponseWriter, r *http.Request) {
		seen := map[string]bool{}
		out := []string{}
		for _, p := range runningProcesses() {
			lp := strings.ToLower(p)
			if !strings.HasSuffix(lp, ".exe") || seen[lp] || systemProcess[lp] {
				continue
			}
			seen[lp] = true
			out = append(out, p)
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
		writeJSON(w, out)
	})
	api("GET /api/cursor", func(w http.ResponseWriter, r *http.Request) {
		d, _ := strconv.Atoi(r.URL.Query().Get("delay"))
		if d > 0 && d <= 10 {
			time.Sleep(time.Duration(d) * time.Second)
		}
		x, y := cursorPos()
		writeJSON(w, map[string]any{"x": x, "y": y})
	})
	api("GET /api/keys/stream", apiKeyStream)
	api("GET /api/update", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, updateSnapshot()) })
	api("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, checkUpdate()) })
	api("POST /api/update/apply", func(w http.ResponseWriter, r *http.Request) {
		go applyUpdate()
		writeJSON(w, map[string]any{"ok": true})
	})
	api("POST /api/update/notes", func(w http.ResponseWriter, r *http.Request) {
		openReleasePage()
		writeJSON(w, map[string]any{"ok": true})
	})
	api("GET /api/window", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"max": guiMaximized()})
	})
	api("POST /api/window", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Cmd, Hit string }
		json.NewDecoder(r.Body).Decode(&req)
		hits := map[string]uintptr{"caption": 2, "top": 12, "topleft": 13, "topright": 14}
		cmds := map[string]uintptr{"min": guiCmdMin, "max": guiCmdMax, "close": guiCmdClose}
		ok := false
		if req.Cmd == "drag" && hits[req.Hit] != 0 {
			ok = postGUICmd(guiCmdDrag, hits[req.Hit])
		} else if cmds[req.Cmd] != 0 {
			ok = postGUICmd(cmds[req.Cmd], 0)
		}
		writeJSON(w, map[string]bool{"ok": ok})
	})
	api("POST /api/gamemode", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			On bool `json:"on"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		setGameModeEnabled(req.On)
		time.Sleep(800 * time.Millisecond) // let the watcher pick it up
		apiState(w, r)
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

// systemProcess hides Windows background processes from the app picker.
var systemProcess = map[string]bool{
	"svchost.exe": true, "csrss.exe": true, "wininit.exe": true, "winlogon.exe": true, "services.exe": true,
	"lsass.exe": true, "smss.exe": true, "dwm.exe": true, "fontdrvhost.exe": true, "conhost.exe": true,
	"runtimebroker.exe": true, "sihost.exe": true, "taskhostw.exe": true, "ctfmon.exe": true, "dllhost.exe": true,
	"searchindexer.exe": true, "searchhost.exe": true, "startmenuexperiencehost.exe": true, "shellexperiencehost.exe": true,
	"textinputhost.exe": true, "securityhealthservice.exe": true, "spoolsv.exe": true, "wmiprvse.exe": true,
	"audiodg.exe": true, "registry": true, "system": true, "memory compression": true, "msmpeng.exe": true,
	"nissrv.exe": true, "lsaiso.exe": true, "wudfhost.exe": true, "mxhelper.exe": true, "applicationframehost.exe": true,
	"backgroundtaskhost.exe": true, "smartscreen.exe": true, "useroobebroker.exe": true, "widgetservice.exe": true,
	"crossdeviceservice.exe": true, "searchprotocolhost.exe": true, "searchfilterhost.exe": true, "sgrmbroker.exe": true,
	"securityhealthsystray.exe": true, "unsecapp.exe": true, "dashost.exe": true, "msedgewebview2.exe": true,
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

// cookieName is per port: cookies ignore ports, so without this another MX Helper
// instance on 127.0.0.1 (an update, a second copy) would overwrite our session.
func cookieName(base string) string {
	_, port, _ := strings.Cut(guiAddr, ":")
	return base + "_" + port
}

func authed(r *http.Request) bool {
	if c, err := r.Cookie(cookieName("mxh")); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(guiToken)) == 1 {
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
	GameMode   bool           `json:"game_mode"`
	GameReason string         `json:"game_mode_reason"`
	Update     updateInfo     `json:"update"`
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
	gm, gr := gameModeActive()
	writeJSON(w, apiStateResp{
		GameMode: gm, GameReason: gr, Update: updateSnapshot(),
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

// guiWindow finds the open GUI window: Edge --app windows use the Chromium
// window class and the page <title> ("MX Helper"). Our own hidden tray window
// has the same title but a different class, so it never matches.
func guiWindow() uintptr {
	h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("Chrome_WidgetWin_1"))), uintptr(unsafe.Pointer(u16("MX Helper"))))
	return h
}

// focusGUIWindow brings an already open GUI window to the front. Returns false if none is open.
func focusGUIWindow() bool {
	h := guiWindow()
	if h == 0 {
		return false
	}
	if r, _, _ := pIsIconic.Call(h); r != 0 {
		pShowWindow.Call(h, 9) // SW_RESTORE
	}
	pSetForegroundWindow.Call(h)
	return true
}

var guiOpen struct {
	sync.Mutex
	last time.Time
}

// openGUIOnce opens the GUI unless it is already open (then it is focused) or
// was launched moments ago and has not shown its window yet.
func openGUIOnce(url string) {
	guiOpen.Lock()
	defer guiOpen.Unlock()
	if focusGUIWindow() {
		return
	}
	if time.Since(guiOpen.last) < 5*time.Second {
		return // Edge is still starting; repeated tray clicks must not open more windows
	}
	guiOpen.last = time.Now()
	openURLWindow(url)
}

// openGUI shows the GUI window (our own WebView2 window; Edge as fallback).
// Safe to call from any goroutine: the window lives on the UI thread.
func openGUI() {
	if guiAddr == "" {
		return
	}
	if trayHwnd != 0 {
		pPostMessageW.Call(trayHwnd, wmOpenGUI, 0, 0)
		return
	}
	openGUIOnce(guiURL())
}

// openRunningGUI is used by a second instance to show the first one's window.
func openRunningGUI() bool {
	if h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("MXHelperWnd"))), 0); h != 0 {
		pAllowSetForegroundWin.Call(^uintptr(0)) // ASFW_ANY: let the running instance take the foreground
		pPostMessageW.Call(h, wmOpenGUI, 0, 0)
		return true
	}
	b, err := os.ReadFile(filepath.Join(appDir(), "gui.url"))
	if err != nil || !strings.HasPrefix(string(b), "http://127.0.0.1:") {
		return false
	}
	openGUIOnce(string(b))
	return true
}
