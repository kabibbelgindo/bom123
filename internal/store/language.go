// Daftar bahasa antarmuka yang dikenal aplikasi. Katalog teksnya ada di
// paket ui (i18n.go); simpanan konfigurasi hanya menyimpan kodenya.
package store

// DefaultLanguage dipakai bila konfigurasi belum menentukan bahasa.
const DefaultLanguage = "id"

// Language satu bahasa yang bisa dipilih pengguna.
type Language struct {
	Code   string // kode tersimpan di config.json
	Native string // nama bahasa dalam bahasanya sendiri
}

// Languages urutan menu "Pilih Bahasa".
var Languages = []Language{
	{Code: "id", Native: "Bahasa Indonesia"},
	{Code: "en", Native: "English"},
	{Code: "zh", Native: "中文"},
}

// IsLanguageCode true bila kode dikenal.
func IsLanguageCode(code string) bool {
	for _, l := range Languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

// LanguageNative nama tampilan sebuah kode bahasa (fallback: kodenya sendiri).
func LanguageNative(code string) string {
	for _, l := range Languages {
		if l.Code == code {
			return l.Native
		}
	}
	return code
}
