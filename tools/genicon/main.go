// genicon draws the MX Helper app icon and writes:
//
//	assets/app.ico               multi-size icon (16…256, PNG entries)
//	web/icon.png                 256 px, used as the GUI window icon
//	rsrc_windows_amd64.syso      COFF resource object: icon, version info, manifest; `go build` links it
//
// Run from the repo root: go run ./tools/genicon -version v0.3.6
// (build.ps1, the Makefile and CI do this so the exe carries its version).
// Standard library only (no rsrc / go-winres), to keep the project free of third-party modules.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

type rgb struct{ r, g, b float64 }

var (
	tileCol  = rgb{0xed, 0xee, 0xf0}
	bodyCol  = rgb{0x1c, 0x1d, 0x20}
	keyCol   = rgb{0xed, 0xee, 0xf0}
	accentCl = rgb{0x3b, 0x9e, 0xff}
)

func sdRoundRect(px, py, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	qx, qy := math.Abs(px-cx)-hx, math.Abs(py-cy)-hy
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

type layer struct {
	x0, y0, x1, y1, r float64 // on a 16-unit grid
	c                 rgb
}

// draw renders the icon at n×n with 4×4 supersampling.
func draw(n int) *image.NRGBA {
	u := float64(n) / 16
	var layers []layer
	layers = append(layers,
		layer{0.6, 0.6, 15.4, 15.4, 3.4, tileCol}, // tile
		layer{2.2, 4.2, 13.8, 11.8, 1.5, bodyCol}, // keyboard body
	)
	// key rows (fewer, bigger keys at small sizes so they stay readable)
	cols := 5
	if n <= 24 {
		cols = 4
	}
	kx0, kx1 := 3.3, 12.7
	gap := 0.55
	kw := (kx1 - kx0 - gap*float64(cols-1)) / float64(cols)
	for row, y := range []float64{5.3, 7.25} {
		_ = row
		for i := 0; i < cols; i++ {
			x := kx0 + float64(i)*(kw+gap)
			layers = append(layers, layer{x, y, x + kw, y + 1.45, 0.45, keyCol})
		}
	}
	layers = append(layers, layer{5.2, 9.2, 10.8, 10.65, 0.45, accentCl}) // space bar
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const ss = 4
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := (float64(x) + (float64(sx)+.5)/ss) / u
					py := (float64(y) + (float64(sy)+.5)/ss) / u
					// topmost layer containing the sample wins
					for i := len(layers) - 1; i >= 0; i-- {
						l := layers[i]
						if sdRoundRect(px, py, l.x0, l.y0, l.x1, l.y1, l.r) <= 0 {
							r += l.c.r
							g += l.c.g
							b += l.c.b
							a++
							break
						}
					}
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(math.Round(a / ss / ss * 255))})
		}
	}
	return img
}

func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}

func le(w *bytes.Buffer, v ...any) {
	for _, x := range v {
		binary.Write(w, binary.LittleEndian, x)
	}
}

func dim(n int) uint8 {
	if n >= 256 {
		return 0
	}
	return uint8(n)
}

func writeICO(path string, imgs [][]byte) {
	var w bytes.Buffer
	le(&w, uint16(0), uint16(1), uint16(len(imgs)))
	off := 6 + 16*len(imgs)
	for i, d := range imgs {
		le(&w, dim(sizes[i]), dim(sizes[i]), uint8(0), uint8(0), uint16(1), uint16(32), uint32(len(d)), uint32(off))
		off += len(d)
	}
	for _, d := range imgs {
		w.Write(d)
	}
	must(os.WriteFile(path, w.Bytes(), 0644))
}

// resource is one entry of the .rsrc section.
type resource struct {
	typ, id uint32
	data    []byte
}

// writeSyso writes a COFF object with one .rsrc section holding the given resources
// (sorted by type, then id; language en-US). Data-entry RVAs are fixed up by the
// linker through IMAGE_REL_AMD64_ADDR32NB relocations against the section symbol.
func writeSyso(path string, res []resource) {
	const (
		lang        = 0x0409
		dirSize     = 16
		entrySize   = 8
		dataEntSize = 16
	)
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []uint32
	byType := map[uint32][]int{}
	for i, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}
	n := len(res)
	// layout: root dir, one name dir per type, one lang dir per resource, data entries, data
	typeOff := map[uint32]int{}
	p := dirSize + len(types)*entrySize
	for _, t := range types {
		typeOff[t] = p
		p += dirSize + len(byType[t])*entrySize
	}
	langOff := p
	dataEntOff := langOff + n*(dirSize+entrySize)
	p = dataEntOff + n*dataEntSize
	var blobOff []int
	for _, r := range res {
		p = (p + 7) &^ 7
		blobOff = append(blobOff, p)
		p += len(r.data)
	}
	sec := make([]byte, (p+7)&^7)
	put32 := func(at int, v uint32) { binary.LittleEndian.PutUint32(sec[at:], v) }
	put16 := func(at int, v uint16) { binary.LittleEndian.PutUint16(sec[at:], v) }
	dir := func(at, ids int) { put16(at+14, uint16(ids)) }
	const sub = 0x80000000

	dir(0, len(types))
	for i, t := range types {
		e := dirSize + i*entrySize
		put32(e, t)
		put32(e+4, sub|uint32(typeOff[t]))
		dir(typeOff[t], len(byType[t]))
		for j, ri := range byType[t] {
			e := typeOff[t] + dirSize + j*entrySize
			put32(e, res[ri].id)
			put32(e+4, sub|uint32(langOff+ri*(dirSize+entrySize)))
		}
	}
	var relocs []int
	for i, r := range res {
		ld := langOff + i*(dirSize+entrySize)
		dir(ld, 1)
		put32(ld+16, lang)
		put32(ld+20, uint32(dataEntOff+i*dataEntSize))
		de := dataEntOff + i*dataEntSize
		put32(de, uint32(blobOff[i])) // section-relative; the reloc adds the section RVA
		put32(de+4, uint32(len(r.data)))
		relocs = append(relocs, de)
		copy(sec[blobOff[i]:], r.data)
	}

	var w bytes.Buffer
	const fileHdr, secHdr = 20, 40
	rawPtr := fileHdr + secHdr
	relocPtr := rawPtr + len(sec)
	symPtr := relocPtr + len(relocs)*10
	// IMAGE_FILE_HEADER
	le(&w, uint16(0x8664), uint16(1), uint32(0), uint32(symPtr), uint32(1), uint16(0), uint16(0))
	// section header
	w.WriteString(".rsrc\x00\x00\x00")
	le(&w, uint32(0), uint32(0), uint32(len(sec)), uint32(rawPtr), uint32(relocPtr), uint32(0), uint16(len(relocs)), uint16(0), uint32(0x40000040))
	w.Write(sec)
	for _, r := range relocs {
		le(&w, uint32(r), uint32(0), uint16(3)) // IMAGE_REL_AMD64_ADDR32NB against symbol 0
	}
	// symbol table: the section symbol
	w.WriteString(".rsrc\x00\x00\x00")
	le(&w, uint32(0), int16(1), uint16(0), uint8(3), uint8(0))
	le(&w, uint32(4)) // empty string table
	must(os.WriteFile(path, w.Bytes(), 0644))
}

// ---------- RT_VERSION ----------

func utf16z(s string) []byte {
	var b bytes.Buffer
	for _, c := range utf16.Encode([]rune(s)) {
		le(&b, c)
	}
	le(&b, uint16(0))
	return b.Bytes()
}

// vnode builds one VS_VERSIONINFO-style node: wLength, wValueLength, wType, key, value, children.
func vnode(key string, text bool, value []byte, valueLen int, children ...[]byte) []byte {
	var b bytes.Buffer
	typ := uint16(0)
	if text {
		typ = 1
	}
	le(&b, uint16(0), uint16(valueLen), typ)
	b.Write(utf16z(key))
	pad := func() {
		for b.Len()%4 != 0 {
			b.WriteByte(0)
		}
	}
	pad()
	b.Write(value)
	for _, c := range children {
		pad()
		b.Write(c)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

// versionInfo: file/product version and the strings Explorer shows under Details.
func versionInfo(ver string) []byte {
	nums := [4]uint16{}
	for i, part := range strings.SplitN(strings.TrimPrefix(ver, "v"), ".", 4) {
		v, _ := strconv.Atoi(strings.TrimFunc(part, func(r rune) bool { return r < '0' || r > '9' }))
		nums[i] = uint16(v)
	}
	ms, ls := uint32(nums[0])<<16|uint32(nums[1]), uint32(nums[2])<<16|uint32(nums[3])
	var fixed bytes.Buffer
	le(&fixed, uint32(0xFEEF04BD), uint32(0x00010000), ms, ls, ms, ls, uint32(0x3F), uint32(0), uint32(0x00040004), uint32(1), uint32(0), uint32(0), uint32(0))
	dotted := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	str := func(k, v string) []byte { return vnode(k, true, utf16z(v), len(utf16.Encode([]rune(v)))+1) }
	table := vnode("040904B0", true, nil, 0,
		str("CompanyName", "mustafaomereser"),
		str("FileDescription", "MX Helper - Logitech MX Keys Mini helper"),
		str("FileVersion", dotted),
		str("InternalName", "MXHelper"),
		str("LegalCopyright", "Copyright (c) 2026 mustafaomereser"),
		str("OriginalFilename", "MXHelper.exe"),
		str("ProductName", "MX Helper"),
		str("ProductVersion", dotted),
		str("Comments", "https://github.com/mustafaomereser/logitech-mx-keys-helper"),
	)
	var tr bytes.Buffer
	le(&tr, uint16(0x0409), uint16(0x04B0))
	return vnode("VS_VERSION_INFO", false, fixed.Bytes(), fixed.Len(),
		vnode("StringFileInfo", true, nil, 0, table),
		vnode("VarFileInfo", true, nil, 0, vnode("Translation", false, tr.Bytes(), tr.Len())),
	)
}

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="mustafaomereser.MXHelper" version="1.0.0.0" processorArchitecture="amd64"/>
  <description>MX Helper</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application><supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/></application>
  </compatibility>
</assembly>
`

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	ver := flag.String("version", "0.0.0", "version written into the exe's version resource")
	flag.Parse()
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, pngBytes(draw(s)))
	}
	writeICO("assets/app.ico", imgs)
	must(os.WriteFile("web/icon.png", imgs[len(imgs)-1], 0644))
	n := len(imgs)
	var grp bytes.Buffer
	le(&grp, uint16(0), uint16(1), uint16(n))
	res := []resource{}
	for i, d := range imgs {
		le(&grp, dim(sizes[i]), dim(sizes[i]), uint8(0), uint8(0), uint16(1), uint16(32), uint32(len(d)), uint16(i+1))
		res = append(res, resource{3, uint32(i + 1), d}) // RT_ICON
	}
	res = append(res,
		resource{14, 1, grp.Bytes()},       // RT_GROUP_ICON
		resource{16, 1, versionInfo(*ver)}, // RT_VERSION
		resource{24, 1, []byte(manifest)},  // RT_MANIFEST
	)
	writeSyso("rsrc_windows_amd64.syso", res)
	log.Printf("assets/app.ico, web/icon.png, rsrc_windows_amd64.syso yazıldı (%d boyut)", len(sizes))
}
