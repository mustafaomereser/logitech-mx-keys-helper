//go:build windows

package main

// UI language for the tray menu, notifications and API messages. The web GUI does its
// own translation (web/i18n.js) from the effective language reported in /api/state.
// Logs and the diagnostics report stay Turkish.

var pGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")

// uiLang returns "tr" or "en": the config value, or Windows' display language on "".
func uiLang() string {
	switch getConfig().Language {
	case "tr":
		return "tr"
	case "en":
		return "en"
	}
	if r, _, _ := pGetUserDefaultUILanguage.Call(); r&0x3FF == 0x1F { // LANG_TURKISH
		return "tr"
	}
	return "en"
}

// L picks the Turkish or English text.
func L(tr, en string) string {
	if uiLang() == "en" {
		return en
	}
	return tr
}
