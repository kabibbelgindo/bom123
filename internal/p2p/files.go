package p2p

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// namaTercadangWindows adalah nama yang tidak boleh dipakai sebagai berkas
// di Windows, terlepas dari ekstensinya.
var namaTercadangWindows = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

const (
	maxNameBytes = 150 // batas aman nama berkas (Windows MAX_PATH 260)
	maxNameRunes = 96
)

// safeFileName membersihkan nama file yang datang dari jaringan supaya bisa
// dipakai sebagai nama berkas sungguhan.
//
// Yang dijaga: pemisah path, karakter terlarang Windows, nama tercadang,
// titik/spasi di ujung, karakter bidi penyamar arah, pemotongan UTF-8 di
// tengah karakter, dan ekstensi yang melewati batas panjang.
func safeFileName(name string) string {
	name = strings.TrimSpace(stripBidi(name))
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.Is(unicode.Cf, r):
			// karakter format (bidi, zero-width) dibuang: bisa dipakai
			// menyamarkan arah atau akhiran sebenarnya
			continue
		case r < 0x20 || r == 0x7f || unicode.IsControl(r):
			b.WriteRune('_')
		case strings.ContainsRune(`\/:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Join(strings.Fields(b.String()), " ")
	name = strings.TrimRight(name, ". ")

	base, ext := splitExt(name)
	base = strings.Join(strings.Fields(stripBidi(base)), " ")
	base = strings.TrimRight(strings.TrimLeft(base, " "), ". ")
	if utf8.RuneCountInString(base) > maxNameRunes {
		r := []rune(base)
		base = string(r[:maxNameRunes])
	}
	if base == "" {
		base = "file"
	}
	if strings.EqualFold(base, "..") || strings.EqualFold(base, ".") {
		base = "file"
	}
	if namaTercadangWindows[strings.ToLower(base)] {
		base = "_" + base
	}
	ext = cleanExt(ext)
	if len(ext) > 16 {
		ext = ext[:16]
	}
	name = base + ext
	if len(name) > maxNameBytes {
		name = cutUTF8(base, maxNameBytes-len(ext)) + ext
	}
	return name
}

// splitExt memisahkan nama menjadi bagian pokok dan ekstensi (termasuk titik).
// Titik di awal nama (".gitconfig") dianggap bagian pokok, bukan ekstensi.
func splitExt(name string) (base, ext string) {
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return name, ""
	}
	return name[:i], name[i:]
}

// cleanExt menahan ekstensi hanya karakter yang aman untuk berkas.
// Titik pertama adalah pemisah ekstensi, jadi tidak diuji.
func cleanExt(ext string) string {
	if len(ext) < 2 {
		return ""
	}
	var b strings.Builder
	for i, r := range ext {
		if i > 0 && r == '.' {
			return "" // ".tar.gz" sudah dipotong splitExt; dua titik mencurigakan
		}
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) || strings.ContainsRune(`\/:*?"<>| `, r) {
			return ""
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// cutUTF8 memotong s pada batas byte <= n tanpa memecah karakter.
func cutUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	if n <= 0 {
		return ""
	}
	return s[:n]
}

// stripBidi membuang karakter pengarah tulisan yang bisa menyamarkan nama
// berkas (mis. "report\u202Exe" yang tampil sebagai "reportexe").
func stripBidi(s string) string {
	if strings.IndexByte(s, byte(0xE2)) < 0 && !strings.ContainsAny(s, "\u200e\u200f\ufeff") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
			'\u200e', '\u200f', '\ufeff', '\u061c':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Ekstensi yang berbahaya bila dijalankan langsung dari folder unduhan.
// Daftar ini dipakai untuk memberi peringatan, bukan untuk menolak transfer.
var ekstensiBerbahaya = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true,
	".js": true, ".jse": true, ".vbs": true, ".vbe": true, ".wsf": true,
	".wsh": true, ".msi": true, ".msp": true, ".lnk": true, ".reg": true,
	".ps1": true, ".jar": true, ".hta": true, ".cpl": true, ".iso": true,
	".docm": true, ".xlsm": true, ".pptm": true, ".apk": true, ".sh": true,
	".url": true, ".pif": true,
}

// IsRiskyExt true bila berkas kemungkinan program yang bisa dijalankan.
func IsRiskyExt(name string) bool {
	return ekstensiBerbahaya[strings.ToLower(filepath.Ext(name))]
}

// WarningRiskyFile mengembalikan pesan peringatan untuk berkas berisiko,
// atau "" bila berkas tampak aman.
func WarningRiskyFile(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if !ekstensiBerbahaya[ext] {
		return ""
	}
	return "Berkas ini berjenis " + ext + " dan bisa berisi program. " +
		"Jangan jalankan kecuali Anda kenal pengirimnya."
}

// hexSha256 memvalidasi bentuk tanda tangan sha256 dari jaringan.
func hexSha256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ruangTersisa mengembalikan byte yang masih bebas di folder tujuan.
// Nilai 0 berarti tidak dapat dipastikan (pemanggil tidak boleh menolak).
func ruangTersisa(dir string) int64 {
	free, err := diskFreeBytes(dir)
	if err != nil || free < 0 {
		return 0
	}
	return free
}

// checkDiskSpace memastikan ruang tulis cukup untuk size + cadangan 32 MB
// untuk berkas .part. Tidak menolak bila sistem tidak bisa melaporkan ruang.
func checkDiskSpace(dir string, size int64) error {
	bebas := ruangTersisa(dir)
	if bebas <= 0 {
		return nil
	}
	if butuh := size + 32<<20; bebas < butuh {
		return fmt.Errorf("ruang disk di folder tujuan tinggal %d MB, kurang untuk berkas %d MB",
			bebas>>20, size>>20)
	}
	return nil
}

// pathAman memaksa hasil tetap berada di dalam folder tujuan,
// meski nama berkas dari jaringan mencoba keluar (mis. "..\\..\\x").
func pathAman(dir, name string) (string, error) {
	full := filepath.Clean(filepath.Join(dir, safeFileName(name)))
	cleanDir := filepath.Clean(dir)
	if full != cleanDir && !strings.HasPrefix(full, cleanDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("nama file mencoba keluar dari folder tujuan")
	}
	return full, nil
}
