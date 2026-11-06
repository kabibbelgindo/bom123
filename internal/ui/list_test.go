//go:build windows

package ui

import (
	"testing"

	"belgindo/internal/p2p"
	"belgindo/internal/store"
)

func testPeers() []p2p.Peer {
	return []p2p.Peer{
		{UID: "u1", Nick: "Kabib", PC: "ADMPRD2", Online: true, StatMsg: "di ruang meeting"},
		{UID: "u2", Nick: "Siti", PC: "ADMPRD3", Online: true},
	}
}

func newTestModel() *App {
	a := &App{
		cfg:       &store.Config{},
		collapsed: map[string]bool{},
		groupDefs: []store.Group{{ID: "g1", Name: "Tim Gudang", Members: []string{"u1", "u2"}}},
		unread:    map[string]int{},
	}
	a.model = &userModel{app: a}
	return a
}

// TestBuildRowsBertingkat memastikan daftar berisi judul bagian lalu anggotanya
// dan pesan status rekan tampil di dalam kurung.
func TestBuildRowsBertingkat(t *testing.T) {
	a := newTestModel()
	a.model.rows = a.buildRows(testPeers())

	want := []string{
		"▾ Teman — 2", "Kabib (di ruang meeting)", "Siti",
		"▾ Tim Gudang (2)", "Kabib (di ruang meeting)", "Siti",
	}
	if len(a.model.rows) != len(want) {
		t.Fatalf("jumlah baris = %d, ingin %d", len(a.model.rows), len(want))
	}
	for i, wantText := range want {
		if got := a.model.Value(i, 1); got != wantText {
			t.Errorf("baris %d = %v, ingin %q", i, got, wantText)
		}
		isHeader := a.model.rows[i].header
		if wantHeader := i == 0 || i == 3; isHeader != wantHeader {
			t.Errorf("baris %d header = %t, ingin %t", i, isHeader, wantHeader)
		}
	}
	if got := a.model.Value(1, 2); got != "Online" {
		t.Errorf("kolom status rekan = %v", got)
	}
}

// TestBadgeBelumDibaca memastikan nama rekan yang punya pesan belum dibuka
// diberi angka jumlah, dan rekan tanpa pesan belum dibaca tetap bersih.
func TestBadgeBelumDibaca(t *testing.T) {
	a := newTestModel()
	a.unread["u1"] = 3
	a.model.rows = a.buildRows(testPeers())

	if got := a.model.Value(1, 1); got != "Kabib [3] (di ruang meeting)" {
		t.Errorf("baris belum dibaca = %v, ingin \"Kabib [3] (di ruang meeting)\"", got)
	}
	if got := a.model.Value(2, 1); got != "Siti" {
		t.Errorf("baris sudah dibaca = %v, ingin \"Siti\"", got)
	}
	if got := a.unreadCount("u2"); got != 0 {
		t.Errorf("jumlah belum dibaca u2 = %d, ingin 0", got)
	}
}

// TestCollapseMenyembunyikanAnggota memastikan bagian yang ditutup hanya
// menampilkan judulnya, dan judul grup tetap mewakili ID grupnya.
func TestCollapseMenyembunyikanAnggota(t *testing.T) {
	a := newTestModel()
	a.collapsed["g1"] = true
	a.model.rows = a.buildRows(testPeers())

	if len(a.model.rows) != 4 {
		t.Fatalf("jumlah baris = %d, ingin 4 (judul Teman + 2 rekan + judul grup)", len(a.model.rows))
	}
	if got := a.model.Value(3, 1); got != "▸ Tim Gudang (2)" {
		t.Errorf("judul grup tertutup = %v", got)
	}
	if uid, gid := rowIDs(a.model.rows[3]); uid != "" || gid != "g1" {
		t.Errorf("judul grup: uid=%q gid=%q, ingin uid kosong gid g1", uid, gid)
	}
	if uid, gid := rowIDs(a.model.rows[0]); uid != "" || gid != "" {
		t.Errorf("judul Teman: uid=%q gid=%q, ingin keduanya kosong", uid, gid)
	}
	if uid, gid := rowIDs(a.model.rows[1]); uid != "u1" || gid != "" {
		t.Errorf("baris rekan: uid=%q gid=%q, ingin uid u1", uid, gid)
	}
}
