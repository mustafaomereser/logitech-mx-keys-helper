//go:build windows

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

//go:embed assets/icon_on.ico
var iconOnData []byte

//go:embed assets/icon_off.ico
var iconOffData []byte

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        GUID
	BalloonIcon     uintptr
}

const (
	wmDestroy           = 0x0002
	wmCommand           = 0x0111
	wmNull              = 0x0000
	wmInput             = 0x00FF
	wmInputDeviceChange = 0x00FE
	wmPowerBroadcast    = 0x0218
	wmContextMenu       = 0x007B
	wmLButtonUp         = 0x0202
	wmApp               = 0x8000
	wmTray              = wmApp + 1
	wmNotify            = wmApp + 2
	wmRefresh           = wmApp + 3

	ninSelect           = 0x0400
	ninBalloonTimeout   = 0x0404
	ninBalloonUserClick = 0x0405

	nifMessage  = 0x01
	nifIcon     = 0x02
	nifTip      = 0x04
	nifInfo     = 0x10
	nifShowTip  = 0x80
	nimAdd      = 0
	nimModify   = 1
	nimDelete   = 2
	nimSetVer   = 4
	niifInfo    = 0x1
	niifWarning = 0x2

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfPopup     = 0x0010
	mfSeparator = 0x0800
	mftRadio    = 0x0200 // MFT_RADIOCHECK: bullet instead of a tick
)

var (
	trayHwnd    uintptr
	wndProcCb   = syscall.NewCallback(wndProc)
	iconOn      uintptr
	iconOff     uintptr
	taskbarMsg  uint32
	trayOnline  bool
	notifyMu    sync.Mutex
	notifyQueue []pendingNotify
	balloonCb   func()
)

type pendingNotify struct {
	title, text string
	action      func()
}

func notify(title, text string)                 { queueNotify(pendingNotify{title, text, nil}) }
func notifyAction(title, text string, f func()) { queueNotify(pendingNotify{title, text, f}) }
func queueNotify(n pendingNotify) {
	notifyMu.Lock()
	notifyQueue = append(notifyQueue, n)
	notifyMu.Unlock()
	if trayHwnd != 0 {
		pPostMessageW.Call(trayHwnd, wmNotify, 0, 0)
	}
}

func trayRefresh() {
	if trayHwnd != 0 {
		pPostMessageW.Call(trayHwnd, wmRefresh, 0, 0)
	}
}

func setTrayIconState(online bool) {
	notifyMu.Lock()
	trayOnline = online
	notifyMu.Unlock()
	trayRefresh()
}

func baseNID() notifyIconData {
	var n notifyIconData
	n.Size = uint32(unsafe.Sizeof(n))
	n.Wnd = trayHwnd
	n.ID = 1
	return n
}

func loadIcon(data []byte, name string) uintptr {
	p := filepath.Join(appDir(), name)
	os.WriteFile(p, data, 0644)
	h, _, _ := pLoadImageW.Call(0, uintptr(unsafe.Pointer(u16(p))), 1, 16, 16, 0x10) // IMAGE_ICON, LR_LOADFROMFILE
	if h == 0 {
		h, _, _ = pLoadIconW.Call(0, 32512) // IDI_APPLICATION
	}
	return h
}

func trayAdd() {
	n := baseNID()
	n.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	n.CallbackMessage = wmTray
	n.Icon = batteryTrayIcon(false, batteryState{})
	if n.Icon == 0 {
		n.Icon = iconOff
	}
	u16copy(n.Tip[:], "MX Helper")
	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&n)))
	n.Version = 4
	pShellNotifyIconW.Call(nimSetVer, uintptr(unsafe.Pointer(&n)))
	updateTray()
}

func updateTray() {
	conn, name, b, _, _ := mgr.snapshot()
	tip := L("MX Helper - bağlı değil", "MX Helper - not connected")
	if conn {
		tip = name + L(" - bağlı", " - connected")
		if b.Known {
			tip += fmt.Sprintf(L(" - pil %%%d", " - battery %d%%"), b.Percent)
			if b.Charging {
				tip += L(" (şarjda)", " (charging)")
			}
		}
	}
	n := baseNID()
	n.Flags = nifIcon | nifTip | nifShowTip
	notifyMu.Lock()
	online := trayOnline
	notifyMu.Unlock()
	n.Icon = batteryTrayIcon(online && conn, b)
	if n.Icon == 0 { // drawing failed: fall back to the embedded icons
		n.Icon = iconOff
		if online {
			n.Icon = iconOn
		}
	}
	if gm, _ := gameModeActive(); gm {
		tip += L(" - oyun modu", " - game mode")
	}
	u16copy(n.Tip[:], tip)
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

func flushNotifications() {
	notifyMu.Lock()
	q := notifyQueue
	notifyQueue = nil
	notifyMu.Unlock()
	for _, it := range q {
		n := baseNID()
		n.Flags = nifInfo
		u16copy(n.InfoTitle[:], it.title)
		u16copy(n.Info[:], it.text)
		n.InfoFlags = niifInfo
		balloonCb = it.action
		pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
	}
}

// ---------- menu ----------

const (
	idOpenGUI = 200 + iota
	idStopAll
)

const (
	idStatus = 1 + iota
	idBattery
	idRepairNow
	idAutoAsk
	idAutoAuto
	idAutoOff
	idBlAuto
	idBlOff
	idBlRules
	idLearn
	idOpenCfg
	idOpenLog
	idOpenRepairLog
	idDiag
	idAutostart
	idGameMode
	idUpdate
	idQuit
	idBlLevelBase = 100
)

func appendMenu(m uintptr, flags uint32, id uintptr, text string) {
	pAppendMenuW.Call(m, uintptr(flags), id, uintptr(unsafe.Pointer(u16(text))))
}

func check(b bool) uint32 {
	if b {
		return mfChecked
	}
	return 0
}

func showMenu() {
	conn, name, b, bl, blKnown := mgr.snapshot()
	c := getConfig()
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)

	// status row: battery glyph in the level colour, name, and the percentage right-aligned
	if name == "" {
		name = "MX Keys Mini"
	}
	status := name + L("\tbağlı değil", "\tnot connected")
	if conn {
		switch {
		case !b.Known:
			status = name + L("\tbağlı", "\tconnected")
		case b.Charging:
			status = fmt.Sprintf(L("%s\t%%%d · şarjda", "%s\t%d%% · charging"), name, b.Percent)
		default:
			status = fmt.Sprintf(L("%s\t%%%d", "%s\t%d%%"), name, b.Percent)
		}
	}
	appendMenu(m, mfString, idStatus, status)
	bmp := menuBatteryBitmap(conn, b)
	if bmp != 0 {
		setMenuItemBitmap(m, idStatus, bmp)
		defer pDeleteObject.Call(bmp)
	}
	pSetMenuDefaultItm.Call(m, idStatus, 0) // bold; also what a double-click would do
	if u := updateSnapshot(); u.Available {
		label := L("Güncelle: ", "Update: ") + u.Latest
		if u.State == "downloading" || u.State == "installing" {
			label = fmt.Sprintf(L("Güncelleniyor… %%%d", "Updating… %d%%"), u.Progress)
		}
		appendMenu(m, mfString, idUpdate, label)
	}
	appendMenu(m, mfSeparator, 0, "")
	appendMenu(m, mfString, idRepairNow, L("Klavyeyi onar\tMute ×3", "Repair keyboard\tMute ×3"))
	if len(engine.runningIDs()) > 0 {
		appendMenu(m, mfString, idStopAll, L("Çalışan makroları durdur", "Stop running macros"))
	}
	appendMenu(m, mfSeparator, 0, "")

	bls, _, _ := pCreatePopupMenu.Call()
	if blKnown {
		appendMenu(bls, check(bl.Enabled != 0 && bl.Mode() == 1)|mftRadio, idBlAuto, L("Otomatik", "Auto"))
		appendMenu(bls, check(bl.Enabled == 0)|mftRadio, idBlOff, L("Kapalı", "Off"))
		appendMenu(bls, mfSeparator, 0, "")
		levels := bl.Levels
		if levels <= 0 {
			levels = 8
		}
		for i := 0; i < levels && i < 32; i++ {
			appendMenu(bls, check(bl.Enabled != 0 && bl.Mode() == 3 && int(bl.Level) == i)|mftRadio, uintptr(idBlLevelBase+i), fmt.Sprintf(L("Seviye %d", "Level %d"), i))
		}
		appendMenu(bls, mfSeparator, 0, "")
		appendMenu(bls, mfString, idBlRules, L("Kurallara dön", "Back to rules"))
	} else {
		appendMenu(bls, mfGrayed, 0, L("Klavye bağlı değil", "Keyboard not connected"))
	}
	blText := L("Arka ışık", "Backlight")
	if blKnown {
		switch {
		case bl.Enabled == 0:
			blText += L("\tkapalı", "\toff")
		case bl.Mode() == 1:
			blText += L("\totomatik", "\tauto")
		default:
			blText += fmt.Sprintf(L("\tseviye %d", "\tlevel %d"), bl.Level)
		}
	}
	appendMenu(m, mfPopup, bls, blText)

	gm, _ := gameModeActive()
	appendMenu(m, check(gm), idGameMode, L("Oyun modu", "Game mode"))

	sub, _, _ := pCreatePopupMenu.Call()
	appendMenu(sub, check(c.Repair.Auto == "ask")|mftRadio, idAutoAsk, L("Bildirimle sor", "Ask with a notification"))
	appendMenu(sub, check(c.Repair.Auto == "auto")|mftRadio, idAutoAuto, L("Sormadan onar", "Repair without asking"))
	appendMenu(sub, check(c.Repair.Auto == "off")|mftRadio, idAutoOff, L("Bir şey yapma (sadece Mute ×3)", "Do nothing (Mute ×3 only)"))
	appendMenu(m, mfPopup, sub, L("Takılma fark edilince", "When a hang is detected"))
	appendMenu(m, mfSeparator, 0, "")

	mgr.mu.Lock()
	learn := mgr.learnMode
	mgr.mu.Unlock()
	adv, _, _ := pCreatePopupMenu.Call()
	appendMenu(adv, check(learn), idLearn, L("Tuş öğrenme modu", "Key learning mode"))
	appendMenu(adv, mfSeparator, 0, "")
	appendMenu(adv, mfString, idDiag, L("Tanılama raporu oluştur", "Create diagnostics report"))
	appendMenu(adv, mfString, idOpenLog, L("Günlüğü aç", "Open log"))
	appendMenu(adv, mfString, idOpenRepairLog, L("Onarım günlüğünü aç", "Open repair log"))
	appendMenu(adv, mfString, idOpenCfg, L("config.json dosyasını aç", "Open config.json"))
	appendMenu(m, mfPopup, adv, L("Gelişmiş", "Advanced"))
	appendMenu(m, check(autostartEnabled()), idAutostart, L("Windows açılışında başlat", "Start with Windows"))
	appendMenu(m, mfString, idOpenGUI, L("Ayarlar…", "Settings…"))
	appendMenu(m, mfSeparator, 0, "")
	appendMenu(m, mfString, idQuit, L("Çıkış", "Quit"))

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(trayHwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, 0x0100|0x0002|0x0020, uintptr(pt.X), uintptr(pt.Y), 0, trayHwnd, 0) // RETURNCMD|RIGHTBUTTON|BOTTOMALIGN
	pPostMessageW.Call(trayHwnd, wmNull, 0, 0)
	handleCommand(int(cmd))
}

func handleCommand(id int) {
	switch {
	case id == idOpenGUI || id == idStatus:
		go openGUI()
	case id == idStopAll:
		engine.stopAll()
	case id == idRepairNow:
		go repairer.repair("menü")
	case id == idAutoAsk:
		setConfigValue([]string{"repair", "auto"}, "ask")
	case id == idAutoAuto:
		setConfigValue([]string{"repair", "auto"}, "auto")
	case id == idAutoOff:
		setConfigValue([]string{"repair", "auto"}, "off")
	case id == idBlAuto:
		mgr.setBacklightOverride("auto", 0)
	case id == idBlOff:
		mgr.setBacklightOverride("off", 0)
	case id == idBlRules:
		go mgr.clearBacklightOverride()
	case id >= idBlLevelBase && id < idBlLevelBase+32:
		mgr.setBacklightOverride("manual", id-idBlLevelBase)
	case id == idLearn:
		mgr.mu.Lock()
		on := !mgr.learnMode
		mgr.mu.Unlock()
		mgr.setLearnMode(on)
		if on {
			notify(L("Tuş öğrenme modu açık", "Key learning mode on"), L("Özel tuşlara (emoji, dikte, mikrofon, ekran görüntüsü...) bas; her birinin CID'si burada görünecek.", "Press the special keys (emoji, dictation, mic, screenshot...); each one's CID shows up here."))
		}
	case id == idOpenCfg:
		shellExecute("open", "notepad.exe", `"`+configPath()+`"`, 1)
	case id == idOpenLog:
		shellExecute("open", "notepad.exe", `"`+logPath()+`"`, 1)
	case id == idOpenRepairLog:
		p := filepath.Join(appDir(), "repair.log")
		if _, err := os.Stat(p); err != nil {
			os.WriteFile(p, []byte{}, 0644)
		}
		shellExecute("open", "notepad.exe", `"`+p+`"`, 1)
	case id == idDiag:
		go func() {
			p := filepath.Join(appDir(), "tanilama.txt")
			os.WriteFile(p, []byte(mgr.diagnostics()), 0644)
			shellExecute("open", "notepad.exe", `"`+p+`"`, 1)
		}()
	case id == idGameMode:
		go setGameModeEnabled(!getConfig().GameMode.Enabled)
	case id == idAutostart:
		go setAutostart(!autostartEnabled())
	case id == idUpdate:
		go applyUpdate()
	case id == idQuit:
		closeGUIWindow()
		n := baseNID()
		pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&n)))
		engine.stopAll()
		mgr.mu.Lock()
		mgr.learnMode = false
		mgr.mu.Unlock()
		// release diverted keys so they work normally again
		mgr.mu.Lock()
		hp, div := mgr.hp, mgr.diverted
		mgr.mu.Unlock()
		if hp != nil {
			for cid := range div {
				hp.setDivert(cid, false)
			}
		}
		pPostQuitMessage.Call(0)
	}
}

// setConfigValue edits one key in config.json (keeps the other keys).
func setConfigValue(path []string, value any) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		notify(L("Ayar dosyasında hata", "Error in the config file"), L("config.json okunamadı; elle düzelt.", "config.json couldn't be read; fix it by hand."))
		return
	}
	cur := root
	for _, k := range path[:len(path)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
	out, _ := json.MarshalIndent(root, "", "  ")
	os.WriteFile(configPath(), out, 0644)
	loadConfig()
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmInput:
		handleRawInput(lparam)
	case wmInputDeviceChange:
		if wparam == 2 { // GIDC_REMOVAL
			forgetRawDevice(lparam)
		}
	case wmTray:
		switch uint32(lparam & 0xFFFF) {
		case wmContextMenu:
			showMenu()
		case ninSelect:
			go openGUI()
		case ninBalloonUserClick:
			if balloonCb != nil {
				f := balloonCb
				balloonCb = nil
				f()
			}
		case ninBalloonTimeout:
			balloonCb = nil
		}
		return 0
	case wmNotify:
		flushNotifications()
		return 0
	case wmRefresh:
		updateTray()
		return 0
	case wmHookUpdate:
		requestHookUpdate()
		return 0
	case wmOpenGUI:
		showGUIWindow()
		return 0
	case wmQuitApp:
		handleCommand(idQuit)
		return 0
	case wmPowerBroadcast:
		if wparam == 0x12 { // PBT_APMRESUMEAUTOMATIC
			detector.onReconnect()
			mgr.kick()
		}
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	default:
		if taskbarMsg != 0 && uint32(msg) == taskbarMsg { // explorer restarted
			trayAdd()
			return 0
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func runUI() {
	runtime.LockOSThread()
	pCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED: WebView2 needs an STA with a message loop
	inst, _, _ := pGetModuleHandleW.Call(0)
	cls := u16("MXHelperWnd")
	wc := wndClassEx{WndProc: wndProcCb, Instance: inst, ClassName: cls}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("MX Helper"))), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	trayHwnd = hwnd
	enableDarkMenus()
	// a second, non-elevated instance may ask an elevated one to show its window
	pChangeWindowMsgFilterEx.Call(hwnd, wmOpenGUI, 1, 0) // MSGFLT_ALLOW
	tm, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	taskbarMsg = uint32(tm)
	iconOn = loadIcon(iconOnData, "icon_on.ico")
	iconOff = loadIcon(iconOffData, "icon_off.ico")
	trayAdd()
	startHookThread()
	if err := registerRawInput(hwnd); err != nil {
		logf("Raw input kaydı başarısız: %v", err)
	}
	flushNotifications()

	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
