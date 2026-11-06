//go:build windows

package ui

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestEnterKirimCtrlEnterBaris mengunci tata letak tombol Enter di kolom balas:
// Enter biasa mengirim, Ctrl+Enter (atau Shift+Enter) menyisip baris baru.
// Kalau urutan ini tertukar, pengguna mengirim pesan saat hanya ingin ganti
// baris. MSHTML kadang hanya meneruskan Enter sebagai keypress, jadi kedua
// event harus memakai satu penangan yang sama.
func TestEnterKirimCtrlEnterBaris(t *testing.T) {
	page := buildChatPageHTML(chatPageOpts{Name: "Budi", Sub: "PC-01", Placeholder: "ph"})
	i := strings.Index(page, "function enterKey(")
	if i < 0 {
		t.Fatal("penanganan tombol Enter tidak ditemukan")
	}
	body := page[i:]
	if j := strings.Index(body, "\n}\n"); j > 0 {
		body = body[:j]
	}
	ktrl := strings.Index(body, "e.ctrlKey")
	kirim := strings.Index(body, "submit()")
	if ktrl < 0 || kirim < 0 {
		t.Fatalf("handler Enter harus menyebut ctrlKey dan submit: %q", body)
	}
	if ktrl > kirim {
		t.Errorf("cabang Ctrl+Enter harus diperiksa sebelum submit()")
	}
	for _, need := range []string{
		"inp.onkeydown = function(e){",
		"inp.onkeypress = enterKey;",
		"function newLine(",
		"String.fromCharCode(0x200b)",
	} {
		if !strings.Contains(page, need) {
			t.Errorf("halaman chat kehilangan %q", need)
		}
	}
	// newLine tidak boleh lagi menyerah bila execCommand gagal; kalau menyerah,
	// Ctrl+Enter tampak tidak berfungsi.
	nl := page[strings.Index(page, "function newLine("):]
	nl = nl[:strings.Index(nl, "\n}\n")]
	if strings.Contains(nl, "return true") {
		t.Errorf("newLine() masih membatalkan diri saat execCommand gagal: %q", nl)
	}
	if !strings.Contains(nl, "noDefault(e)") {
		t.Errorf("newLine() harus membatalkan tindakan bawaan IE: %q", nl)
	}
	// submit() harus membuang penjaga kursor, tidak boleh ikut terkirim.
	s := page[strings.Index(page, "function submit("):]
	s = s[:strings.Index(s, "function selectionText")]
	if !strings.Contains(s, "split(ZWSP)") {
		t.Errorf("submit() tidak membersihkan penjaga baris baru: %q", s)
	}
}

// TestPesanBarisGandaDirawat: teks multi-baris harus tampil sebagai baris baru
// di balon, baik halaman awal (Go) maupun pesan berjalan (JS).
func TestPesanBarisGandaDirawat(t *testing.T) {
	m := chatMessage{TS: time.Now().Unix(), Dir: "in", Who: "Budi", Text: "baris satu\nbaris dua"}
	if got := pageBubbleGo(m); !strings.Contains(got, "baris satu<br/>baris dua") {
		t.Errorf("balon versi Go tidak merawat baris baru: %s", got)
	}
	if !strings.Contains(bubbleJS, `.replace(/\n/g,'<br/>')`) {
		t.Error("bubbleHTML versi JS kehilangan pengganti baris baru")
	}
	line := ""
	for _, ln := range strings.Split(bubbleJS, "\n") {
		if strings.Contains(ln, "msg-text") {
			line = ln
		}
	}
	if !strings.Contains(line, "linkify(esc(m.t)") || !strings.Contains(line, "br/>") {
		t.Errorf("balon teks versi JS harus menautkan setelah baris baru diubah: %q", line)
	}
}

// TestJudulKirimBawaBarisbaru: halaman mengirim teks lewat document.title
// dalam bentuk percent-encoded; baris baru harus selamat sampai Go.
func TestJudulKirimBawaBarisbaru(t *testing.T) {
	text := "réuni jam 7\r\nruang rapat\r\njangan terlambat"
	title := "\x013|" + "0123456789abcdef" + "|" + url.QueryEscape(text)
	id, got, ok := parseSendTitle(title)
	if !ok || id == "" {
		t.Fatalf("sinyal kirim tidak terbaca: %q", title)
	}
	if got != text {
		t.Errorf("teks = %q, ingin %q", got, text)
	}
}
