//go:build windows

package ui

// i18n.go menyediakan teks antarmuka multi bahasa. Kuncinya adalah teks
// Indonesia (bahasa dasar), jadi T() tanpa katalog apa pun tetap benar.
// Aturan main (Update.txt): SEMUA teks harus ikut bahasa terpilih — karena
// itu uji TestCatalogCoversAllKeys memaksa setiap T("...") yang ada di kode
// punya entri di katalog EN dan ZH. Mengganti bahasa membangun ulang seluruh
// jendela, bukan hanya sebagian, supaya tidak ada campur-campur bahasa.

import (
	"sync"

	"belgindo/internal/store"
)

var (
	i18nMu  sync.RWMutex
	langNow = store.DefaultLanguage
)

// SetLanguage memasang bahasa aktif. false bila kode tidak dikenal.
func SetLanguage(code string) bool {
	if !store.IsLanguageCode(code) {
		return false
	}
	i18nMu.Lock()
	langNow = code
	i18nMu.Unlock()
	return true
}

// Lang mengembalikan bahasa aktif.
func Lang() string {
	i18nMu.RLock()
	defer i18nMu.RUnlock()
	return langNow
}

// catalogs memetakan kode bahasa ke tabel terjemahannya.
func catalogs(code string) map[string]string {
	switch code {
	case "en":
		return catalogEN
	case "zh":
		return catalogZH
	}
	return nil
}

// T menerjemahkan teks. Teks asli (Indonesia) dipakai bila bahasa aktif
// Indonesia atau entri belum ada.
func T(s string) string {
	i18nMu.RLock()
	code := langNow
	i18nMu.RUnlock()
	if c := catalogs(code); c != nil {
		if v, ok := c[s]; ok && v != "" {
			return v
		}
	}
	return s
}
