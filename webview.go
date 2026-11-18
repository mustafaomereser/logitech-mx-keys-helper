//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// The GUI runs in our own top-level window hosting WebView2, so MX Helper is one
// program with one taskbar button. WebView2 is driven through raw COM vtables
// (no cgo, no WebView2Loader.dll): we load the runtime's EmbeddedBrowserWebView.dll
// and call CreateWebViewEnvironmentWithOptionsInternal, which is what the loader does.
// Everything here runs on the UI thread (STA + message loop in runUI).
// If the runtime is missing or creation fails, openURLWindow (Edge --app) is used instead.

var (
	ole32  = syscall.NewLazyDLL("ole32.dll")
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")

	pCoInitializeEx          = ole32.NewProc("CoInitializeEx")
	pDwmSetWindowAttribute   = dwmapi.NewProc("DwmSetWindowAttribute")
	pCreateSolidBrush        = gdi32.NewProc("CreateSolidBrush")
	pDestroyWindow           = user32.NewProc("DestroyWindow")
	pGetClientRect           = user32.NewProc("GetClientRect")
	pSetWindowPos            = user32.NewProc("SetWindowPos")
	pGetDpiForWindow         = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem         = user32.NewProc("GetDpiForSystem")
	pLoadCursorW             = user32.NewProc("LoadCursorW")
	pSetProcessDpiAwareCtx   = user32.NewProc("SetProcessDpiAwarenessContext")
	pChangeWindowMsgFilterEx = user32.NewProc("ChangeWindowMessageFilterEx")
	pAllowSetForegroundWin   = user32.NewProc("AllowSetForegroundWindow")
	pUpdateWindow            = user32.NewProc("UpdateWindow")
)

const (
	wmOpenGUI      = wmApp + 5
	wmQuitApp      = wmApp + 6 // quit from another goroutine (self-update)
	guiClassName   = "MXHelperGUI"
	wmSize         = 0x0005
	wmActivate     = 0x0006
	wmClose        = 0x0010
	wmGetMinMax    = 0x0024
	wmDpiChanged   = 0x02E0
	wsOverlappedW  = 0x00CF0000
	cwUseDefault   = 0x80000000
	guiBG          = 0x00131111 // COLORREF (BGR) of #111113
)

// enableDPIAwareness: per-monitor v2, so the GUI and menus are sharp on scaled displays.
func enableDPIAwareness() {
	if pSetProcessDpiAwareCtx.Find() == nil {
		pSetProcessDpiAwareCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}
}

// ---------- runtime discovery ----------

func regString(root uintptr, key, value string) string {
	var size uint32
	pRegGetValueW.Call(root, uintptr(unsafe.Pointer(u16(key))), uintptr(unsafe.Pointer(u16(value))), 0x2|0x00010000, 0, 0, uintptr(unsafe.Pointer(&size))) // RRF_RT_REG_SZ | RRF_SUBKEY_WOW6464KEY
	if size == 0 {
		return ""
	}
	buf := make([]uint16, size/2+1)
	r, _, _ := pRegGetValueW.Call(root, uintptr(unsafe.Pointer(u16(key))), uintptr(unsafe.Pointer(u16(value))), 0x2|0x00010000, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// webviewDLL returns the path of EmbeddedBrowserWebView.dll of the installed runtime.
func webviewDLL() string {
	const guid = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	type loc struct {
		root uintptr
		key  string
	}
	for _, l := range []loc{
		{0x80000002, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + guid}, // HKLM
		{0x80000002, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + guid},
		{0x80000001, `Software\Microsoft\EdgeUpdate\Clients\` + guid}, // HKCU (per-user install)
	} {
		dir, ver := regString(l.root, l.key, "location"), regString(l.root, l.key, "pv")
		if dir == "" || ver == "" || ver == "0.0.0.0" {
			continue
		}
		p := filepath.Join(dir, ver, "EBWebView", "x64", "EmbeddedBrowserWebView.dll")
		if fileExists(p) {
			return p
		}
	}
	// registry missing or stale: pick the newest version folder on disk
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		m, _ := filepath.Glob(filepath.Join(base, "Microsoft", "EdgeWebView", "Application", "*", "EBWebView", "x64", "EmbeddedBrowserWebView.dll"))
		if len(m) > 0 {
			sort.Slice(m, func(i, j int) bool { return versionLess(m[j], m[i]) })
			return m[0]
		}
	}
	return ""
}

func versionLess(a, b string) bool {
	va := strings.Split(filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(a)))), ".")
	vb := strings.Split(filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(b)))), ".")
	for i := 0; i < len(va) && i < len(vb); i++ {
		if len(va[i]) != len(vb[i]) {
			return len(va[i]) < len(vb[i])
		}
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return len(va) < len(vb)
}

// ---------- COM helpers ----------

func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	p := *(*unsafe.Pointer)(unsafe.Pointer(&obj))
	vt := *(*unsafe.Pointer)(p)
	fn := *(*uintptr)(unsafe.Add(vt, idx*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

// handler is a minimal COM object: IUnknown + Invoke. WebView2 only calls these four slots.
type handler struct{ vtbl *[4]uintptr }

func newHandler(invoke func(this, hr, result uintptr) uintptr) *handler {
	vt := &[4]uintptr{
		syscall.NewCallback(func(this, riid, ppv uintptr) uintptr { **(**uintptr)(unsafe.Pointer(&ppv)) = this; return 0 }),
		syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
		syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
		syscall.NewCallback(invoke),
	}
	return &handler{vtbl: vt}
}

func (h *handler) ptr() uintptr { return uintptr(unsafe.Pointer(h)) }

// ---------- GUI window ----------

var gui struct {
	hwnd    uintptr
	env     uintptr
	ctrl    uintptr
	view    uintptr
	url     string
	classOK bool
}

var (
	guiWndProcCb = syscall.NewCallback(guiWndProc)
	envDone      = newHandler(onEnvCreated)
	ctrlDone     = newHandler(onControllerCreated)
)

func dpiOf(hwnd uintptr) int {
	var d uintptr
	if hwnd != 0 && pGetDpiForWindow.Find() == nil {
		d, _, _ = pGetDpiForWindow.Call(hwnd)
	} else if pGetDpiForSystem.Find() == nil {
		d, _, _ = pGetDpiForSystem.Call()
	}
	if d == 0 {
		return 96
	}
	return int(d)
}

// showGUIWindow runs on the UI thread (wmOpenGUI).
func showGUIWindow() {
	if gui.hwnd != 0 {
		if r, _, _ := pIsIconic.Call(gui.hwnd); r != 0 {
			pShowWindow.Call(gui.hwnd, 9) // SW_RESTORE
		}
		pSetForegroundWindow.Call(gui.hwnd)
		return
	}
	dll := webviewDLL()
	if dll == "" {
		logf("WebView2 bulunamadı; arayüz Edge ile açılıyor")
		go openGUIOnce(guiURL())
		return
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	if !gui.classOK {
		icon, _, _ := pLoadIconW.Call(inst, 1) // RT_GROUP_ICON 1 from rsrc_windows_amd64.syso
		cursor, _, _ := pLoadCursorW.Call(0, 32512)
		brush, _, _ := pCreateSolidBrush.Call(guiBG)
		wc := wndClassEx{WndProc: guiWndProcCb, Instance: inst, ClassName: u16(guiClassName), Icon: icon, IconSm: icon, Cursor: cursor, Background: brush}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		gui.classOK = true
	}
	d := dpiOf(0)
	w, h := 1280*d/96, 860*d/96
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16(guiClassName))), uintptr(unsafe.Pointer(u16("MX Helper"))),
		wsOverlappedW, cwUseDefault, cwUseDefault, uintptr(w), uintptr(h), 0, 0, inst, 0)
	if hwnd == 0 {
		go openGUIOnce(guiURL())
		return
	}
	gui.hwnd, gui.url = hwnd, guiURL()
	one := uint32(1)
	pDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&one)), 4) // DWMWA_USE_IMMERSIVE_DARK_MODE
	bg := uint32(guiBG)
	pDwmSetWindowAttribute.Call(hwnd, 35, uintptr(unsafe.Pointer(&bg)), 4) // DWMWA_CAPTION_COLOR (Windows 11)
	pShowWindow.Call(hwnd, 5)                                               // SW_SHOW
	pUpdateWindow.Call(hwnd)
	pSetForegroundWindow.Call(hwnd)

	create := syscall.NewLazyDLL(dll).NewProc("CreateWebViewEnvironmentWithOptionsInternal")
	if create.Find() != nil {
		guiFallback("WebView2 giriş noktası yok")
		return
	}
	dataDir := filepath.Join(appDir(), "webview2")
	if isElevated() {
		dataDir += "-admin" // elevated and normal browser processes cannot share a profile
	}
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "FF111113") // no white flash before the page paints
	r, _, _ := create.Call(1, 0, uintptr(unsafe.Pointer(u16(dataDir))), 0, envDone.ptr())
	if r != 0 {
		guiFallback("WebView2 ortamı oluşturulamadı")
	}
}

// guiFallback closes our window and opens the GUI in Edge instead.
func guiFallback(why string) {
	logf("%s; arayüz Edge ile açılıyor", why)
	closeGUIWindow()
	go openGUIOnce(guiURL())
}

func onEnvCreated(this, hr, env uintptr) uintptr {
	if int32(hr) < 0 || env == 0 || gui.hwnd == 0 {
		guiFallback("WebView2 ortamı başlatılamadı")
		return 0
	}
	comCall(env, 1) // AddRef: we keep it
	gui.env = env
	if r := comCall(env, 3, gui.hwnd, ctrlDone.ptr()); int32(r) < 0 { // CreateCoreWebView2Controller
		guiFallback("WebView2 denetleyicisi oluşturulamadı")
	}
	return 0
}

func onControllerCreated(this, hr, ctrl uintptr) uintptr {
	if int32(hr) < 0 || ctrl == 0 || gui.hwnd == 0 {
		guiFallback("WebView2 penceresi açılamadı")
		return 0
	}
	comCall(ctrl, 1)
	gui.ctrl = ctrl
	var view uintptr
	comCall(ctrl, 25, uintptr(unsafe.Pointer(&view))) // get_CoreWebView2
	gui.view = view
	if view != 0 {
		var s uintptr
		comCall(view, 3, uintptr(unsafe.Pointer(&s))) // get_Settings
		if s != 0 {
			comCall(s, 10, 0) // put_IsStatusBarEnabled(false)
			comCall(s, 12, 0) // put_AreDevToolsEnabled(false)
			comCall(s, 18, 0) // put_IsZoomControlEnabled(false)
			comRelease(s)
		}
	}
	resizeGUI()
	comCall(ctrl, 4, 1) // put_IsVisible(true)
	if view != 0 {
		comCall(view, 5, uintptr(unsafe.Pointer(u16(gui.url)))) // Navigate
	}
	comCall(ctrl, 12, 0) // MoveFocus(programmatic)
	return 0
}

func resizeGUI() {
	if gui.ctrl == 0 {
		return
	}
	var rc [4]int32
	pGetClientRect.Call(gui.hwnd, uintptr(unsafe.Pointer(&rc)))
	comCall(gui.ctrl, 6, uintptr(unsafe.Pointer(&rc))) // put_Bounds (RECT passed by reference on x64)
}

func closeGUIWindow() {
	if gui.ctrl != 0 {
		comCall(gui.ctrl, 24) // Close
	}
	comRelease(gui.view)
	comRelease(gui.ctrl)
	comRelease(gui.env) // last reference: the browser processes exit
	h := gui.hwnd
	gui.view, gui.ctrl, gui.env, gui.hwnd = 0, 0, 0, 0
	if h != 0 {
		pDestroyWindow.Call(h)
	}
}

func guiWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmSize:
		resizeGUI()
		return 0
	case wmActivate:
		if wparam&0xFFFF != 0 && gui.ctrl != 0 {
			comCall(gui.ctrl, 12, 0)
		}
	case wmGetMinMax:
		d := dpiOf(hwnd)
		mmi := *(**[10]int32)(unsafe.Pointer(&lparam))
		mmi[6], mmi[7] = int32(900*d/96), int32(600*d/96) // ptMinTrackSize
		return 0
	case wmDpiChanged:
		rc := *(**[4]int32)(unsafe.Pointer(&lparam))
		pSetWindowPos.Call(hwnd, 0, uintptr(rc[0]), uintptr(rc[1]), uintptr(rc[2]-rc[0]), uintptr(rc[3]-rc[1]), 0x0014) // NOZORDER|NOACTIVATE
		return 0
	case wmClose:
		closeGUIWindow()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}
