// imggen.go — pembuat gambar murni (tanpa dependensi GUI): identicon,
// avatar bawaan, titik status, dan ikon grup. Semua deterministik agar
// tampil sama di setiap PC. Berkas ini dapat diuji di semua OS.
package ui

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"belgindo/internal/store"
)

// rgb warna sederhana tanpa dependensi GUI.
type rgb struct{ r, g, b uint8 }

func (c rgb) RGBA() (uint8, uint8, uint8) { return c.r, c.g, c.b }

// Jumlah avatar bawaan yang dapat dipilih di Pengaturan > Profil Saya.
const builtinAvatarCount = 12

// Ukuran gambar yang disimpan saat pengguna mengunggah foto.
const ownPhotoSize = 256

// hsvToRGB mengonversi HSV (0..1) menjadi RGB 0..255.
func hsvToRGB(h, s, v float64) (uint8, uint8, uint8) {
	i := int(h*6) % 6
	f := h*6 - float64(int(h*6))
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)
	var r, g, b float64
	switch ((i % 6) + 6) % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return uint8(r * 255), uint8(g * 255), uint8(b * 255)
}

// identicon menggambar avatar pola 5x5 simetris dari seed (deterministik).
func identicon(seed string, size int) image.Image {
	sum := md5.Sum([]byte(seed))
	r, g, b := hsvToRGB(float64(sum[0])/255.0, 0.55+float64(sum[1]&7)/7.0*0.25, 0.72)
	fg := color.RGBA{r, g, b, 255}
	bg := color.RGBA{0xF7, 0xF9, 0xFC, 255}

	im := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			im.Set(x, y, bg)
		}
	}
	const cells = 5
	cs := size / cells
	off := (size - cs*cells) / 2
	filled := make([][]bool, cells)
	for i := range filled {
		filled[i] = make([]bool, cells)
	}
	for x := 0; x < 3; x++ {
		for y := 0; y < cells; y++ {
			on := sum[3+(x*cells+y)%13]&(1<<(uint(y)%8)) != 0
			filled[x][y] = on
			filled[cells-1-x][y] = on
		}
	}
	for x := 0; x < cells; x++ {
		for y := 0; y < cells; y++ {
			if !filled[x][y] {
				continue
			}
			for dy := 0; dy < cs; dy++ {
				for dx := 0; dx < cs; dx++ {
					im.Set(off+x*cs+dx, off+y*cs+dy, fg)
				}
			}
		}
	}
	return im
}

// builtinAvatar avatar bawaan ke-i (sama di semua PC).
func builtinAvatar(id int, size int) image.Image {
	if id < 0 {
		id = 0
	}
	return identicon(fmt.Sprintf("belgindo-avatar-bawaan-%d", id), size)
}

// dotImage menggambar titik status bulat mengkilap.
func dotImage(col rgb, size int) image.Image {
	return dotImageOn(col, size, rgb{})
}

// dotImageOn menggambar titik status dengan latar solid.
func dotImageOn(col rgb, size int, bg rgb) image.Image {
	base := color.RGBA{col.r, col.g, col.b, 255}
	dark := color.RGBA{uint8(float64(col.r) * 0.72), uint8(float64(col.g) * 0.72), uint8(float64(col.b) * 0.72), 255}
	solid := bg != (rgb{})

	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	if solid {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				im.SetNRGBA(x, y, color.NRGBA{bg.r, bg.g, bg.b, 255})
			}
		}
	}
	cx, cy := float64(size-1)/2, float64(size-1)/2
	rad := float64(size)/2 - 0.5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := dist2D(float64(x), float64(y), cx, cy)
			if d > rad {
				continue
			}
			a := 255
			if d > rad-0.75 {
				a = int(255 * (rad - d) / 0.75)
			}
			c := base
			if d > rad*0.66 {
				c = dark
			}
			hd := dist2D(float64(x), float64(y), cx-rad*0.34, cy-rad*0.34)
			if hd < rad*0.30 {
				const t = 0.65
				c = color.RGBA{
					uint8(float64(c.R)*(1-t) + 255*t),
					uint8(float64(c.G)*(1-t) + 255*t),
					uint8(float64(c.B)*(1-t) + 255*t),
					255,
				}
			}
			if solid && a < 255 {
				f := float64(a) / 255
				c.R = uint8(float64(c.R)*f + float64(bg.r)*(1-f))
				c.G = uint8(float64(c.G)*f + float64(bg.g)*(1-f))
				c.B = uint8(float64(c.B)*f + float64(bg.b)*(1-f))
			}
			alpha := uint8(a)
			if solid {
				alpha = 255
			}
			im.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, alpha})
		}
	}
	return im
}

// groupImage menggambar ikon grup (dua orang dalam lingkaran biru).
func groupImage(size int) image.Image {
	blue := rgb{0x1F, 0x4E, 0x79}
	soft := rgb{0xB9, 0xCF, 0xE4}
	white := rgb{255, 255, 255}

	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size-1)/2, float64(size-1)/2
	rad := float64(size)/2 - 0.5
	person := func(px, py, s float64) func(float64, float64) bool {
		hr := 0.13 * s
		hx, hy := px, py-0.16*s
		bx, by := px, py+0.16*s
		brx, bry := 0.19*s, 0.15*s
		return func(x, y float64) bool {
			if dist2D(x, y, hx, hy) <= hr {
				return true
			}
			dx, dy := (x-bx)/brx, (y-by)/bry
			return dx*dx+dy*dy <= 1 && y < by+0.13*s
		}
	}
	inSoft := person(cx+0.12*rad, cy+0.10*rad, rad*1.5)
	inMain := person(cx-0.10*rad, cy+0.13*rad, rad*1.55)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := dist2D(float64(x), float64(y), cx, cy)
			if d > rad {
				continue
			}
			a := 255
			if d > rad-0.75 {
				a = int(255 * (rad - d) / 0.75)
			}
			fx, fy := float64(x), float64(y)
			var c rgb
			switch {
			case inMain(fx, fy):
				c = white
			case inSoft(fx, fy):
				c = soft
			default:
				c = blue
			}
			im.SetNRGBA(x, y, color.NRGBA{c.r, c.g, c.b, uint8(a)})
		}
	}
	return im
}

func dist2D(x1, y1, x2, y2 float64) float64 {
	return math.Sqrt((x1-x2)*(x1-x2) + (y1-y2)*(y1-y2))
}

// ---------- foto profil ----------

// decodeSquareImage membaca file gambar apa pun (PNG/JPG/GIF).
func decodeSquareImage(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	im, _, err := image.Decode(bytes.NewReader(b))
	return im, err
}

// resizeSquareCenter potong bagian tengah gambar menjadi persegi lalu
// skala ke size x size dengan interpolasi bilinear.
func resizeSquareCenter(src image.Image, size int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return identicon("kosong", size)
	}
	side := w
	if h < w {
		side = h
	}
	sx := b.Min.X + (w-side)/2
	sy := b.Min.Y + (h-side)/2

	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(side) / float64(size)
	for y := 0; y < size; y++ {
		fy := (float64(y)+0.5)*scale - 0.5
		for x := 0; x < size; x++ {
			fx := (float64(x)+0.5)*scale - 0.5
			dst.Set(x, y, bilinearSample(src, sx, sy, fx, fy, side))
		}
	}
	return dst
}

func bilinearSample(src image.Image, sx, sy int, fx, fy float64, side int) color.RGBA {
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	x0 := clamp(sx+int(fx), sx, sx+side-1)
	y0 := clamp(sy+int(fy), sy, sy+side-1)
	x1 := clamp(x0+1, sx, sx+side-1)
	y1 := clamp(y0+1, sy, sy+side-1)
	tX := fx - float64(int(fx))
	tY := fy - float64(int(fy))
	if tX < 0 {
		tX = 0
	}
	if tY < 0 {
		tY = 0
	}
	c00 := rgbaAt(src, x0, y0)
	c10 := rgbaAt(src, x1, y0)
	c01 := rgbaAt(src, x0, y1)
	c11 := rgbaAt(src, x1, y1)
	lerpX := func(a, b uint32) uint32 { return uint32(float64(a)*(1-tX) + float64(b)*tX) }
	out := [4]uint32{}
	for i := 0; i < 4; i++ {
		top := lerpX(c00[i], c10[i])
		bot := lerpX(c01[i], c11[i])
		out[i] = uint32(float64(top)*(1-tY) + float64(bot)*tY)
	}
	return color.RGBA{uint8(out[0]), uint8(out[1]), uint8(out[2]), uint8(out[3])}
}

func rgbaAt(src image.Image, x, y int) [4]uint32 {
	r, g, b, a := src.At(x, y).RGBA()
	return [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
}

// saveOwnPhoto membaca gambar sumber, potong persegi, skala 256px,
// lalu simpan sebagai avatar.png di folder data aplikasi.
func saveOwnPhoto(srcPath string) error {
	im, err := decodeSquareImage(srcPath)
	if err != nil {
		return fmt.Errorf("gagal membaca gambar: %w", err)
	}
	sq := resizeSquareCenter(im, ownPhotoSize)
	var buf bytes.Buffer
	if err := png.Encode(&buf, sq); err != nil {
		return err
	}
	dst := store.OwnAvatarPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

// removeOwnPhoto menghapus foto profil sendiri (kalau ada).
func removeOwnPhoto() {
	_ = os.Remove(store.OwnAvatarPath())
}
