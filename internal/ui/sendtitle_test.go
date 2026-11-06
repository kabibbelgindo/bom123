//go:build windows

package ui

import (
	"net/url"
	"strings"
	"testing"
)

// TestParseSendTitleBawaIDBalon mengunci kontrak judul dokumen: halaman
// menggambar balonnya sendiri lalu meneruskan ID balon itu ke Go. Bila format
// ini berubah, Go dan halaman tidak lagi sepakat dan pesan sendiri muncul dua
// kali atau tidak sama sekali.
func TestParseSendTitleBawaIDBalon(t *testing.T) {
	id := "0123456789abcdef"
	text := "halo, jangan dobel"
	title := "\x01" + "7" + "|" + id + "|" + url.QueryEscape(text)
	gotID, gotText, ok := parseSendTitle(title)
	if !ok {
		t.Fatalf("judul kirim tidak terbaca: %q", title)
	}
	if gotID != id {
		t.Errorf("ID = %q, ingin %q", gotID, id)
	}
	if gotText != text {
		t.Errorf("teks = %q, ingin %q", gotText, text)
	}
}

func TestParseSendTitleTolakBentukAsing(t *testing.T) {
	for _, title := range []string{"Belgindo Chat", "", "\x017tanpa pemisah"} {
		if _, _, ok := parseSendTitle(title); ok {
			t.Errorf("judul %q seharusnya bukan sinyal kirim", title)
		}
	}
	// ID rusak tidak boleh membuang pesan: Go membuat ID sendiri dan
	// menggambar balonnya sendiri.
	for _, title := range []string{
		"\x017|zz|xxx",
		"\x017|" + strings.Repeat("a", 40) + "|xxx",
	} {
		id, text, ok := parseSendTitle(title)
		if !ok {
			t.Errorf("pesan dibuang untuk %q", title)
			continue
		}
		if id != "" {
			t.Errorf("ID rusak %q seharusnya menjadi kosong", id)
		}
		if text != "xxx" {
			t.Errorf("teks = %q, ingin xxx", text)
		}
	}
	if got := cleanMsgID("ABC0123456789ABC"); got != "abc0123456789abc" {
		t.Errorf("cleanMsgID menormalkan jadi %q", got)
	}
}

// TestHalamanMendukungGemaLokal memastikan JS yang dibutuhkan gema lokal dan
// bridge batch ada di halaman; tanpa salah satunya pesan sendiri terlambat
// tampil atau balon tersisih tidak bisa ditarik kembali.
func TestHalamanMendukungGemaLokal(t *testing.T) {
	page := buildChatPageHTML(chatPageOpts{Name: "Budi", Sub: "PC-01", Placeholder: "ph"})
	for _, need := range []string{
		"function __dispatchAll(", // satu navigasi membawa banyak perintah
		"function newMsgId(",      // ID balon dibuat di halaman
		"function dropMsgs(",      // balon yang ternyata tidak terkirim ditarik
		"case 'drop':",
		"function clockNow(",
	} {
		if !strings.Contains(page, need) {
			t.Errorf("halaman chat kehilangan %q", need)
		}
	}
	// submit() harus menggambar balon sendiri SEBELUM memanggil Go.
	i := strings.Index(page, "function submit(")
	if i < 0 {
		t.Fatal("submit() tidak ditemukan")
	}
	body := page[i:]
	if j := strings.Index(body, "function selectionText"); j > 0 {
		body = body[:j]
	}
	add := strings.Index(body, "addMsgs(")
	hold := strings.Index(body, "document.title")
	if add < 0 || hold < 0 || add > hold {
		t.Errorf("submit() harus menggambar balon dulu baru memberi sinyal ke Go (add=%d title=%d)", add, hold)
	}
}
