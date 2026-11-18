//go:build windows

package main

import (
	"strings"
	"sync/atomic"
	"time"
)

// Game mode: while active, the keyboard hook swallows keys that would throw you
// out of a game (Windows keys, Alt+Tab, Alt+F4). It is active when switched on by
// hand, or while one of the configured apps is in the foreground.

var (
	gameActive atomic.Bool
	gameReason atomic.Value // string: "elle" or the exe name
)

func gameModeActive() (bool, string) {
	r, _ := gameReason.Load().(string)
	return gameActive.Load(), r
}

func watchGameMode() {
	for {
		c := getConfig()
		on, reason := false, ""
		if c.GameMode.configured() {
			if c.GameMode.Enabled {
				on, reason = true, "elle"
			} else {
				fg := foregroundProcessName()
				for _, a := range c.GameMode.Apps {
					if fg != "" && strings.EqualFold(a, fg) {
						on, reason = true, fg
						break
					}
				}
			}
		}
		if gameActive.Swap(on) != on {
			gameReason.Store(reason)
			if on {
				logf("Oyun modu açıldı (%s)", reason)
				notify(L("Oyun modu açık", "Game mode on"), gameModeSummary(c.GameMode))
			} else {
				logf("Oyun modu kapandı")
			}
			trayRefresh()
		} else if on {
			gameReason.Store(reason)
		}
		time.Sleep(700 * time.Millisecond)
	}
}

func gameModeSummary(g GameMode) string {
	var parts []string
	if g.BlockWin {
		parts = append(parts, L("Windows tuşu", "Windows key"))
	}
	if g.BlockAltTab {
		parts = append(parts, "Alt+Tab")
	}
	if g.BlockAltF4 {
		parts = append(parts, "Alt+F4")
	}
	return L("Kilitli: ", "Locked: ") + strings.Join(parts, ", ")
}

// gameBlocks reports whether a key press must be swallowed. Called from the
// keyboard hook (UI thread), so it must stay cheap: no locks besides getConfig.
func gameBlocks(vk uint16, mods int, down bool) bool {
	if !down || !gameActive.Load() {
		return false
	}
	g := getConfig().GameMode
	switch {
	case g.BlockWin && (vk == 0x5B || vk == 0x5C):
		return true
	case g.BlockAltTab && vk == 0x09 && mods&modAlt != 0:
		return true
	case g.BlockAltF4 && vk == 0x73 && mods&modAlt != 0:
		return true
	}
	return false
}

// setGameModeEnabled switches the manual game mode and saves it to config.json.
func setGameModeEnabled(on bool) {
	cur := getConfig()
	c := *cur
	c.GameMode.Enabled = on
	if on && !c.GameMode.BlockWin && !c.GameMode.BlockAltTab && !c.GameMode.BlockAltF4 {
		c.GameMode.BlockWin = true
	}
	c.Macros = append([]Macro(nil), cur.Macros...)
	if err := saveConfig(&c); err != nil {
		logf("Oyun modu kaydedilemedi: %v", err)
	}
}
