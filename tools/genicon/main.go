//go:build ignore

// genicon generates app.ico used by goversioninfo to embed a program icon
// into the Windows executable.  Run via: go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	sizes := []int{16, 32, 48, 256}
	var entries []icoEntry
	var imgData []byte

	const headerSize = 6
	const entrySize = 16

	for _, sz := range sizes {
		img := makeIcon(sz)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic(err)
		}
		entries = append(entries, icoEntry{
			width:  uint8(sz & 0xff), // 0 means 256
			height: uint8(sz & 0xff),
			size:   uint32(buf.Len()),
		})
		imgData = append(imgData, buf.Bytes()...)
	}

	// Fix up offsets
	offset := uint32(headerSize + entrySize*len(entries))
	for i := range entries {
		entries[i].offset = offset
		offset += entries[i].size
	}

	var out bytes.Buffer
	// ICONDIR header
	binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // type = icon
	binary.Write(&out, binary.LittleEndian, uint16(len(entries)))
	// ICONDIRENTRY list
	for _, e := range entries {
		out.WriteByte(e.width)
		out.WriteByte(e.height)
		out.WriteByte(0) // color count
		out.WriteByte(0) // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&out, binary.LittleEndian, uint16(32)) // bit count
		binary.Write(&out, binary.LittleEndian, e.size)
		binary.Write(&out, binary.LittleEndian, e.offset)
	}
	out.Write(imgData)

	if err := os.WriteFile("app.ico", out.Bytes(), 0o644); err != nil {
		panic(err)
	}
	println("wrote app.ico")
}

type icoEntry struct {
	width, height uint8
	size, offset  uint32
}

// makeIcon draws a lightning-bolt (power / wing) icon at size×size pixels.
func makeIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{0x10, 0x18, 0x2a, 0xff} // dark navy
	fg := color.RGBA{0xff, 0xd0, 0x00, 0xff} // golden yellow

	// Fill background with rounded-rect feel (simple solid fill).
	for y := range size {
		for x := range size {
			img.SetRGBA(x, y, bg)
		}
	}

	// Draw a centred lightning bolt as a filled polygon.
	// We work in normalised [0,1] coordinates then scale.
	type pt struct{ x, y float64 }
	bolt := []pt{
		{0.55, 0.08},
		{0.30, 0.52},
		{0.50, 0.52},
		{0.45, 0.92},
		{0.70, 0.48},
		{0.50, 0.48},
	}

	s := float64(size)
	// Convert to pixel coords.
	poly := make([][2]int, len(bolt))
	for i, p := range bolt {
		poly[i] = [2]int{int(p.x * s), int(p.y * s)}
	}

	// Scanline fill.
	for y := range size {
		var xs []int
		n := len(poly)
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			y0, y1 := poly[i][1], poly[j][1]
			x0, x1 := poly[i][0], poly[j][0]
			if (y0 <= y && y < y1) || (y1 <= y && y < y0) {
				if y1 == y0 {
					continue
				}
				xi := x0 + (y-y0)*(x1-x0)/(y1-y0)
				xs = append(xs, xi)
			}
		}
		// Sort pairs.
		for i := 0; i < len(xs)-1; i++ {
			for j := i + 1; j < len(xs); j++ {
				if xs[i] > xs[j] {
					xs[i], xs[j] = xs[j], xs[i]
				}
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			for x := xs[i]; x <= xs[i+1]; x++ {
				if x >= 0 && x < size {
					img.SetRGBA(x, y, fg)
				}
			}
		}
	}
	return img
}
