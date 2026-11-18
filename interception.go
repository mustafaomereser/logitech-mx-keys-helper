//go:build windows

package main

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Interception (Oblita) is a keyboard/mouse filter driver used by macro and game tools.
// It installs itself as an UpperFilter of the keyboard and mouse classes ("keyboard",
// "mouse") and has a fixed number of device slots that are only freed on reboot. Every
// Bluetooth reconnect is a new device, so after enough wake-ups the MX keyboard's input
// goes nowhere until the next reboot — the exact "only media keys work" hang. No repair
// level can fix that, so we detect it and offer to remove it.

const (
	kbdClassKey   = `SYSTEM\CurrentControlSet\Control\Class\{4d36e96b-e325-11ce-bfc1-08002be10318}`
	mouseClassKey = `SYSTEM\CurrentControlSet\Control\Class\{4d36e96f-e325-11ce-bfc1-08002be10318}`
)

// regMultiString reads a REG_MULTI_SZ value from HKLM.
func regMultiString(key, value string) []string {
	const rrfMulti = 0x20 | 0x00010000 // RRF_RT_REG_MULTI_SZ | RRF_SUBKEY_WOW6464KEY
	var size uint32
	pRegGetValueW.Call(0x80000002, uintptr(unsafe.Pointer(u16(key))), uintptr(unsafe.Pointer(u16(value))), rrfMulti, 0, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return nil
	}
	buf := make([]uint16, size/2+2)
	r, _, _ := pRegGetValueW.Call(0x80000002, uintptr(unsafe.Pointer(u16(key))), uintptr(unsafe.Pointer(u16(value))), rrfMulti, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return nil
	}
	var out []string
	for start, i := 0, 0; i < len(buf); i++ {
		if buf[i] == 0 {
			if i == start {
				break
			}
			out = append(out, syscall.UTF16ToString(buf[start:i]))
			start = i + 1
		}
	}
	return out
}

func hasFilter(list []string, name string) bool {
	for _, f := range list {
		if strings.EqualFold(f, name) {
			return true
		}
	}
	return false
}

// interceptionFilters returns which Interception filters are registered ("keyboard", "mouse").
func interceptionFilters() []string {
	var out []string
	if hasFilter(regMultiString(kbdClassKey, "UpperFilters"), "keyboard") {
		out = append(out, "keyboard")
	}
	if hasFilter(regMultiString(mouseClassKey, "UpperFilters"), "mouse") {
		out = append(out, "mouse")
	}
	return out
}

var interception struct {
	mu        sync.Mutex
	removed   bool // removal ran this session: a reboot is pending
	checkedAt time.Time
	present   bool
}

// interceptionState: installed (filters registered) and whether we removed it this session.
func interceptionState() (present, removed bool) {
	interception.mu.Lock()
	defer interception.mu.Unlock()
	if time.Since(interception.checkedAt) > 10*time.Second {
		interception.present = len(interceptionFilters()) > 0
		interception.checkedAt = time.Now()
	}
	return interception.present, interception.removed
}

// checkInterceptionAtStart warns once per start when the driver is installed.
func checkInterceptionAtStart() {
	f := interceptionFilters()
	if len(f) == 0 {
		return
	}
	logf("Interception filtre sürücüsü kurulu (%s): klavye uykudan sonra takılabilir", strings.Join(f, ", "))
	notifyAction(L("Interception sürücüsü kurulu", "Interception driver is installed"),
		L("Bu sürücü yüzünden klavye uykudan sonra takılır ve sadece yeniden başlatınca düzelir. Kaldırmak için arayüzü aç.", "Because of this driver the keyboard hangs after sleep and only a restart fixes it. Open the window to remove it."),
		openGUI)
}

const removeInterceptionPS = `$ErrorActionPreference = 'Continue'
foreach($c in @(
  @{ Key = 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e96b-e325-11ce-bfc1-08002be10318}'; Drop = 'keyboard'; Keep = 'kbdclass' },
  @{ Key = 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e96f-e325-11ce-bfc1-08002be10318}'; Drop = 'mouse'; Keep = 'mouclass' })) {
  $cur = @((Get-ItemProperty $c.Key).UpperFilters)
  $new = @($cur | Where-Object { $_ -and $_ -ne $c.Drop })
  if ($new -notcontains $c.Keep) { $new += $c.Keep }
  "UpperFilters: $($cur -join ',') -> $($new -join ',')"
  Set-ItemProperty $c.Key UpperFilters -Type MultiString -Value $new
}
`

// removeInterception takes Interception out of the class filter lists (elevated, UAC
// prompt when needed) and waits until the registry shows it gone. The driver itself
// stays loaded until the next reboot.
func removeInterception() bool {
	logf("Interception kaldırılıyor")
	script := "& {\n\"===== " + time.Now().Format("2006-01-02 15:04:05") + " Interception kaldırma =====\"\n" +
		removeInterceptionPS + "} *>> '" + strings.ReplaceAll(appDir()+`\repair.log`, "'", "''") + "'\n"
	if err := runPowerShell(script, true); err != nil {
		logf("Interception kaldırılamadı: %v", err)
		return false
	}
	// with a UAC prompt the script runs detached: poll the registry
	for range 60 {
		if len(interceptionFilters()) == 0 {
			interception.mu.Lock()
			interception.removed, interception.present, interception.checkedAt = true, false, time.Now()
			interception.mu.Unlock()
			logf("Interception kaldırıldı; yeniden başlatma gerekiyor")
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	logf("Interception kaldırılamadı (izin verilmedi ya da zaman aşımı)")
	return false
}
