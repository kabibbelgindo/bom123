//go:build windows

package ui

import (
	"testing"
	"time"

	"belgindo/internal/store"
)

// TestViewHanyaHariIni mengunci aturan baru: ruang chat menampilkan riwayat
// hari ini saja. Entri kemarin tidak boleh ikut tergambar, tetapi tetap ada
// di berkas riwayat.
func TestViewHanyaHariIni(t *testing.T) {
	sekarang := time.Now().Unix()
	entries := []store.Entry{
		{TS: todayStart() - 1, Dir: "in", Kind: "msg", Text: "kemarin"},
		{TS: sekarang, Dir: "in", Kind: "msg", Text: "hari ini"},
		{TS: sekarang, Dir: "out", Kind: "msg", Text: "balasan", ID: "abc", State: "read"},
	}
	got := viewFromHistory(entries, "Budi")
	if len(got) != 2 {
		t.Fatalf("jumlah balon = %d, ingin 2: %+v", len(got), got)
	}
	if got[0].Text != "hari ini" || got[0].Who != "Budi" {
		t.Errorf("balon masuk = %+v", got[0])
	}
	if got[1].Who != "Saya" || got[1].State != "read" || got[1].ID != "abc" {
		t.Errorf("balon keluar = %+v", got[1])
	}
}

// TestViewBatasiBalon mencegah ruang chat diisi ribuan balon sekaligus kalau
// hari itu sangat ramai.
func TestViewBatasiBalon(t *testing.T) {
	n := time.Now().Unix()
	entries := make([]store.Entry, 0, maxViewBubbles+50)
	for i := 0; i < maxViewBubbles+50; i++ {
		entries = append(entries, store.Entry{TS: n, Dir: "out", Kind: "msg", Text: "pesan"})
	}
	if got := viewFromHistory(entries, ""); len(got) != maxViewBubbles {
		t.Fatalf("jumlah balon = %d, ingin %d", len(got), maxViewBubbles)
	}
}

// TestViewLampiranDanSistem memeriksa entri berkas dan sistem di riwayat hari
// ini menjadi balon yang sama seperti saat masih hidup di layar.
func TestViewLampiranDanSistem(t *testing.T) {
	n := time.Now().Unix()
	file, ok := bubbleEntry(store.Entry{TS: n, Dir: "in", Kind: "file",
		Text: "nota.pdf|1234|C:\\inbox\\nota.pdf", Who: "Sari"}, "")
	if !ok {
		t.Fatal("entri berkas tidak menjadi balon")
	}
	if file.Att == nil || file.Att.Name != "nota.pdf" || file.Att.Size != 1234 ||
		file.Att.Path != "C:\\inbox\\nota.pdf" || file.Text != "nota.pdf" {
		t.Errorf("balon berkas = %+v %+v", file, file.Att)
	}
	if file.Who != "Sari" {
		t.Errorf("pengirim grup = %q, ingin Sari", file.Who)
	}
	sys, ok := bubbleEntry(store.Entry{TS: n, Dir: "in", Kind: "ntf", Text: "Rekan online."}, "")
	if !ok || sys.Dir != "sys" || sys.Who != "Info" {
		t.Errorf("entri sistem = %+v (ok=%v)", sys, ok)
	}
	if _, ok := bubbleEntry(store.Entry{TS: n, Kind: "lain-lain"}, ""); ok {
		t.Error("kind tak dikenal seharusnya dilewati")
	}
}

// TestBalonFotoHariIni: riwayat menyimpan nama berkas, jadi penanda foto harus
// dipulihkan dari nama itu agar pratinjau tetap muncul saat jendela dibuka lagi.
func TestBalonFotoHariIni(t *testing.T) {
	n := time.Now().Unix()
	m, ok := bubbleEntry(store.Entry{TS: n, Dir: "out", Kind: "file",
		Text: "foto barang.jpg|2048|C:\\inbox\\foto barang.jpg"}, "")
	if !ok || m.Att == nil || !m.Att.IsPhoto {
		t.Fatalf("foto tidak dikenali: %+v", m)
	}
}
