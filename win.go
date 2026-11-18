//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	hidDLL   = syscall.NewLazyDLL("hid.dll")
	cfgmgr32 = syscall.NewLazyDLL("cfgmgr32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pCreateEventW           = kernel32.NewProc("CreateEventW")
	pGetOverlappedResult    = kernel32.NewProc("GetOverlappedResult")
	pCancelIoEx             = kernel32.NewProc("CancelIoEx")
	pWaitForSingleObject    = kernel32.NewProc("WaitForSingleObject")
	pResetEvent             = kernel32.NewProc("ResetEvent")
	pCreateMutexW           = kernel32.NewProc("CreateMutexW")
	pGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
	pOpenProcess            = kernel32.NewProc("OpenProcess")
	pQueryFullProcessImageW = kernel32.NewProc("QueryFullProcessImageNameW")
	pCreateToolhelp32Snap   = kernel32.NewProc("CreateToolhelp32Snapshot")
	pProcess32FirstW        = kernel32.NewProc("Process32FirstW")
	pProcess32NextW         = kernel32.NewProc("Process32NextW")

	pCMGetIfaceListSizeW = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	pCMGetIfaceListW     = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")

	pHidDGetAttributes     = hidDLL.NewProc("HidD_GetAttributes")
	pHidDGetPreparsedData  = hidDLL.NewProc("HidD_GetPreparsedData")
	pHidDFreePreparsedData = hidDLL.NewProc("HidD_FreePreparsedData")
	pHidPGetCaps           = hidDLL.NewProc("HidP_GetCaps")
	pHidDGetProductString  = hidDLL.NewProc("HidD_GetProductString")

	pRegisterClassExW        = user32.NewProc("RegisterClassExW")
	pCreateWindowExW         = user32.NewProc("CreateWindowExW")
	pDefWindowProcW          = user32.NewProc("DefWindowProcW")
	pGetMessageW             = user32.NewProc("GetMessageW")
	pTranslateMessage        = user32.NewProc("TranslateMessage")
	pDispatchMessageW        = user32.NewProc("DispatchMessageW")
	pPostMessageW            = user32.NewProc("PostMessageW")
	pPostQuitMessage         = user32.NewProc("PostQuitMessage")
	pRegisterRawInputDevices = user32.NewProc("RegisterRawInputDevices")
	pGetRawInputData         = user32.NewProc("GetRawInputData")
	pGetRawInputDeviceInfoW  = user32.NewProc("GetRawInputDeviceInfoW")
	pCreatePopupMenu         = user32.NewProc("CreatePopupMenu")
	pAppendMenuW             = user32.NewProc("AppendMenuW")
	pTrackPopupMenu          = user32.NewProc("TrackPopupMenu")
	pDestroyMenu             = user32.NewProc("DestroyMenu")
	pGetCursorPos            = user32.NewProc("GetCursorPos")
	pSetForegroundWindow     = user32.NewProc("SetForegroundWindow")
	pLoadImageW              = user32.NewProc("LoadImageW")
	pLoadIconW               = user32.NewProc("LoadIconW")
	pRegisterWindowMessageW  = user32.NewProc("RegisterWindowMessageW")
	pSendInput               = user32.NewProc("SendInput")
	pGetForegroundWindow     = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcId   = user32.NewProc("GetWindowThreadProcessId")
	pMessageBoxW             = user32.NewProc("MessageBoxW")
	pGetSystemMetrics        = user32.NewProc("GetSystemMetrics")
	pFindWindowW             = user32.NewProc("FindWindowW")
	pShowWindow              = user32.NewProc("ShowWindow")
	pIsIconic                = user32.NewProc("IsIconic")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW    = shell32.NewProc("ShellExecuteW")

	pOpenProcessToken    = advapi32.NewProc("OpenProcessToken")
	pGetTokenInformation = advapi32.NewProc("GetTokenInformation")
)

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func u16copy(dst []uint16, s string) {
	src, _ := syscall.UTF16FromString(s)
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var hidGUID = GUID{0x4D1E55B2, 0xF16F, 0x11CF, [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}

// ---------- process helpers ----------

type processEntry32 struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

func runningProcesses() []string {
	h, _, _ := pCreateToolhelp32Snap.Call(0x2, 0) // TH32CS_SNAPPROCESS
	if h == uintptr(syscall.InvalidHandle) {
		return nil
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	var e processEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []string
	r, _, _ := pProcess32FirstW.Call(h, uintptr(unsafe.Pointer(&e)))
	for r != 0 {
		out = append(out, syscall.UTF16ToString(e.ExeFile[:]))
		r, _, _ = pProcess32NextW.Call(h, uintptr(unsafe.Pointer(&e)))
	}
	return out
}

func foregroundProcessName() string {
	hwnd, _, _ := pGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	pGetWindowThreadProcId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if h == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	r, _, _ := pQueryFullProcessImageW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return ""
	}
	full := syscall.UTF16ToString(buf[:n])
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '\\' {
			return full[i+1:]
		}
	}
	return full
}

func isElevated() bool {
	var tok syscall.Token
	cur, _ := syscall.GetCurrentProcess()
	r, _, _ := pOpenProcessToken.Call(uintptr(cur), 0x0008, uintptr(unsafe.Pointer(&tok))) // TOKEN_QUERY
	if r == 0 {
		return false
	}
	defer tok.Close()
	var elev uint32
	var ret uint32
	r, _, _ = pGetTokenInformation.Call(uintptr(tok), 20, uintptr(unsafe.Pointer(&elev)), 4, uintptr(unsafe.Pointer(&ret))) // TokenElevation
	return r != 0 && elev != 0
}

func shellExecute(verb, file, args string, show int) error {
	var a uintptr
	if args != "" {
		a = uintptr(unsafe.Pointer(u16(args)))
	}
	r, _, _ := pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16(verb))), uintptr(unsafe.Pointer(u16(file))), a, 0, uintptr(show))
	if r <= 32 {
		return syscall.Errno(r)
	}
	return nil
}

func messageBox(title, text string) {
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), 0x40) // MB_ICONINFORMATION
}
