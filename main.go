//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var version = "dev"

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func hasArg(a string) bool {
	for _, x := range os.Args[1:] {
		if x == a {
			return true
		}
	}
	return false
}

func main() {
	enableDPIAwareness()
	finishUpdate() // after a self-update: wait for the old process to release the mutex
	r, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16(`Local\MXKeysHelper`))))
	if r != 0 && err == syscall.Errno(183) { // ERROR_ALREADY_EXISTS
		if !openRunningGUI() {
			messageBox("MX Helper", "MX Helper zaten çalışıyor (görev çubuğunun sağındaki simge).")
		}
		return
	}
	if !fileExists(configPath()) {
		// remember first run before loadConfig writes the default file
		os.Setenv("MXHELPER_FIRSTRUN", "1")
	}
	if err := loadConfig(); err != nil {
		logf("%v", err)
		notify("Ayar dosyasında hata", err.Error())
	}
	firstRun := os.Getenv("MXHELPER_FIRSTRUN") == "1"
	engine.rebuild()
	logf("MX Helper %s başladı (yönetici: %v)", version, isElevated())
	if err := startGUIServer(); err != nil {
		logf("Arayüz sunucusu başlatılamadı: %v", err)
	}
	if firstRun || hasArg("--gui") {
		go func() { time.Sleep(1500 * time.Millisecond); openGUI() }()
	}
	go watchConfig()
	go watchGameMode()
	go watchUpdates()
	go checkInterceptionAtStart()
	go mgr.run()
	go func() {
		time.Sleep(4 * time.Second)
		var found []string
		for _, p := range runningProcesses() {
			lp := strings.ToLower(p)
			if strings.HasPrefix(lp, "logioptionsplus") || strings.HasPrefix(lp, "lghub") {
				found = append(found, p)
			}
		}
		if len(found) > 0 {
			logf("Çalışan Logitech yazılımları: %v", found)
			notify("Logitech yazılımı açık", "Options+ / G HUB açıkken özel tuş ve ışık ayarları çakışabilir. Sorun olursa onları kapat.")
		}
	}()
	runUI()
}
