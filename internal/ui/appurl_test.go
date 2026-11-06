//go:build windows

package ui

import "testing"

// TestAppURLSesuaiLaporanMSHTML mengunci bentuk URL app:// yang benar-benar
// keluar dari WebView IE. IE menganggap "ready" sebagai nama host lalu
// menempelkan garis miring penutup, jadi perbandingan langsung dengan string
// yang ditulis JS selalu gagal: markReady tidak pernah jalan, emoji/lampiran/
// batal tidak pernah dikenali.
func TestAppURLSesuaiLaporanMSHTML(t *testing.T) {
	cases := map[string]string{
		"app://ready/":               "app://ready",
		"app://typing/":              "app://typing",
		"app://emoji/":               "app://emoji",
		"app://clearchat/":           "app://clearchat",
		"app://copy/?p=abc":          "app://copy?p=abc",
		"app://open/?p=C%3A%5Ca.txt": "app://open?p=C%3A%5Ca.txt",
		"app://emoji/%F0%9F%98%80":   "app://emoji/%F0%9F%98%80",
		"app://ready":                "app://ready",
		"file:///C:/x/y.html":        "file:///C:/x/y.html",
	}
	for in, want := range cases {
		if got := appURL(in); got != want {
			t.Errorf("appURL(%q) = %q, ingin %q", in, got, want)
		}
	}
	if got := parseAppQuery(appURL("app://copy/?p=halo%20dunia"), "p"); got != "halo dunia" {
		t.Errorf("parameter hilang setelah normalisasi: %q", got)
	}
}
