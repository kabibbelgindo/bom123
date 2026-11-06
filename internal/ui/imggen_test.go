package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Identicon harus deterministik: seed sama = gambar sama,
// seed beda = hampir pasti beda.
func TestIdenticonDeterministic(t *testing.T) {
	a := identicon("uid-abc123", 40).(*image.RGBA)
	b := identicon("uid-abc123", 40).(*image.RGBA)
	c := identicon("uid-berbeda", 40).(*image.RGBA)

	if a.Bounds() != b.Bounds() {
		t.Fatal("bounds beda untuk seed sama")
	}
	same, diff := 0, 0
	for i := range a.Pix {
		if a.Pix[i] == b.Pix[i] {
			same++
		}
		if a.Pix[i] != c.Pix[i] {
			diff++
		}
	}
	if same != len(a.Pix) {
		t.Fatalf("seed sama menghasilkan gambar beda (same=%d/%d)", same, len(a.Pix))
	}
	if diff == 0 {
		t.Fatal("seed beda menghasilkan gambar sama")
	}
}

// Simetri horizontal identicon.
func TestIdenticonSymmetry(t *testing.T) {
	const size = 40
	im := identicon("sym-test", size).(*image.RGBA)
	for y := 0; y < size; y++ {
		for x := 0; x < size/2; x++ {
			l := im.RGBAAt(x, y)
			r := im.RGBAAt(size-1-x, y)
			if l != r {
				t.Fatalf("tidak simetris di (%d,%d): %v vs %v", x, y, l, r)
			}
		}
	}
}

// 12 avatar bawaan harus semuanya berbeda satu sama lain.
func TestBuiltinAvatarsDistinct(t *testing.T) {
	hashes := map[string]bool{}
	for i := 0; i < builtinAvatarCount; i++ {
		im := builtinAvatar(i, 32)
		sum := ""
		b := im.(*image.RGBA).Pix
		for _, v := range b {
			sum += string(rune(v))
		}
		if hashes[sum] {
			t.Fatalf("avatar bawaan %d duplikat", i)
		}
		hashes[sum] = true
	}
}

// Titik status harus punya piksel berwarna dan tepi transparan.
func TestDotImage(t *testing.T) {
	const size = 16
	im := dotImage(rgb{0xE3, 0x3A, 0x2E}, size).(*image.NRGBA)
	if im.At(0, 0).(color.NRGBA).A != 0 {
		t.Fatal("pojok harus transparan")
	}
	cx := im.NRGBAAt(size/2, size/2)
	if cx.A == 0 {
		t.Fatal("pusat harus solid")
	}
}

// Resize persegi dengan crop tengah.
func TestResizeSquareCenter(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			if x < 25 || x >= 75 {
				src.Set(x, y, color.RGBA{255, 0, 0, 255}) // sisi merah
			} else {
				src.Set(x, y, color.RGBA{0, 0, 255, 255}) // tengah biru
			}
		}
	}
	out := resizeSquareCenter(src, 20).(*image.NRGBA)
	// hasil harus didominasi biru (crop tengah) tanpa merah di tepi kiri/kanan
	for y := 0; y < 20; y++ {
		if out.NRGBAAt(0, y).R > 128 {
			t.Fatal("tepi kiri masih merah: crop tengah salah")
		}
		if out.NRGBAAt(19, y).R > 128 {
			t.Fatal("tepi kanan masih merah: crop tengah salah")
		}
	}
}

// saveOwnPhoto + decode balik harus menghasilkan PNG persegi 256px.
func TestSaveOwnPhotoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	src := image.NewRGBA(image.Rect(0, 0, 300, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 300; x++ {
			src.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	srcPath := filepath.Join(dir, "foto.jpg")
	f, err := os.Create(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := saveOwnPhoto(srcPath); err != nil {
		t.Fatalf("saveOwnPhoto: %v", err)
	}
	im, err := decodeSquareImage(filepath.Join(dir, "BelgindoMessenger", "avatar.png"))
	if err != nil {
		t.Fatalf("decode balik: %v", err)
	}
	if im.Bounds().Dx() != ownPhotoSize || im.Bounds().Dy() != ownPhotoSize {
		t.Fatalf("ukuran hasil %v, ingin %dx%d", im.Bounds(), ownPhotoSize, ownPhotoSize)
	}
}
