//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

// Repair ladder:
//  1: restart the keyboard's HID-over-GATT service node (BTHLEDEVICE\{00001812-...}) -> forces Windows
//     to rediscover the GATT reports and re-subscribe notifications.
//  2: disable/enable the Bluetooth radio (Intel adapter).
//  3: restart Bluetooth services + radio + HOGP node.

type repairState struct {
	mu        sync.Mutex
	running   bool
	lastAt    time.Time
	lastLevel int
	pendingAt time.Time
	pendingLv int
}

var repairer = &repairState{}

func psScript(level int, pid string, logPath string) string {
	p := strings.ToUpper(pid)
	findHogp := fmt.Sprintf(`$hogp = Get-PnpDevice -PresentOnly -ErrorAction SilentlyContinue | Where-Object { $_.InstanceId -like 'BTHLEDEVICE\{00001812-*' -and $_.InstanceId -like '*PID&%s*' }`, p)
	restartHogp := `foreach($d in $hogp){ "HOGP yeniden başlatılıyor: $($d.InstanceId)"; pnputil /restart-device "$($d.InstanceId)" }
if(-not $hogp){ "HOGP düğümü bulunamadı" }`
	findRadio := `$radio = Get-PnpDevice -Class Bluetooth -PresentOnly -ErrorAction SilentlyContinue | Where-Object { $_.InstanceId -like 'USB\VID_*' -or $_.InstanceId -like 'PCI\*' }
if(-not $radio){ $radio = Get-PnpDevice -Class Bluetooth -PresentOnly | Where-Object { $_.FriendlyName -match 'Wireless Bluetooth|Bluetooth.*(Adapter|Radio)|Realtek|MediaTek|Qualcomm|Broadcom' } }`
	cycleRadio := `foreach($r in $radio){ "Adaptör kapatılıyor: $($r.FriendlyName)"; Disable-PnpDevice -InstanceId $r.InstanceId -Confirm:$false }
Start-Sleep -Seconds 3
foreach($r in $radio){ "Adaptör açılıyor: $($r.FriendlyName)"; Enable-PnpDevice -InstanceId $r.InstanceId -Confirm:$false }`

	var body string
	switch level {
	case 1:
		body = findHogp + "\n" + restartHogp
	case 2:
		body = findRadio + "\n" + cycleRadio
	default:
		body = findRadio + "\n" + findHogp + `
"Bluetooth servisleri yeniden başlatılıyor"
Get-Service -Name 'BluetoothUserService*' -ErrorAction SilentlyContinue | Restart-Service -Force -ErrorAction SilentlyContinue
Restart-Service bthserv -Force -ErrorAction SilentlyContinue
` + cycleRadio + `
Start-Sleep -Seconds 6
` + findHogp + "\n" + restartHogp
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Continue'
& {
"===== %s onarım seviye %d ====="
%s
"bitti"
} *>> '%s'
`, time.Now().Format("2006-01-02 15:04:05"), level, body, strings.ReplaceAll(logPath, "'", "''"))
}

func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[i*2], b[i*2+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func runPowerShell(script string, elevated bool) error {
	args := "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -EncodedCommand " + encodePS(script)
	if elevated && !isElevated() {
		return shellExecute("runas", "powershell.exe", args, 0)
	}
	cmd := exec.Command("powershell.exe", strings.Fields(args)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run()
}

func (r *repairState) repair(reason string) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	st := loadState()
	level := st.LastWorkingLevel
	if level < 1 {
		level = 1
	}
	// asked again shortly after the previous attempt => previous level didn't help, escalate
	if !r.lastAt.IsZero() && time.Since(r.lastAt) < 3*time.Minute && r.lastLevel >= level {
		level = r.lastLevel + 1
	}
	if level > 3 {
		level = 3
	}
	r.running = true
	r.lastAt, r.lastLevel = time.Now(), level
	r.pendingAt, r.pendingLv = time.Now(), level
	r.mu.Unlock()

	desc := map[int]string{1: "klavyenin Bluetooth HID servisi yeniden başlatılıyor", 2: "Bluetooth adaptörü kapatılıp açılıyor", 3: "Bluetooth servisleri ve adaptör sıfırlanıyor"}[level]
	logf("Onarım başladı (%s): seviye %d", reason, level)
	notify(fmt.Sprintf("MX Keys Mini onarılıyor (seviye %d/3)", level), desc+". Birkaç saniye sonra yazmayı dene; olmazsa tekrar Mute x3.")
	detector.mu.Lock()
	detector.repairPending = true
	detector.mu.Unlock()

	err := runPowerShell(psScript(level, mgr.currentPID(), filepath.Join(appDir(), "repair.log")), true)
	if err != nil {
		logf("Onarım çalıştırılamadı: %v", err)
		notify("Onarım çalıştırılamadı", "Yönetici izni gerekli. Menüden 'Windows açılışında yönetici olarak başlat'ı açarsan bir daha sorulmaz.")
	}
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
	mgr.kick()
}

// markSuccess is called when a normal key arrives after a repair.
func (r *repairState) markSuccess() {
	r.mu.Lock()
	lv, at := r.pendingLv, r.pendingAt
	r.pendingLv = 0
	r.mu.Unlock()
	if lv == 0 || time.Since(at) > 5*time.Minute {
		return
	}
	st := loadState()
	st.LastWorkingLevel = lv
	if st.Successes == nil {
		st.Successes = map[string]int{}
	}
	st.Successes[fmt.Sprint(lv)]++
	saveState(st)
	logf("Onarım işe yaradı: seviye %d", lv)
	notify("MX Keys Mini düzeldi", fmt.Sprintf("Seviye %d işe yaradı; bir dahakine doğrudan bununla başlayacağım.", lv))
}

// ---------- autostart via Task Scheduler (elevated, works on battery, no time limit) ----------

const taskName = "MXKeysHelper"

func autostartEnabled() bool {
	cmd := exec.Command("schtasks.exe", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run() == nil
}

func taskXML(exe string) string {
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>MX Keys Mini yardımcı programı</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><Delay>PT5S</Delay></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>` + xmlEscape(exe) + `</Command></Exec></Actions>
</Task>`
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func setAutostart(on bool) {
	if !on {
		runPowerShell(fmt.Sprintf(`schtasks.exe /Delete /TN %s /F *>> '%s'`, taskName, filepath.Join(appDir(), "repair.log")), true)
		return
	}
	exe, _ := os.Executable()
	xmlPath := filepath.Join(appDir(), "task.xml")
	u := utf16.Encode([]rune(taskXML(exe)))
	b := []byte{0xFF, 0xFE}
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	if err := os.WriteFile(xmlPath, b, 0644); err != nil {
		logf("task.xml yazılamadı: %v", err)
		return
	}
	runPowerShell(fmt.Sprintf(`schtasks.exe /Create /TN %s /XML '%s' /F *>> '%s'`, taskName, xmlPath, filepath.Join(appDir(), "repair.log")), true)
}
