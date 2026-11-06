//go:build windows

package ui

// bmp_decode.go adalah decoder BMP minimal karena modul ini mem-vendor hanya
// golang.org/x/sys (image/bmp ada di golang.org/x/image). Cakupan: BMP
// tak terkompresi 1/4/8/16/24/32 bpp, header 12/40/56/64/108/124, orientasi
// atas-bawah maupun bawah-atas, plus BI_BITFIELDS. RLE 4/8 ditolak dengan
// galat, bukan menampilkan thumbnail rusak.

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/png"
	"io"
	"path/filepath"
	"strings"
)

type bmpImage struct {
	pix   []byte // RGBA rapat
	b     image.Rectangle
	field [3]uint32
	shift [3]uint32
}

func (im *bmpImage) ColorModel() color.Model { return color.RGBAModel }
func (im *bmpImage) Bounds() image.Rectangle { return im.b }

func (im *bmpImage) At(x, y int) color.Color {
	if x < im.b.Min.X || x >= im.b.Max.X || y < im.b.Min.Y || y >= im.b.Max.Y {
		return color.RGBA{}
	}
	o := ((y-im.b.Min.Y)*im.b.Dx() + (x - im.b.Min.X)) * 4
	return color.RGBA{im.pix[o], im.pix[o+1], im.pix[o+2], im.pix[o+3]}
}

func decodeBMPFrom(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(io.LimitReader(r, 512<<20))
	if err != nil {
		return nil, err
	}
	return decodeBMPBytes(data)
}

// maskShift mengembalikan offset bit terendah dan lebar sebuah mask kanal.
func maskShift(mask uint32) (shift, bits uint32, ok bool) {
	if mask == 0 {
		return 0, 0, false
	}
	for shift = 0; mask&(1<<shift) == 0; shift++ {
	}
	for bits = 0; mask&(1<<(shift+bits)) != 0; bits++ {
	}
	return shift, bits, true
}

func entrySizeOf(biSize int) int {
	if biSize == 12 {
		return 3
	}
	return 4
}

// decodeImageFile men-decode berkas gambar. BMP memakai decoder di file ini
// karena golang.org/x/image tidak ikut di-vendor; format lain lewat stdlib.
func decodeImageFile(r io.Reader, path string) (image.Image, error) {
	if strings.EqualFold(filepath.Ext(path), ".bmp") {
		return decodeBMPFrom(r)
	}
	im, _, err := image.Decode(r)
	return im, err
}

func decodeBMPBytes(data []byte) (image.Image, error) {
	if len(data) < 14+4 || data[0] != 'B' || data[1] != 'M' {
		return nil, fmt.Errorf("bukan berkas BMP")
	}
	pixOff := int(binary.LittleEndian.Uint32(data[10:14]))
	biSize := int(binary.LittleEndian.Uint32(data[14:18]))
	if biSize < 12 || 14+biSize > len(data) {
		return nil, fmt.Errorf("header BMP pendek")
	}
	h := data[14 : 14+biSize]
	var width, height int32
	var bitCount, comp uint16
	if biSize == 12 {
		width = int32(binary.LittleEndian.Uint32(h[0:4]))
		height = int32(binary.LittleEndian.Uint32(h[4:8]))
		bitCount = binary.LittleEndian.Uint16(h[10:12])
	} else {
		width = int32(binary.LittleEndian.Uint32(h[4:8]))
		height = int32(binary.LittleEndian.Uint32(h[8:12]))
		bitCount = binary.LittleEndian.Uint16(h[12:14])
		comp = binary.LittleEndian.Uint16(h[14:16])
	}
	if width <= 0 || height == 0 {
		return nil, fmt.Errorf("ukuran BMP tidak sah")
	}
	if comp == 1 || comp == 2 {
		return nil, fmt.Errorf("BMP RLE tidak didukung")
	}
	if comp == 3 && biSize < 40 {
		return nil, fmt.Errorf("BI_BITFIELDS butuh header 40 byte")
	}
	topDown := height < 0
	if topDown {
		height = -height
	}
	switch bitCount {
	case 1, 4, 8, 16, 24, 32:
	default:
		return nil, fmt.Errorf("kedalaman warna %d bit tidak didukung", bitCount)
	}

	im := &bmpImage{b: image.Rect(0, 0, int(width), int(height))}
	im.pix = make([]byte, int(width)*int(height)*4)

	// palette: tepat setelah header info, kecuali BI_BITFIELDS 16/32 bpp.
	var palette []byte
	palOff := 14 + biSize
	if bitCount <= 8 {
		entries := 1 << bitCount
		if biSize >= 40 {
			if n := int(binary.LittleEndian.Uint32(h[20:24])); n > 0 && n < entries {
				entries = n
			}
		}
		size := entrySizeOf(biSize)
		need := palOff + entries*size
		if need > len(data) {
			return nil, fmt.Errorf("palette BMP rusak")
		}
		palette = data[palOff:need]
	}

	// mask kanal warna (BGR default, kecuali BI_BITFIELDS menyatakan lain).
	switch {
	case comp == 3 && (bitCount == 16 || bitCount == 32):
		if palOff+12 > len(data) {
			return nil, fmt.Errorf("mask BMP rusak")
		}
		im.field[0] = binary.LittleEndian.Uint32(data[palOff : palOff+4])
		im.field[1] = binary.LittleEndian.Uint32(data[palOff+4 : palOff+8])
		im.field[2] = binary.LittleEndian.Uint32(data[palOff+8 : palOff+12])
	case bitCount == 16:
		im.field[0], im.field[1], im.field[2] = 0x7C00, 0x03E0, 0x001F
	case bitCount == 32:
		im.field[0], im.field[1], im.field[2] = 0x00FF0000, 0x0000FF00, 0x000000FF
	}
	for k := range im.field {
		shift, bits, ok := maskShift(im.field[k])
		if !ok {
			continue
		}
		if bits > 8 {
			shift += bits - 8 // kanal 10 bit diturunkan ke 8 bit
		}
		im.shift[k] = shift
	}

	rowBytes := (int(width)*int(bitCount) + 7) / 8
	rowSize := (rowBytes + 3) & ^3
	if pixOff <= 0 || pixOff >= len(data) {
		return nil, fmt.Errorf("offset piksel BMP rusak")
	}
	if pixOff+rowSize*int(height) > len(data) {
		if pixOff+rowSize*(int(height)-1)+rowBytes > len(data) {
			return nil, fmt.Errorf("data piksel BMP pendek")
		}
	}
	palAt := func(i int) (r, g, b byte) {
		base := i * entrySizeOf(biSize)
		return palette[base+2], palette[base+1], palette[base]
	}
	for y := 0; y < int(height); y++ {
		dstRow := y
		if !topDown {
			dstRow = int(height) - 1 - y
		}
		src := data[pixOff+y*rowSize:]
		for x := 0; x < int(width); x++ {
			o := (dstRow*int(width) + x) * 4
			switch bitCount {
			case 1:
				im.pix[o], im.pix[o+1], im.pix[o+2] = palAt(int((src[x/8] >> (7 - uint(x%8))) & 1))
				im.pix[o+3] = 255
			case 4:
				im.pix[o], im.pix[o+1], im.pix[o+2] = palAt(int((src[x/8] >> (4 - uint(x%8)*4)) & 0x0f))
				im.pix[o+3] = 255
			case 8:
				im.pix[o], im.pix[o+1], im.pix[o+2] = palAt(int(src[x]))
				im.pix[o+3] = 255
			case 16:
				v := uint32(binary.LittleEndian.Uint16(src[x*2:]))
				im.pix[o] = byte((v >> im.shift[0]) & 0xff)
				im.pix[o+1] = byte((v >> im.shift[1]) & 0xff)
				im.pix[o+2] = byte((v >> im.shift[2]) & 0xff)
				im.pix[o+3] = 255
			case 24:
				p := src[x*3:]
				im.pix[o], im.pix[o+1], im.pix[o+2] = p[2], p[1], p[0]
				im.pix[o+3] = 255
			case 32:
				v := binary.LittleEndian.Uint32(src[x*4:])
				im.pix[o] = byte((v >> im.shift[0]) & 0xff)
				im.pix[o+1] = byte((v >> im.shift[1]) & 0xff)
				im.pix[o+2] = byte((v >> im.shift[2]) & 0xff)
				im.pix[o+3] = 255
			}
		}
	}
	return im, nil
}
