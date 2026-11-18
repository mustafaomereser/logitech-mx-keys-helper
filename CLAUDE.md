# CLAUDE.md — MX Helper

A Windows tray helper for the Logitech MX Keys Mini (Bluetooth LE). Go, standard library + Win32 syscalls only, no cgo, no third-party modules. UI text is in Turkish; code comments are in English.

## Building

- Cross-compile: `make build VERSION=v0.2.0`, or on Windows: `powershell -File build.ps1 -Version v0.2.0`
- `GOOS=windows GOARCH=amd64 go vet ./...` must pass. Every source file carries `//go:build windows`, so tools must run with GOOS=windows.
- Output goes to `dist/MXHelper.exe`, which is gitignored. CI (`.github/workflows/build.yml`) builds on every push. When a `v*` tag is pushed, it also attaches the exe to a GitHub Release.

## Status

- Pushed to the public repo https://github.com/mustafaomereser/logitech-mx-keys-helper (branch `main`, tag `v0.2.0`).
- **Nothing has been tested on real hardware.** Only the GUI was tested in a headless browser against a mock backend. The first things to verify on the device:
  1. The HID++ channel is found: Logitech VID 046D, usage page ≥ 0xFF00, 20-byte output report, product name contains "MX Keys". MX Keys Mini's BLE PID is assumed to be `b369`.
  2. **Backlight2 (0x1982) byte layout.** It was taken from Solaar: `enabled, options(mode in bits 3-4), supported, effects(LE16), level, dho, dhi, dpow (LE16, units of 5 s)`. Mode values: 1 = auto, 3 = manual. Level count comes from function 2. Set = function 1, which round-trips the bytes it read. Verify all of this against the "Tanılama raporu" output.
  3. ReprogControlsV4 (0x1B04): CIDs confirmed from a real diagnostics report: F1–F3 Easy-Switch (0xD1–D3), F4/F5 backlight (0xE2/E3), F6 dictation 0x103, F7 emoji 0x108, F8 screenshot 0x10A, F9 mic 0x11C, F10 play 0xE5, F11 mute 0xE7, F12 vol- 0xE8, ins vol+ 0xE9 (pos 0), 0x117 unknown.
  4. The repair ladder actually fixes the "only media keys work" hang.

## The user's original problem

MX Keys Mini on Bluetooth with an Intel Wireless Bluetooth adapter, Windows 11, laptop. After the keyboard sleeps and reconnects, only the volume/media keys work. Resetting the keyboard or disabling and re-enabling the HID devices in Device Manager does not fix it; only a reboot does. The laptop's own keyboard keeps working. Logi Options+ and G HUB are installed; G HUB adds a virtual keyboard.

**Root cause found (2026-11-19):** the Interception filter driver (Oblita, `keyboard.sys` / `mouse.sys` as keyboard/mouse class UpperFilters) was installed. It has a fixed number of device slots and frees them only on reboot; every Bluetooth reconnect is a new device stack, so after enough wake-ups the MX keyboard's input went nowhere. A raw-input count while stuck showed zero events from the MX device; the "working" media keys were injected by Logi Options+ from its HID++ channel. The user removed the filters (UpperFilters back to `kbdclass` / `mouclass`) and rebooted. The repair ladder cannot help against this; if the hang comes back, check the class UpperFilters first.

## Architecture (files)

| File | Contents |
|---|---|
| `win.go` | DLL/proc declarations, process helpers, elevation check, ShellExecute |
| `hid.go` | HID enumeration (CM_Get_Device_Interface_List), overlapped read/write |
| `hidpp.go` | HID++ 2.0 over BLE: report 0x11, device index 0xFF, swID 0x0B; features: battery, backlight, reprog |
| `manager.go` | Device lifecycle: ping polling, reconnect handling, key diversions, backlight rules, diagnostics |
| `rawinput.go` | Raw input (keyboard + consumer page), device matched by `vid&02046d_pid&<pid>`; `stuckDetector` (Mute ×3 gesture, "only media keys" heuristic) |
| `repair.go` | 3-level PowerShell repair ladder (HOGP node restart → BT radio disable/enable → services + radio); remembers the last level that worked in `state.json`; autostart via a Task Scheduler XML (elevated, runs on battery, no time limit) |
| `input.go` | SendInput keyboard/mouse; `injectMagic` ExtraInfo so our own hooks ignore injected events; key name ↔ VK mapping; `normalizeTrigger` |
| `hooks.go` | WH_KEYBOARD_LL / WH_MOUSE_LL on their own thread (`startHookThread`; on the UI thread Windows dropped them whenever WebView2 stalled it), installed only when needed; triggers may mix keys and one mouse part (`ctrl+mouseleft`, `q+mouse5`, `shift+wheeldown`; left/right/wheel never alone); trigger capture for the GUI's "Yakala" button; macro recorder |
| `macro.go` | Macro engine: modes `press` / `hold` / `toggle`, per-app macros, a runner with release-on-stop, the action implementations, the MX diverted-key queue |
| `config.go` | Config schema (v2: `macros` is an array), migration from v1 (map of CID → rules), save/reload, log, state |
| `api.go` | GUI HTTP server on 127.0.0.1:random; token → HttpOnly SameSite cookie; Host/Origin checks; opens an Edge `--app` window (when elevated, launches Edge de-elevated through explorer + a .vbs) |
| `interception.go` | Detects the Interception filter driver (keyboard/mouse class UpperFilters `keyboard` / `mouse`), warns at start and in the GUI, removes it from the filter lists on request (elevated); repair is skipped while it is installed |
| `gamemode.go` | Game mode: watcher goroutine sets an atomic flag (manual or foreground app in list); `gameBlocks` is called first in the keyboard hook |
| `trayicon.go` | Battery tray icon / menu bitmap drawn in code (SDF anti-aliased canvas → DIB → CreateIconIndirect), taskbar/app theme from the registry, dark popup menus via uxtheme ordinals 135/136 |
| `webview.go` | GUI window (no system caption: WM_NCCALCSIZE drops it, the page draws the title bar and forwards drag/min/max/close via `/api/window`; browser accelerators off via ICoreWebView2Settings3): own top-level window hosting WebView2 via raw COM vtables (runtime's EmbeddedBrowserWebView.dll → CreateWebViewEnvironmentWithOptionsInternal). UI thread is STA; opened through `wmOpenGUI`. Falls back to Edge `--app` (`openGUIOnce`). Process is per-monitor-v2 DPI aware |
| `update.go` | Self-update from GitHub releases (`releases/latest`): needs the `MXHelper.exe` asset and a SHA-256 (asset `digest` or `MXHelper.exe.sha256` from CI); renames the running exe to `.old`, starts the new one with `--after-update <pid>`, quits. Dev builds never self-update |
| `tray.go` | Hidden window, tray icon (version 4), right-click menu, balloon notifications, message loop |
| `tools/genicon` | Stdlib-only generator: draws the app icon, writes `assets/app.ico`, `web/icon.png` and `rsrc_windows_amd64.syso` (COFF .rsrc, linked automatically). Re-run `go run ./tools/genicon` after changing the design; the outputs are committed |
| `web/` | Embedded GUI (vanilla HTML/CSS/JS, Turkish) |

Runtime data lives in `%APPDATA%\MXHelper\`: `config.json`, `state.json`, `log.txt`, `repair.log`, `gui.url`, `tanilama.txt`.

## Conventions and gotchas

- Win32 structs are laid out by hand. Their sizes were checked on amd64: NOTIFYICONDATAW 976, WNDCLASSEXW 80, MSG 48, INPUT 40, RAWINPUTHEADER 24, HIDP_CAPS 64, PROCESSENTRY32W 568.
- Hook callbacks take a `uintptr` code; compare with `int32(code)`. Read lparam with the `*(**T)(unsafe.Pointer(&lparam))` pattern so `go vet` stays quiet.
- Never block the HID++ read loop: `onEvent` must not make HID++ requests synchronously. Use goroutines or `queueMX`.
- In PowerShell scripts, `$pid` is a read-only automatic variable; use another name.
- CSS: `[hidden] { display: none !important; }` is required, because the overlay's `display: grid` would otherwise override `hidden`.
- The user communicates in casual Turkish; keep UI strings in Turkish.

## Possible next steps

- Hardware verification using the "Tanılama raporu" output, then fix CIDs and backlight details.
- Turbo for left/right click as a trigger: blocked on purpose to avoid locking the user out; could be offered as an opt-in.
- Mouse-movement recording, macro import/export, multiple device profiles.
