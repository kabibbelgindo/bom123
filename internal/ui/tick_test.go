//go:build windows

package ui

import (
	"strings"
	"testing"
)

func TestTickHTMLJumlahCentang(t *testing.T) {
	cases := []struct {
		state string
		ticks int
		blue  bool
	}{
		{"pending", 1, false},
		{"sent", 2, false},
		{"read", 2, true},
		{"", 2, false}, // riwayat lama tanpa state dianggap terkirim
	}
	for _, c := range cases {
		got := tickHTML(c.state)
		if n := strings.Count(got, "&#10003;"); n != c.ticks {
			t.Errorf("state %q: %d centang, harusnya %d", c.state, n, c.ticks)
		}
		if strings.Contains(got, "tick-read") != c.blue {
			t.Errorf("state %q: warna centang salah (%s)", c.state, got)
		}
	}
}

func TestBuildChatHTMLHanyaPesanKeluarBercentang(t *testing.T) {
	page := buildChatHTML("Budi", []chatMessage{
		{TS: 1, Dir: "in", Who: "Budi", Text: "halo"},
		{TS: 2, Dir: "out", Who: "Saya", Text: "ya", ID: "abc", State: "pending"},
		{TS: 3, Dir: "sys", Who: "Info", Text: "catatan"},
	})
	// Hitung centang hanya pada balon yang sudah dirender di HTML awal;
	// helper JS tickHTML() memang berisi glyph centang untuk pesan berikutnya.
	rendered := page
	if i := strings.Index(page, "<script>"); i >= 0 {
		rendered = page[:i]
	}
	if n := strings.Count(rendered, "&#10003;"); n != 1 {
		t.Fatalf("jumlah centang = %d, harusnya 1 (hanya pada pesan keluar)", n)
	}
	i := strings.Index(rendered, "msg-out")
	j := strings.Index(rendered, "&#10003;")
	if i < 0 || j < i {
		t.Fatal("centang tidak berada di baris pesan keluar")
	}
}
