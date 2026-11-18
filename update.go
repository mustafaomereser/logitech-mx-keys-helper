//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Auto-update from GitHub releases. The release must carry MXHelper.exe; its
// SHA-256 comes from the asset's "digest" field (GitHub) or MXHelper.exe.sha256
// (our CI). Nothing is installed without a matching checksum.
//
// Install: download next to the running exe, rename the running exe to
// MXHelper.exe.old (Windows allows renaming a running image), move the new file
// into place, start it with --after-update <pid> and quit. The new process waits
// for the old one to exit and then deletes the .old file.

const updateRepo = "mustafaomereser/logitech-mx-keys-helper"

type updateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`
	State     string `json:"state"` // idle | checking | downloading | installing | error
	Error     string `json:"error"`
	Progress  int    `json:"progress"` // download percent
	Checked   string `json:"checked"`
	DevBuild  bool   `json:"dev_build"`

	pageURL  string
	assetURL string
	size     int64
	digest   string // hex sha256, may be empty until checksum asset is read
	sumURL   string
}

var updater struct {
	sync.Mutex
	info     updateInfo
	notified string
	busy     bool
}

func updateSnapshot() updateInfo {
	updater.Lock()
	defer updater.Unlock()
	i := updater.info
	i.Current = version
	i.DevBuild = parseVersion(version) == nil
	if i.State == "" {
		i.State = "idle"
	}
	return i
}

// parseVersion: "v1.2.3" / "1.2.3-beta" -> [1 2 3 pre]; pre = 0 for a prerelease, 1 for a final release.
func parseVersion(s string) []int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	pre := 1
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, pre = s[:i], 0
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 0, 4)
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return append(out, pre)
}

func newerVersion(latest, current string) bool {
	l, c := parseVersion(latest), parseVersion(current)
	if l == nil || c == nil {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

var updateClient = &http.Client{Timeout: 20 * time.Second}

func ghGet(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "MXHelper-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	return updateClient.Do(req)
}

func checkUpdate() updateInfo {
	updater.Lock()
	if updater.busy {
		updater.Unlock()
		return updateSnapshot()
	}
	updater.info.State, updater.info.Error = "checking", ""
	updater.Unlock()

	info, err := fetchLatest()
	updater.Lock()
	if err != nil {
		updater.info.State, updater.info.Error = "error", err.Error()
		logf("Güncelleme kontrolü başarısız: %v", err)
	} else {
		updater.info = info
		updater.info.State = "idle"
	}
	updater.info.Checked = time.Now().Format("15:04")
	updater.Unlock()
	return updateSnapshot()
}

func fetchLatest() (updateInfo, error) {
	resp, err := ghGet("https://api.github.com/repos/" + updateRepo + "/releases/latest")
	if err != nil {
		return updateInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return updateInfo{}, errors.New("henüz yayınlanmış sürüm yok")
	}
	if resp.StatusCode != 200 {
		return updateInfo{}, fmt.Errorf("GitHub yanıtı: %s", resp.Status)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Page   string `json:"html_url"`
		Body   string `json:"body"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return updateInfo{}, err
	}
	info := updateInfo{Latest: rel.Tag, pageURL: rel.Page, Notes: rel.Body}
	for _, a := range rel.Assets {
		switch strings.ToLower(a.Name) {
		case "mxhelper.exe":
			info.assetURL, info.size = a.URL, a.Size
			if d, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
				info.digest = strings.ToLower(d)
			}
		case "mxhelper.exe.sha256":
			info.sumURL = a.URL
		}
	}
	info.Available = info.assetURL != "" && newerVersion(rel.Tag, version)
	return info, nil
}

// watchUpdates checks shortly after start and then every 6 hours.
func watchUpdates() {
	time.Sleep(30 * time.Second)
	for {
		if mode := getConfig().Update.Mode; mode != "off" && parseVersion(version) != nil {
			info := checkUpdate()
			updater.Lock()
			fresh := info.Available && updater.notified != info.Latest
			if fresh {
				updater.notified = info.Latest
			}
			updater.Unlock()
			if fresh {
				if mode == "auto" {
					logf("Yeni sürüm %s, otomatik yükleniyor", info.Latest)
					go applyUpdate()
				} else {
					notifyAction("MX Helper güncellemesi", fmt.Sprintf("%s sürümü hazır. Yüklemek için tıkla.", info.Latest), func() { go applyUpdate() })
				}
			}
		}
		time.Sleep(6 * time.Hour)
	}
}

func setUpdateState(state, errMsg string, progress int) {
	updater.Lock()
	updater.info.State, updater.info.Error, updater.info.Progress = state, errMsg, progress
	updater.Unlock()
}

// applyUpdate downloads, verifies and installs the latest release, then restarts.
func applyUpdate() {
	updater.Lock()
	if updater.busy {
		updater.Unlock()
		return
	}
	updater.busy = true
	updater.Unlock()
	defer func() { updater.Lock(); updater.busy = false; updater.Unlock() }()

	info, err := fetchLatest()
	if err == nil && !info.Available {
		err = errors.New("zaten en güncel sürüm")
	}
	if err == nil {
		updater.Lock()
		updater.info = info
		updater.Unlock()
		err = installUpdate(info)
	}
	if err != nil {
		setUpdateState("error", err.Error(), 0)
		logf("Güncelleme başarısız: %v", err)
		notify("Güncelleme yapılamadı", err.Error())
	}
}

func installUpdate(info updateInfo) error {
	want := info.digest
	if want == "" && info.sumURL != "" {
		resp, err := ghGet(info.sumURL)
		if err != nil {
			return err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if f := strings.Fields(string(b)); len(f) > 0 {
			want = strings.ToLower(f[0])
		}
	}
	if len(want) != 64 {
		return errors.New("sürümde SHA-256 özeti yok; güvenlik için yüklenmedi")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	tmp := exe + ".download"
	setUpdateState("downloading", "", 0)
	if err := download(info.assetURL, tmp, info.size, want); err != nil {
		os.Remove(tmp)
		return err
	}

	setUpdateState("installing", "", 100)
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("exe değiştirilemedi (klasöre yazma izni?): %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe)
		os.Remove(tmp)
		return err
	}
	args := []string{"--after-update", strconv.Itoa(os.Getpid())}
	if gui.hwnd != 0 {
		args = append(args, "--gui")
	}
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		os.Rename(exe, tmp)
		os.Rename(old, exe)
		return err
	}
	logf("%s sürümüne güncellendi, yeniden başlatılıyor", info.Latest)
	if trayHwnd != 0 {
		pPostMessageW.Call(trayHwnd, wmQuitApp, 0, 0)
	}
	return nil
}

func download(url, dst string, size int64, wantHex string) error {
	resp, err := ghGet(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("indirme: %s", resp.Status)
	}
	if size <= 0 {
		size = resp.ContentLength
	}
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("exe klasörüne yazılamıyor: %w", err)
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var n int64
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, werr := f.Write(buf[:k]); werr != nil {
				f.Close()
				return werr
			}
			h.Write(buf[:k])
			n += int64(k)
			if n > 200<<20 {
				f.Close()
				return errors.New("dosya beklenenden büyük")
			}
			if size > 0 {
				setUpdateState("downloading", "", int(n*100/size))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantHex {
		return errors.New("indirilen dosyanın özeti tutmuyor; yüklenmedi")
	}
	return nil
}

// finishUpdate runs in the new process: wait for the old one to exit, clean up, announce.
func finishUpdate() {
	for i, a := range os.Args {
		if a != "--after-update" || i+1 >= len(os.Args) {
			continue
		}
		if pid, err := strconv.Atoi(os.Args[i+1]); err == nil {
			if h, _, _ := pOpenProcess.Call(0x00100000, 0, uintptr(pid)); h != 0 { // SYNCHRONIZE
				pWaitForSingleObject.Call(h, 15000)
				syscall.CloseHandle(syscall.Handle(h))
			}
		}
		go func() {
			exe, _ := os.Executable()
			for range 10 {
				if os.Remove(exe+".old") == nil || !fileExists(exe+".old") {
					break
				}
				time.Sleep(time.Second)
			}
			time.Sleep(3 * time.Second)
			notify("MX Helper güncellendi", "Şu anki sürüm: "+version)
		}()
		return
	}
	// a leftover from an interrupted update
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

func openReleasePage() {
	updater.Lock()
	u := updater.info.pageURL
	updater.Unlock()
	if u == "" {
		u = "https://github.com/" + updateRepo + "/releases/latest"
	}
	shellExecute("open", u, "", 1)
}
