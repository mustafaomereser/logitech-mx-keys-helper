# CLAUDE.md — MX Helper

A Windows tray helper for the Logitech MX Keys Mini (Bluetooth LE). Go, standard library + Win32 syscalls only, no cgo, no third-party modules. UI text is in Turkish; code comments are in English.

## Building

- Cross-compile: `make build VERSION=v0.2.0`, or on Windows: `powershell -File build.ps1 -Version v0.2.0`
- `GOOS=windows GOARCH=amd64 go vet ./...` must pass. Every source file carries `//go:build windows`, so tools must run with GOOS=windows.
- Output goes to `dist/MXHelper.exe`, which is gitignored. CI (`.github/workflows/build.yml`) builds on every push. When a `v*` tag is pushed, it also attaches the exe to a GitHub Release.

## Status

- The repo has one commit (`8fe04cf MX Helper v0.2.0`) and the tag `v0.2.0`. **It has not been pushed to GitHub yet.** The plan is a public repo named `mxhelper`:
  `gh repo create mxhelper --public --source . --push && git push origin v0.2.0`
- **Nothing has been tested on real hardware.** Only the GUI was tested in a headless browser against a mock backend. The first things to verify on the device:
  1. The HID++ channel is found: Logitech VID 046D, usage page ≥ 0xFF00, 20-byte output report, product name contains "MX Keys". MX Keys Mini's BLE PID is assumed to be `b369`.
  2. **Backlight2 (0x1982) byte layout.** It was taken from Solaar: `enabled, options(mode in bits 3-4), supported, effects(LE16), level, dho, dhi, dpow (LE16, units of 5 s)`. Mode values: 1 = auto, 3 = manual. Level count comes from function 2. Set = function 1, which round-trips the bytes it read. Verify all of this against the "Tanılama raporu" output.
  3. ReprogControlsV4 (0x1B04): the CIDs of the special keys. The names in `cidNames` are guesses.
  4. The repair ladder actually fixes the "only media keys work" hang.

## The user's original problem

MX Keys Mini on Bluetooth with an Intel Wireless Bluetooth adapter, Windows 11, laptop. After the keyboard sleeps and reconnects, only the volume/media keys work. Resetting the keyboard or disabling and re-enabling the HID devices in Device Manager does not fix it; only a reboot does. The laptop's own keyboard keeps working. Logi Options+ and G HUB are installed; G HUB adds a virtual keyboard.

Working hypothesis: on reconnect, Windows fails to re-subscribe to the GATT notifications for the HOGP keyboard input report, while the consumer-control report still arrives.

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
| `hooks.go` | WH_KEYBOARD_LL / WH_MOUSE_LL, installed only when needed; trigger capture for the GUI's "Yakala" button; macro recorder |
| `macro.go` | Macro engine: modes `press` / `hold` / `toggle`, per-app macros, a runner with release-on-stop, the action implementations, the MX diverted-key queue |
| `config.go` | Config schema (v2: `macros` is an array), migration from v1 (map of CID → rules), save/reload, log, state |
| `api.go` | GUI HTTP server on 127.0.0.1:random; token → HttpOnly SameSite cookie; Host/Origin checks; opens an Edge `--app` window (when elevated, launches Edge de-elevated through explorer + a .vbs) |
| `tray.go` | Hidden window, tray icon (version 4), right-click menu, balloon notifications, message loop |
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
- An exe icon resource (.syso). The tray icons are already embedded.
- Turbo for left/right click as a trigger: blocked on purpose to avoid locking the user out; could be offered as an opt-in.
- Mouse-movement recording, macro import/export, multiple device profiles.
