package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func timeBefore(days int) time.Time { return time.Now().AddDate(0, 0, -days) }

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	c := LoadConfig()
	if c.UID == "" {
		t.Fatal("UID default tidak dibuat")
	}
	c.Nick = "Kasir-01"
	c.Sound = true
	if err := SaveConfig(c); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	c2 := LoadConfig()
	if c2.Nick != "Kasir-01" || !c2.Sound || c2.UID != c.UID {
		t.Fatalf("konfigurasi tidak sama: %+v", c2)
	}
}

func TestHistoryAppendLoad(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	for i := 0; i < 350; i++ {
		e := Entry{TS: int64(1000 + i), Dir: "in", Kind: "msg", Text: "pesan"}
		if err := AppendHistory("uid-x", e); err != nil {
			t.Fatalf("AppendHistory: %v", err)
		}
	}
	got := LoadHistory("uid-x", 300)
	if len(got) != 300 {
		t.Fatalf("jumlah entri = %d, ingin 300", len(got))
	}
	if got[0].TS != 1050 || got[len(got)-1].TS != 1349 {
		t.Fatalf("rentang entri salah: %d..%d", got[0].TS, got[len(got)-1].TS)
	}
	if got2 := LoadHistory("uid-tidak-ada", 10); got2 != nil {
		t.Fatal("peer tak dikenal harusnya tanpa riwayat")
	}
}

func TestPruneHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	if err := AppendHistory("lama", Entry{TS: 1, Dir: "in", Kind: "msg", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(Dir(), "history", "lama.jsonl")
	if err := AppendHistory("baru", Entry{TS: time.Now().Unix(), Dir: "in", Kind: "msg", Text: "y"}); err != nil {
		t.Fatal(err)
	}
	// waktu ubah berkas dibuat sangat lama: pemangkasan menilai isi berkas,
	// bukan stat, jadi riwayat hidup tidak ikut terhapus
	if err := os.Chtimes(old, timeBefore(100), timeBefore(100)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(Dir(), "history", "baru.jsonl"), timeBefore(100), timeBefore(100)); err != nil {
		t.Fatal(err)
	}
	PruneHistory(90)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("riwayat lama harusnya terhapus")
	}
	if _, err := os.Stat(filepath.Join(Dir(), "history", "baru.jsonl")); err != nil {
		t.Fatal("riwayat baru harusnya masih ada")
	}
}

func TestPruneHistoryGrupIkutTerpangkas(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	if err := AppendGroupHistory("g0123456789ab", Entry{TS: 1, Dir: "in", Kind: "msg", Text: "lama", Who: "Budi"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroupHistory("g0123456789ab", Entry{TS: time.Now().Unix(), Dir: "in", Kind: "msg", Text: "baru", Who: "Siti"}); err != nil {
		t.Fatal(err)
	}
	PruneHistory(90)
	got := LoadGroupHistory("g0123456789ab", 10)
	if len(got) != 1 || got[0].Text != "baru" {
		t.Fatalf("riwayat grup tidak terpangkas benar: %+v", got)
	}
}

func TestSafeKeyMenolakKunciJaringanBerbahaya(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	for _, bad := range []string{"../../windows/system32/config", "a/b", "a\\b", "spasi nama", "..", strings.Repeat("x", 200), "nama\x00"} {
		k := SafeKey(bad)
		if k != filepath.Base(k) || k == "" || k == "." || k == ".." {
			t.Fatalf("SafeKey(%q) = %q, masih bisa dipakai keluar folder", bad, k)
		}
	}
	for _, okKey := range []string{"0123456789abcdef", "g0123456789ab", "uid-x", "u9"} {
		if SafeKey(okKey) != okKey {
			t.Fatalf("kunci sah berubah: %q -> %q", okKey, SafeKey(okKey))
		}
	}
	// menulis dengan kunci jahat harus tetap berada di dalam folder riwayat
	if err := AppendHistory("../../escape", Entry{TS: 1, Dir: "in", Kind: "msg", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(Dir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entri riwayat = %d, ingin 1 (di dalam folder)", len(entries))
	}
}

func TestCleanName(t *testing.T) {
	if got := CleanName("  Budi\tSantos  "); got != "Budi Santos" {
		t.Fatalf("rapikan spasi: %q", got)
	}
	if got := CleanName("Budi\nEvil\rX"); strings.ContainsAny(got, "\n\r") {
		t.Fatalf("karakter kontrol lolos: %q", got)
	}
	if got := CleanName("abc\u202defg"); strings.Contains(got, "\u202d") || strings.Contains(got, "\u202e") {
		t.Fatalf("RLO/LRO lolos: %q", got)
	}
	long := strings.Repeat("A", 40)
	if got := CleanName(long); len([]rune(got)) != 25 || !strings.HasSuffix(got, "…") {
		t.Fatalf("pemotongan 24 rune salah: %q", got)
	}
	if got := CleanName("😀nama"); !strings.HasPrefix(got, "😀") {
		t.Fatalf("emoji terpotong rusak: %q", got)
	}
}

func TestKunciPercakapanPakaiConfigAktif(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	c := LoadConfig()
	c.Nick = "Kasir-01"
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	if err := SetChatPassword(c, "0123456789abcdef", "rahasia"); err != nil {
		t.Fatalf("SetChatPassword: %v", err)
	}
	// simpanan lain setelahnya tidak boleh menghapus kunci
	c.Status = 3
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	back := LoadConfig()
	if !IsChatLocked(back, "0123456789abcdef") {
		t.Fatal("kunci percakapan hilang setelah simpanan lain")
	}
	if back.Status != 3 {
		t.Fatalf("perubahan lain hilang: %+v", back)
	}
	if VerifyChatPassword(back, "0123456789abcdef", "salah") {
		t.Fatal("password salah diterima")
	}
	if !VerifyChatPassword(back, "0123456789abcdef", "rahasia") {
		t.Fatal("password benar ditolak")
	}
	if err := RemoveChatPassword(back, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if IsChatLocked(back, "0123456789abcdef") {
		t.Fatal("kunci belum benar-benar lepas")
	}
}

func TestClearAllHistory(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	if err := AppendHistory("aaaa", Entry{TS: 1, Dir: "in", Kind: "msg", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroupHistory("gaaaaaaaaaaaaaa", Entry{TS: 1, Dir: "in", Kind: "msg", Text: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearAllHistory(); err != nil {
		t.Fatal(err)
	}
	if n := len(ListHistory()); n != 0 {
		t.Fatalf("riwayat perorangan tersisa %d", n)
	}
	if n := len(ListGroupHistory()); n != 0 {
		t.Fatalf("riwayat grup tersisa %d", n)
	}
}

func TestSetHistoryState(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	add := func(e Entry) {
		if err := AppendHistory("u9", e); err != nil {
			t.Fatalf("AppendHistory: %v", err)
		}
	}
	add(Entry{TS: 1, Dir: "out", Kind: "msg", Text: "a", ID: "id1", State: "pending"})
	add(Entry{TS: 2, Dir: "in", Kind: "msg", Text: "b", ID: "id2"})
	add(Entry{TS: 3, Dir: "out", Kind: "msg", Text: "c", ID: "id3", State: "sent"})

	if err := SetHistoryState("u9", []string{"id1", "id2", "id3"}, "read"); err != nil {
		t.Fatalf("SetHistoryState: %v", err)
	}
	got := LoadHistory("u9", 10)
	if len(got) != 3 {
		t.Fatalf("jumlah entri berubah: %d", len(got))
	}
	if got[0].State != "read" || got[2].State != "read" {
		t.Fatalf("centang biru tidak tersimpan: %+v", got)
	}
	if got[1].State != "" || got[1].Dir != "in" {
		t.Fatalf("pesan masuk tidak boleh berubah: %+v", got[1])
	}
	if got[0].Text != "a" || got[2].Text != "c" {
		t.Fatalf("isi riwayat rusak: %+v", got)
	}

	// status tidak boleh mundur
	if err := SetHistoryState("u9", []string{"id1"}, "pending"); err != nil {
		t.Fatalf("SetHistoryState ulang: %v", err)
	}
	if got := LoadHistory("u9", 10); got[0].State != "read" {
		t.Fatalf("state read kembali mundur: %+v", got[0])
	}

	// riwayat yang belum ada bukan kesalahan
	if err := SetHistoryState("u-kosong", []string{"id"}, "read"); err != nil {
		t.Fatalf("riwayat kosong: %v", err)
	}
}
