//go:build windows

package ui

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"belgindo/internal/p2p"
	"belgindo/internal/store"

	"github.com/lxn/walk"
	"golang.org/x/sys/windows"
)

// chatAttach lampiran satu pesan chat.
type chatAttach struct {
	IsPhoto bool
	Name    string
	Size    int64
	Path    string
}

// maxTransferSize harus sama dengan batas protokol (proto.MaxFileSize).
const maxTransferSize = int64(500 << 20)

// maxDimFoto batas sisi terpanjang thumbnail yang dibuat untuk tampilan.
const maxDimFoto = 900

// fotoBisaDitampilExt adalah format yang pasti dapat digambar WebView IE11.
// .webp/.avif/.tif sengaja tidak masuk: IE11 tidak bisa menampilkannya, jadi
// diperlakukan sebagai dokumen biasa (antisipasi.txt).
var fotoBisaDitampilExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true,
}

// isPhotoName true bila nama berkas adalah gambar yang bisa ditampilkan.
func isPhotoName(name string) bool {
	return fotoBisaDitampilExt[strings.ToLower(filepath.Ext(name))]
}

// riskyName true bila ekstensi berkas perlu diperingatkan sebelum dibuka.
func riskyName(name string) bool { return p2p.IsRiskyExt(name) }

// existingPath mengembalikan path bila berkasnya masih ada.
func existingPath(path string) string {
	if path == "" {
		return ""
	}
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return path
	}
	return ""
}

// openAppURL membuat URL navigasi "buka berkas" untuk halaman chat.
func openAppURL(path string) string {
	return "app://open?p=" + queryEscapePath(path)
}

// queryEscapePath mengamankan path untuk dipakai dalam URL navigasi WebView.
func queryEscapePath(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// thumbnailFor membuat (atau memakai ulang) thumbnail foto dan mengembalikan
// URL relatif terhadap folder data aplikasi. "" bila foto tidak tersedia.
func thumbnailFor(path string) string {
	src := existingPath(path)
	if src == "" || !isPhotoName(src) {
		return ""
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d", strings.ToLower(src), srcModTime(src))))
	name := "thumb_" + hex.EncodeToString(sum[:])[:24] + ".jpg"
	dst := filepath.Join(store.MediaDir(), name)
	if _, err := os.Stat(dst); err == nil {
		return "media/" + name
	}
	if err := writeThumbnail(src, dst, maxDimFoto); err != nil {
		return ""
	}
	return "media/" + name
}

func srcModTime(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().Unix()
	}
	return 0
}

// writeThumbnail membaca gambar, mengecilkan, lalu menyimpan JPEG ringan.
func writeThumbnail(src, dst string, maxDim int) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	im, err := decodeImageFile(f, src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	w, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(w, scaleToFit(normalizeRGBA(im), maxDim), &jpeg.Options{Quality: 82}); err != nil {
		w.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// normalizeRGBA menormalkan gambar apa pun menjadi RGBA opaque di atas putih,
// karena JPEG tidak punya saluran alpha.
func normalizeRGBA(im image.Image) *image.RGBA {
	b := im.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), im, b.Min, draw.Over)
	return out
}

// scaleToFit mengecilkan gambar memakai rata-rata area (box filter) dengan
// batas sisi terpanjang maxDim.
func scaleToFit(src image.Image, maxDim int) *image.RGBA {
	sb := src.Bounds()
	w, h := sb.Dx(), sb.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	dw, dh := w, h
	switch {
	case w >= h && w > maxDim:
		dw, dh = maxDim, int(float64(h)*float64(maxDim)/float64(w)+0.5)
	case h > w && h > maxDim:
		dh, dw = maxDim, int(float64(w)*float64(maxDim)/float64(h)+0.5)
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	fx := float64(w) / float64(dw)
	fy := float64(h) / float64(dh)
	for y := 0; y < dh; y++ {
		y0 := sb.Min.Y + int(float64(y)*fy)
		y1 := sb.Min.Y + int(float64(y+1)*fy)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > sb.Max.Y {
			y1 = sb.Max.Y
		}
		for x := 0; x < dw; x++ {
			x0 := sb.Min.X + int(float64(x)*fx)
			x1 := sb.Min.X + int(float64(x+1)*fx)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > sb.Max.X {
				x1 = sb.Max.X
			}
			var rs, gs, bs, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, b, _ := src.At(sx, sy).RGBA()
					rs += uint64(r >> 8)
					gs += uint64(g >> 8)
					bs += uint64(b >> 8)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			out.SetRGBA(x, y, color.RGBA{uint8(rs / n), uint8(gs / n), uint8(bs / n), 255})
		}
	}
	return out
}

// bukaBerkasBawaan membuka berkas dengan aplikasi standar Windows.
func bukaBerkasBawaan(path string) error { return shellOpen("open", path) }

// bukaDenganDialog menampilkan pilihan "Open with" Windows.
func bukaDenganDialog(path string) error { return shellOpen("openas", path) }

func shellOpen(verb, path string) error {
	v, err := windows.UTF16PtrFromString(verb)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, v, p, nil, nil, windows.SW_SHOWNORMAL)
}

// handleOpenRequest menindaklanjuti klik pada foto/tautan dokumen di halaman
// chat: foto menanyakan aplikasi pembuka, dokumen langsung dibuka dengan
// aplikasi pilihan Windows. Mengembalikan pesan galat ("" bila sukses).
func handleOpenRequest(path string, preferChooser bool) string {
	p := existingPath(path)
	if p == "" {
		return T("Berkas tidak ditemukan lagi di komputer ini.")
	}
	var err error
	if preferChooser || isPhotoName(p) {
		err = bukaDenganDialog(p)
	} else {
		err = bukaBerkasBawaan(p)
	}
	if err != nil {
		return T("Gagal membuka berkas: ") + err.Error()
	}
	return ""
}

// expandDropPaths memperluas hasil drag & drop: folder diambil seluruh
// berkasnya termasuk subfolder, berkas biasa dipertahankan apa adanya.
func expandDropPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			out = append(out, p)
			continue
		}
		_ = filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if !info.IsDir() {
				out = append(out, path)
			}
			return nil
		})
	}
	return out
}

// berkasBolehDikirim memfilter daftar berkas: kumpulkan yang ukurannya wajar.
func berkasBolehDikirim(paths []string) (lolos []string, terlaluBesar []string) {
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		if fi.Size() > maxTransferSize {
			terlaluBesar = append(terlaluBesar, filepath.Base(p))
			continue
		}
		lolos = append(lolos, p)
	}
	return lolos, terlaluBesar
}

// salinTeks menulis teks ke clipboard Windows (cadangan bila JS gagal).
func salinTeks(text string) error {
	return walk.Clipboard().SetText(text)
}
