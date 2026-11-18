//go:build windows

package main

import (
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
	// pnputil exits non-zero when the node refuses (e.g. "pending system reboot")
	restartHogp := `$hogpOK = $false
foreach($d in $hogp){ "HOGP yeniden başlatılıyor: $($d.InstanceId)"; pnputil /restart-device "$($d.InstanceId)"; if($LASTEXITCODE -eq 0){ $hogpOK = $true } }
if(-not $hogp){ "HOGP düğümü bulunamadı" }`
	findRadio := `$radio = Get-PnpDevice -Class Bluetooth -PresentOnly -ErrorAction SilentlyContinue | Where-Object { $_.InstanceId -like 'USB\VID_*' -or $_.InstanceId -like 'PCI\*' }
if(-not $radio){ $radio = Get-PnpDevice -Class Bluetooth -PresentOnly | Where-Object { $_.FriendlyName -match 'Wireless Bluetooth|Bluetooth.*(Adapter|Radio)|Realtek|MediaTek|Qualcomm|Broadcom' } }`
	// Turn Bluetooth off and on like the quick-settings switch (WinRT Radios API).
	// Disable-PnpDevice is "Not supported" on some adapters (Intel Wireless Bluetooth),
	// so the device-level cycle via pnputil is only the fallback.
	cycleRadio := `$radioDone = $false
try {
  Add-Type -AssemblyName System.Runtime.WindowsRuntime
  $asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
  function Wait-Rt($op, [Type]$t) { $task = $asTask.MakeGenericMethod($t).Invoke($null, @($op)); $task.Wait(-1) | Out-Null; $task.Result }
  [Windows.Devices.Radios.Radio,Windows.System.Devices,ContentType=WindowsRuntime] | Out-Null
  [Windows.Devices.Radios.RadioAccessStatus,Windows.System.Devices,ContentType=WindowsRuntime] | Out-Null
  [Windows.Devices.Radios.RadioState,Windows.System.Devices,ContentType=WindowsRuntime] | Out-Null
  Wait-Rt ([Windows.Devices.Radios.Radio]::RequestAccessAsync()) ([Windows.Devices.Radios.RadioAccessStatus]) | Out-Null
  $radios = Wait-Rt ([Windows.Devices.Radios.Radio]::GetRadiosAsync()) ([System.Collections.Generic.IReadOnlyList[Windows.Devices.Radios.Radio]])
  $bt = $radios | Where-Object { $_.Kind -eq 'Bluetooth' }
  foreach($b in $bt){
    "Bluetooth kapatılıyor: $($b.Name)"
    $r1 = Wait-Rt ($b.SetStateAsync([Windows.Devices.Radios.RadioState]::Off)) ([Windows.Devices.Radios.RadioAccessStatus])
    Start-Sleep -Seconds 3
    "Bluetooth açılıyor: $($b.Name)"
    $r2 = Wait-Rt ($b.SetStateAsync([Windows.Devices.Radios.RadioState]::On)) ([Windows.Devices.Radios.RadioAccessStatus])
    "sonuç: $r1 / $r2"
    if("$r1" -eq 'Allowed' -and "$r2" -eq 'Allowed'){ $radioDone = $true }
  }
  if(-not $bt){ "Bluetooth radyosu bulunamadı (WinRT)" }
} catch { "WinRT radyo hatası: $_" }
if(-not $radioDone){
  foreach($r in $radio){ "Adaptör kapatılıyor (pnputil): $($r.FriendlyName)"; pnputil /disable-device "$($r.InstanceId)" }
  Start-Sleep -Seconds 3
  foreach($r in $radio){ "Adaptör açılıyor (pnputil): $($r.FriendlyName)"; pnputil /enable-device "$($r.InstanceId)" }
}`

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

// runPowerShell runs a script file (not -EncodedCommand: encoded command lines look
// like malware to antivirus heuristics). Scripts go to %APPDATA%\MXHelper\scripts
// with a UTF-8 BOM so Windows PowerShell 5.1 reads the Turkish text correctly.
func runPowerShell(script string, elevated bool) error {
	dir := filepath.Join(appDir(), "scripts")
	os.MkdirAll(dir, 0700)
	if old, _ := filepath.Glob(filepath.Join(dir, "*.ps1")); len(old) > 0 {
		for _, f := range old {
			if fi, err := os.Stat(f); err == nil && time.Since(fi.ModTime()) > time.Hour {
				os.Remove(f)
			}
		}
	}
	f, err := os.CreateTemp(dir, "mxhelper-*.ps1")
	if err != nil {
		return err
	}
	f.WriteString("\xEF\xBB\xBF" + script) // UTF-8 BOM
	f.Close()
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", f.Name()}
	if elevated && !isElevated() {
		return shellExecute("runas", "powershell.exe", strings.Join(args[:6], " ")+` -File "`+f.Name()+`"`, 0)
	}
	cmd := exec.Command("powershell.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	err = cmd.Run()
	os.Remove(f.Name())
	return err
}

func (r *repairState) repair(reason string) {
	if present, removed := interceptionState(); present || removed {
		// slots of the Interception driver are only freed by a reboot; no repair level helps
		logf("Onarım atlandı (%s): Interception sürücüsü yüzünden sadece yeniden başlatma düzeltir", reason)
		if present {
			notifyAction(L("Onarım işe yaramaz: Interception kurulu", "Repair won't help: Interception is installed"), L("Klavye bu sürücü yüzünden takıldı; sadece yeniden başlatmak düzeltir. Kalıcı çözüm için arayüzden kaldır.", "The keyboard hung because of this driver; only a restart fixes it. Remove it from the window for a permanent fix."), openGUI)
		} else {
			notify(L("Bilgisayarı yeniden başlat", "Restart the computer"), L("Interception kaldırıldı ama hâlâ bellekte; klavye yeniden başlatınca düzelir ve bir daha takılmaz.", "Interception was removed but is still in memory; the keyboard recovers after a restart and won't hang again."))
		}
		return
	}
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

	desc := map[int]string{1: L("klavyenin Bluetooth HID servisi yeniden başlatılıyor", "restarting the keyboard's Bluetooth HID service"), 2: L("Bluetooth adaptörü kapatılıp açılıyor", "turning Bluetooth off and on"), 3: L("Bluetooth servisleri ve adaptör sıfırlanıyor", "resetting the Bluetooth services and adapter")}[level]
	logf("Onarım başladı (%s): seviye %d", reason, level)
	notify(fmt.Sprintf(L("MX Keys Mini onarılıyor (seviye %d/3)", "Repairing MX Keys Mini (level %d/3)"), level), desc+L(". Birkaç saniye sonra yazmayı dene; olmazsa tekrar Mute x3.", ". Try typing in a few seconds; if it doesn't work, Mute x3 again."))
	detector.mu.Lock()
	detector.repairPending = true
	detector.mu.Unlock()

	err := runPowerShell(psScript(level, mgr.currentPID(), filepath.Join(appDir(), "repair.log")), true)
	if err != nil {
		logf("Onarım çalıştırılamadı: %v", err)
		notify(L("Onarım çalıştırılamadı", "Repair couldn't run"), L("Yönetici izni gerekli. Menüden 'Windows açılışında yönetici olarak başlat'ı açarsan bir daha sorulmaz.", "Administrator permission is needed. Turn on 'Start with Windows' and it won't ask again."))
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
	notify(L("MX Keys Mini düzeldi", "MX Keys Mini is back"), fmt.Sprintf(L("Seviye %d işe yaradı; bir dahakine doğrudan bununla başlayacağım.", "Level %d worked; next time it starts with that one."), lv))
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
