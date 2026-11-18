// genicon draws the MX Helper app icon and writes:
//
//	assets/app.ico               multi-size icon (16…256, PNG entries)
//	web/icon.png                 256 px, used as the GUI window icon
//	rsrc_windows_amd64.syso      COFF resource object; `go build` links it so the exe has an icon
//
// Run from the repo root: go run ./tools/genicon
// Standard library only (no rsrc / go-winres), to keep the project free of third-party modules.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
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

// writeSyso writes a COFF object with one .rsrc section holding RT_ICON entries
// (IDs 1..n) and one RT_GROUP_ICON (ID 1). Data-entry RVAs are fixed up by the
// linker through IMAGE_REL_AMD64_ADDR32NB relocations against the section symbol.
func writeSyso(path string, imgs [][]byte) {
	const (
		rtIcon      = 3
		rtGroupIcon = 14
		lang        = 0x0409
		dirSize     = 16
		entrySize   = 8
		dataEntSize = 16
	)
	n := len(imgs)
	var grp bytes.Buffer
	le(&grp, uint16(0), uint16(1), uint16(n))
	for i, d := range imgs {
		le(&grp, dim(sizes[i]), dim(sizes[i]), uint8(0), uint8(0), uint16(1), uint16(32), uint32(len(d)), uint16(i+1))
	}
	blobs := append(append([][]byte{}, imgs...), grp.Bytes()) // n icons + group

	// layout: root dir, type dirs, name dirs (lang level), data entries, data
	rootOff := 0
	iconTypeOff := rootOff + dirSize + 2*entrySize
	groupTypeOff := iconTypeOff + dirSize + n*entrySize
	langOff := groupTypeOff + dirSize + entrySize // n+1 lang dirs follow
	dataEntOff := langOff + (n+1)*(dirSize+entrySize)
	dataOff := dataEntOff + (n+1)*dataEntSize
	var blobOff []int
	p := dataOff
	for _, b := range blobs {
		p = (p + 7) &^ 7
		blobOff = append(blobOff, p)
		p += len(b)
	}
	sec := make([]byte, (p+7)&^7)
	put32 := func(at int, v uint32) { binary.LittleEndian.PutUint32(sec[at:], v) }
	put16 := func(at int, v uint16) { binary.LittleEndian.PutUint16(sec[at:], v) }
	dir := func(at, ids int) { put16(at+14, uint16(ids)) }
	const sub = 0x80000000

	dir(rootOff, 2)
	put32(rootOff+16, rtIcon)
	put32(rootOff+20, sub|uint32(iconTypeOff))
	put32(rootOff+24, rtGroupIcon)
	put32(rootOff+28, sub|uint32(groupTypeOff))

	dir(iconTypeOff, n)
	for i := 0; i < n; i++ {
		e := iconTypeOff + dirSize + i*entrySize
		put32(e, uint32(i+1))
		put32(e+4, sub|uint32(langOff+i*(dirSize+entrySize)))
	}
	dir(groupTypeOff, 1)
	put32(groupTypeOff+16, 1)
	put32(groupTypeOff+20, sub|uint32(langOff+n*(dirSize+entrySize)))

	var relocs []int
	for i := 0; i <= n; i++ {
		ld := langOff + i*(dirSize+entrySize)
		dir(ld, 1)
		put32(ld+16, lang)
		put32(ld+20, uint32(dataEntOff+i*dataEntSize))
		de := dataEntOff + i*dataEntSize
		put32(de, uint32(blobOff[i])) // section-relative; the reloc adds the section RVA
		put32(de+4, uint32(len(blobs[i])))
		relocs = append(relocs, de)
		copy(sec[blobOff[i]:], blobs[i])
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

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, pngBytes(draw(s)))
	}
	writeICO("assets/app.ico", imgs)
	must(os.WriteFile("web/icon.png", imgs[len(imgs)-1], 0644))
	writeSyso("rsrc_windows_amd64.syso", imgs)
	log.Printf("assets/app.ico, web/icon.png, rsrc_windows_amd64.syso yazıldı (%d boyut)", len(sizes))
}
