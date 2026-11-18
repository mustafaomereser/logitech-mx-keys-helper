//go:build windows

package main

import (
	"math"
	"syscall"
	"unsafe"
)

// Tray icon and menu bitmaps are drawn in code: a battery whose fill and colour
// follow the battery level (green > 30 %, yellow > 15 %, red otherwise, grey when
// the keyboard is not connected).

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	pCreateDIBSection  = gdi32.NewProc("CreateDIBSection")
	pCreateBitmap      = gdi32.NewProc("CreateBitmap")
	pDeleteObject      = gdi32.NewProc("DeleteObject")
	pCreateIconIndir   = user32.NewProc("CreateIconIndirect")
	pDestroyIcon       = user32.NewProc("DestroyIcon")
	pSetMenuItemInfoW  = user32.NewProc("SetMenuItemInfoW")
	pSetMenuDefaultItm = user32.NewProc("SetMenuDefaultItem")
	pRegGetValueW      = advapi32.NewProc("RegGetValueW")
	pGetProcAddress    = kernel32.NewProc("GetProcAddress")
	pLoadLibraryW      = kernel32.NewProc("LoadLibraryW")
)

type rgba struct{ r, g, b, a float64 }

var (
	colGreen  = rgba{0x3d, 0xd6, 0x8c, 1}
	colYellow = rgba{0xff, 0xca, 0x16, 1}
	colRed    = rgba{0xff, 0x63, 0x69, 1}
	colGrey   = rgba{0x8a, 0x8d, 0x93, 1}
	colLight  = rgba{0xf2, 0xf3, 0xf5, 1}
	colDark   = rgba{0x1f, 0x20, 0x23, 1}
)

func levelColor(pct int) rgba {
	switch {
	case pct <= 15:
		return colRed
	case pct <= 30:
		return colYellow
	}
	return colGreen
}

// ---------- tiny anti-aliased canvas (straight alpha) ----------

type canvas struct {
	n  int
	px []rgba
}

func newCanvas(n int) *canvas { return &canvas{n: n, px: make([]rgba, n*n)} }

// sdRoundRect: signed distance from (px,py) to a rounded rectangle.
func sdRoundRect(px, py, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	qx, qy := math.Abs(px-cx)-hx, math.Abs(py-cy)-hy
	out := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	return out + math.Min(math.Max(qx, qy), 0) - r
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func (c *canvas) blend(i int, col rgba, cov float64) {
	a := col.a * cov
	if a <= 0 {
		return
	}
	d := &c.px[i]
	oa := a + d.a*(1-a)
	if oa <= 0 {
		return
	}
	d.r = (col.r*a + d.r*d.a*(1-a)) / oa
	d.g = (col.g*a + d.g*d.a*(1-a)) / oa
	d.b = (col.b*a + d.b*d.a*(1-a)) / oa
	d.a = oa
}

// shape draws a filled rounded rect, or its outline when stroke > 0.
func (c *canvas) shape(x0, y0, x1, y1, r, stroke float64, col rgba) {
	for y := 0; y < c.n; y++ {
		for x := 0; x < c.n; x++ {
			d := sdRoundRect(float64(x)+.5, float64(y)+.5, x0, y0, x1, y1, r)
			cov := clamp01(.5 - d)
			if stroke > 0 {
				cov -= clamp01(.5 - (d + stroke))
			}
			c.blend(y*c.n+x, col, cov)
		}
	}
}

// drawBattery draws the battery glyph on an n×n canvas laid out on a 16-unit grid.
func drawBattery(n int, connected, known bool, pct int, outline rgba) *canvas {
	c := newCanvas(n)
	u := float64(n) / 16
	line := outline
	if !connected {
		line = colGrey
	} else if known && pct <= 30 {
		line = levelColor(pct) // low battery: the whole glyph takes the warning colour
	}
	sw := math.Max(1, 1.3*u)
	c.shape(.5*u, 3.5*u, 13.5*u, 12.5*u, 2.2*u, sw, line)
	c.shape(14*u, 6.2*u, 15.6*u, 9.8*u, .7*u, 0, line)
	if !connected {
		return c
	}
	p := float64(pct) / 100
	fill := levelColor(pct)
	if !known {
		p, fill = 1, rgba{line.r, line.g, line.b, .35}
	}
	inset := sw + 1*u
	x0, x1 := .5*u+inset, 13.5*u-inset
	w := (x1 - x0) * clamp01(p)
	if w < 1.2*u && known {
		w = 1.2 * u // keep a visible sliver of red at 1-5 %
	}
	c.shape(x0, 3.5*u+inset, x0+w, 12.5*u-inset, 1*u, 0, fill)
	return c
}

// dib creates a 32-bit top-down DIB section from the canvas.
func (c *canvas) dib(premultiply bool) uintptr {
	type bmih struct {
		Size                         uint32
		Width, Height                int32
		Planes, BitCount             uint16
		Compression, SizeImage       uint32
		XPels, YPels, ClrUsed, ClrIm uint32
	}
	h := bmih{Size: 40, Width: int32(c.n), Height: -int32(c.n), Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	hbm, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&h)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return 0
	}
	out := unsafe.Slice((*byte)(bits), c.n*c.n*4)
	for i, p := range c.px {
		m := 1.0
		if premultiply {
			m = p.a
		}
		out[i*4+0] = byte(math.Round(p.b * m))
		out[i*4+1] = byte(math.Round(p.g * m))
		out[i*4+2] = byte(math.Round(p.r * m))
		out[i*4+3] = byte(math.Round(p.a * 255))
	}
	return hbm
}

func (c *canvas) icon() uintptr {
	color := c.dib(false)
	if color == 0 {
		return 0
	}
	mask, _, _ := pCreateBitmap.Call(uintptr(c.n), uintptr(c.n), 1, 1, 0)
	ii := struct {
		FIcon      int32
		XHot, YHot uint32
		Mask       uintptr
		Color      uintptr
	}{FIcon: 1, Mask: mask, Color: color}
	h, _, _ := pCreateIconIndir.Call(uintptr(unsafe.Pointer(&ii)))
	pDeleteObject.Call(color)
	pDeleteObject.Call(mask)
	return h
}

// ---------- theme ----------

func regDWORD(key, value string) (uint32, bool) {
	var data, size uint32 = 0, 4
	r, _, _ := pRegGetValueW.Call(0x80000001, uintptr(unsafe.Pointer(u16(key))), uintptr(unsafe.Pointer(u16(value))),
		0x10, 0, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&size))) // HKCU, RRF_RT_REG_DWORD
	return data, r == 0
}

const personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

func taskbarIsLight() bool { v, ok := regDWORD(personalizeKey, "SystemUsesLightTheme"); return ok && v == 1 }
func appsAreLight() bool   { v, ok := regDWORD(personalizeKey, "AppsUseLightTheme"); return !ok || v == 1 }

// enableDarkMenus lets popup menus follow the Windows app theme (dark/light).
// Uses the uxtheme ordinals Explorer and Notepad++ use; silently does nothing on old builds.
func enableDarkMenus() {
	ux, _, _ := pLoadLibraryW.Call(uintptr(unsafe.Pointer(u16("uxtheme.dll"))))
	if ux == 0 {
		return
	}
	setMode, _, _ := pGetProcAddress.Call(ux, 135) // SetPreferredAppMode
	flush, _, _ := pGetProcAddress.Call(ux, 136)   // FlushMenuThemes
	if setMode != 0 {
		syscall.SyscallN(setMode, 1) // AllowDark: follow the system setting
	}
	if flush != 0 {
		syscall.SyscallN(flush)
	}
}

// ---------- tray icon cache ----------

var trayIconState struct {
	key  string
	icon uintptr
}

// batteryTrayIcon returns an icon for the current state, rebuilding it only when it changes.
func batteryTrayIcon(connected bool, b batteryState) uintptr {
	light := taskbarIsLight()
	key := ""
	if connected {
		key = "c"
		if b.Known {
			key += string(rune('0'+b.Percent/10)) + string(rune('0'+b.Percent%10))
		}
	}
	if light {
		key += "L"
	}
	if key == trayIconState.key && trayIconState.icon != 0 {
		return trayIconState.icon
	}
	outline := colLight
	if light {
		outline = colDark
	}
	pct := b.Percent
	if pct > 100 {
		pct = 100
	}
	h := drawBattery(32, connected, b.Known, pct, outline).icon()
	if h == 0 {
		return 0
	}
	if trayIconState.icon != 0 {
		old := trayIconState.icon
		defer pDestroyIcon.Call(old) // after the shell has the new one
	}
	trayIconState.key, trayIconState.icon = key, h
	return h
}

// menuBatteryBitmap is a 16×16 premultiplied bitmap for the menu's status row.
// The caller deletes it after the menu closes.
func menuBatteryBitmap(connected bool, b batteryState) uintptr {
	outline := colLight
	if appsAreLight() {
		outline = colDark
	}
	return drawBattery(16, connected, b.Known, b.Percent, outline).dib(true)
}

type menuItemInfo struct {
	Size          uint32
	Mask          uint32
	Type          uint32
	State         uint32
	ID            uint32
	SubMenu       uintptr
	BmpChecked    uintptr
	BmpUnchecked  uintptr
	ItemData      uintptr
	TypeData      *uint16
	Cch           uint32
	BmpItem       uintptr
}

func setMenuItemBitmap(menu uintptr, id int, bmp uintptr) {
	mii := menuItemInfo{Mask: 0x80, BmpItem: bmp} // MIIM_BITMAP
	mii.Size = uint32(unsafe.Sizeof(mii))
	pSetMenuItemInfoW.Call(menu, uintptr(id), 0, uintptr(unsafe.Pointer(&mii)))
}
