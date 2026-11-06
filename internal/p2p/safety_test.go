package p2p

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"belgindo/internal/proto"
)

// TestPesanDuplikatHanyaSekali: ulang kirim pesan dengan ID yang sama
// (mis. karena retry outbox) tidak boleh menampilkan pesan dua kali.
func TestPesanDuplikatHanyaSekali(t *testing.T) {
	b := newLAN(t, 1)[0]
	b.mu.Lock()
	b.peers[uidZ] = &Peer{UID: uidZ, Nick: "Zubir", PC: "PC-Z", Online: true, LastSeen: time.Now().Unix()}
	b.mu.Unlock()

	var mu sync.Mutex
	got := 0
	b.OnEvent(func(e Event) {
		if e.Kind == EvMsgIn && e.ID == "id-kembar" {
			mu.Lock()
			got++
			mu.Unlock()
		}
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)

	kirim := func() int {
		if err := proto.Write(conn, &proto.Envelope{T: proto.TMsg, ID: "id-kembar",
			From: uidZ, Nick: "Zubir", To: b.MyUID(), Text: "satu pesan", TS: time.Now().Unix()}); err != nil {
			t.Fatal(err)
		}
		e, err := proto.Read(br)
		if err != nil || e.T != proto.TAck {
			t.Fatalf("tanpa ack: %v %+v", err, e)
		}
		return 0
	}
	kirim()
	kirim()
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 1 {
		t.Fatalf("pesan duplikat tampil %d kali, ingin 1", n)
	}
}

// TestPesanGrupDariRekanAsing: pesan grup dari rekan yang belum pernah
// dilihat harus tetap masuk (tidak dibuang) dan membawa identitas grupnya.
func TestPesanGrupDariRekanAsing(t *testing.T) {
	b := newLAN(t, 1)[0]
	gid := testGroupID(1)

	chMsg := make(chan Event, 4)
	chInfo := make(chan Event, 4)
	b.OnEvent(func(e Event) {
		switch e.Kind {
		case EvMsgIn:
			if e.Group == gid {
				chMsg <- e
			}
		case EvGroupInfo:
			if e.Group == gid {
				chInfo <- e
			}
		}
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	env := &proto.Envelope{T: proto.TMsg, ID: "g1", From: uidZ, Nick: "Zubir",
		To: b.MyUID(), Text: "rapat grup", TS: time.Now().Unix(),
		G: gid, GN: "Tim Gudang", GO: uidZ, GV: 3}
	if err := proto.Write(conn, env); err != nil {
		t.Fatal(err)
	}
	if e, err := proto.Read(br); err != nil || e.T != proto.TAck {
		t.Fatalf("pesan grup tidak di-ACK: %v", err)
	}

	select {
	case e := <-chInfo:
		if e.GroupName != "Tim Gudang" || e.GroupInfo.Version != 3 {
			t.Fatalf("identitas grup salah: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("definisi grup tidak ikut terbawa")
	}
	select {
	case e := <-chMsg:
		if e.UID != uidZ || e.Text != "rapat grup" {
			t.Fatalf("isi pesan grup salah: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pesan grup dibuang padahal grup belum dikenal")
	}

	// siaran identitas grup hanya sekali per versi, tidak tiap pesan.
	for i := 0; i < 3; i++ {
		env.ID = fmt.Sprintf("g%d", 2+i)
		if err := proto.Write(conn, env); err != nil {
			t.Fatal(err)
		}
		if _, err := proto.Read(br); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case <-chInfo:
		t.Fatal("identitas grup disiarkan berulang untuk versi yang sama")
	default:
	}
}

// TestGrupDitolakBilaIdentitasSah: info grup dengan UID/ID palsu tidak
// boleh masuk daftar grup.
func TestGrupDitolakBilaIdentitasSah(t *testing.T) {
	b := newLAN(t, 1)[0]
	var mu sync.Mutex
	terima := 0
	b.OnEvent(func(e Event) {
		if e.Kind == EvGroupInfo {
			mu.Lock()
			terima++
			mu.Unlock()
		}
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	bad := &proto.Envelope{T: proto.TGroupInfo, ID: "x1", From: uidZ,
		G: "g-salah", GN: "Palsu", GO: "bukan-hex", GV: 1, Members: []string{"hacker"}}
	if err := proto.Write(conn, bad); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	n := terima
	mu.Unlock()
	if n != 0 {
		t.Fatalf("grup tidak sah diterima %d kali", n)
	}
}

func TestSafeFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{`..\..\windows\system32\cmd.exe`, "cmd.exe"},
		{`C:\Users\x\rahasia.txt`, "rahasia.txt"},
		{"laporan|final.pdf", "laporan_final.pdf"},
		{"", "file"},
		{".", "file"},
		{"..", "file"},
		{"con.txt", "_con.txt"},
		{"COM1", "_COM1"},
		{"nama.", "nama"},
		{"nama ", "nama"},
		{"\u202ereport.exe", "report.exe"},
		{"​tip.txt", "tip.txt"},
	}
	for _, c := range cases {
		if got := safeFileName(c.in); got != c.want {
			t.Errorf("safeFileName(%q) = %q, ingin %q", c.in, got, c.want)
		}
	}

	// potongan panjang tidak boleh memecah karakter UTF-8 dan ekstensi tetap utuh
	panjang := strings.Repeat("é", 200) + ".laporan.pdf"
	got := safeFileName(panjang)
	if len(got) > maxNameBytes {
		t.Errorf("nama terlalu panjang: %d byte", len(got))
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Errorf("ekstensi hilang: %q", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("karakter UTF-8 rusak: %q", got)
	}

	// titik di tengah nama tidak boleh dianggap ekstensi palsu panjang
	if s := safeFileName("a." + strings.Repeat("x", 40)); len(filepath.Ext(s)) > 17 {
		t.Errorf("ekstensi terlalu panjang: %q", s)
	}
}

func TestPathAmanMenahanKeluarFolder(t *testing.T) {
	dir := t.TempDir()
	p, err := pathAman(dir, "..\\..\\bootstrap.dll")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(dir)) {
		t.Fatalf("hasil keluar folder: %s", p)
	}
}

func TestRiskyExtWarning(t *testing.T) {
	for _, n := range []string{"tagihan.exe", "makro.docm", "pintas.lnk", "skrip.bat"} {
		if !IsRiskyExt(n) {
			t.Errorf("%s seharusnya ditandai berisiko", n)
		}
		if WarningRiskyFile(n) == "" {
			t.Errorf("%s tanpa peringatan", n)
		}
	}
	for _, n := range []string{"laporan.pdf", "foto.jpg", "data.xlsx"} {
		if IsRiskyExt(n) {
			t.Errorf("%s tidak boleh dianggap berisiko", n)
		}
	}
}

// TestChecksumSalahMembuangBerkas: berkas yang isinya tidak cocok dengan
// tanda tangan pengirim tidak boleh dibiarkan tersimpan.
func TestChecksumSalahMembuangBerkas(t *testing.T) {
	b := newLAN(t, 1)[0]
	body := []byte("isi asli yang akan diubah di tengah jalan!!!")
	sum := sha256.Sum256([]byte("isi lain sama sekali"))

	dest := filepath.Join(b.o.Inbox, "ukti.csv")
	done := make(chan Event, 4)
	b.OnEvent(func(e Event) {
		switch e.Kind {
		case EvFileOfferIn:
			if err := b.AcceptFile(e.FID, dest); err != nil {
				t.Errorf("AcceptFile: %v", err)
			}
		case EvFileFailed, EvFileDone:
			done <- e
		}
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)

	fid := "f1"
	if err := proto.Write(conn, &proto.Envelope{T: proto.TOffer, From: uidZ, Nick: "Zubir",
		FID: fid, Name: "ukti.csv", Size: int64(len(body)), Sum: hex.EncodeToString(sum[:])}); err != nil {
		t.Fatal(err)
	}
	// jawaban pertama pada koneksi ini adalah fok (penerima menyetujui);
	// baca sampai fcack untuk chunk yang dikirim.
	time.Sleep(400 * time.Millisecond)
	if err := proto.Write(conn, &proto.Envelope{T: proto.TChunk, FID: fid, Seq: 0, Data: body}); err != nil {
		t.Fatal(err)
	}
	for {
		e, err := proto.Read(br)
		if err != nil {
			t.Fatalf("chunk tidak di-ack: %v", err)
		}
		if e.T == proto.TCAck && e.FID == fid {
			break
		}
	}
	if err := proto.Write(conn, &proto.Envelope{T: proto.TEnd, FID: fid}); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-done:
		if ev.Kind != EvFileFailed {
			t.Fatalf("berkas rusak malah dianggap selesai: %+v", ev)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("berkas rusak tetap tertinggal di folder tujuan")
		}
		if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
			t.Fatal("berkas separuh tidak dibersihkan")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tidak ada hasil transfer")
	}
}

// TestUkuranMelebihiTawaranDitolak: pengirim tidak boleh menulis lebih banyak
// daripada ukuran yang dijanjikan.
func TestUkuranMelebihiTawaranDitolak(t *testing.T) {
	b := newLAN(t, 1)[0]
	done := make(chan Event, 4)
	b.OnEvent(func(e Event) {
		if e.Kind == EvFileOfferIn {
			dest := filepath.Join(b.o.Inbox, "melebihi.bin")
			if err := b.AcceptFile(e.FID, dest); err != nil {
				t.Errorf("AcceptFile: %v", err)
			}
		}
		if e.Kind == EvFileFailed || e.Kind == EvFileDone {
			done <- e
		}
	})

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := proto.Write(conn, &proto.Envelope{T: proto.TOffer, From: uidZ, Nick: "Zubir",
		FID: "f2", Name: "melebihi.bin", Size: 4}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := proto.Write(conn, &proto.Envelope{T: proto.TChunk, FID: "f2", Seq: 0,
		Data: []byte("12345678")}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-done:
		if ev.Kind != EvFileFailed {
			t.Fatalf("kelebihan data malah diterima: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer berlebihan tidak dihentikan")
	}
}

// TestBatalKirimFile: pembatalan menghentikan berkas yang sedang berjalan
// dan tidak menyisakan berkas separuh di penerima.
func TestBatalKirimFile(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)

	dest := filepath.Join(b.o.Inbox, "batal.bin")
	b.OnEvent(func(e Event) {
		if e.Kind == EvFileOfferIn {
			if err := b.AcceptFile(e.FID, dest); err != nil {
				t.Errorf("AcceptFile: %v", err)
			}
		}
	})

	src := filepath.Join(t.TempDir(), "besar.bin")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(24 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ctl := NewSendCtl()
	errc := make(chan error, 1)
	go func() { errc <- a.SendFileCtl(uidB, src, ctl) }()
	time.Sleep(150 * time.Millisecond)
	ctl.Cancel()

	select {
	case err := <-errc:
		if !IsBatal(err) {
			t.Fatalf("hasil batal = %v, ingin ErrBatal", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("pembatalan tidak menghentikan pengiriman")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dest); err == nil {
			t.Fatal("berkas batalan malah tersimpan utuh sebagai final")
		}
		if _, err := os.Stat(dest + ".part"); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("berkas separuh tidak dibuang setelah batal")
	}
}

// TestKirimGrupKeSemuaAnggota: satu pesan grup sampai ke setiap anggota.
func TestKirimGrupKeSemuaAnggota(t *testing.T) {
	lan := newLAN(t, 3)
	a, b, c := lan[0], lan[1], lan[2]
	waitOnline(t, a, uidB)
	waitOnline(t, a, uidC)

	gid := testGroupID(5)
	gc := proto.GroupChange{ID: gid, Name: "Tim Ekspedisi", Owner: uidA, Version: 1,
		Members: []string{uidA, uidB, uidC}}

	type lihat struct{ grup, nama, teks string }
	ch := make(chan lihat, 4)
	for _, h := range []*Hub{b, c} {
		h := h
		h.OnEvent(func(e Event) {
			if e.Kind == EvMsgIn && e.Group == gid {
				ch <- lihat{e.Group, e.GroupName, e.Text}
			}
		})
	}

	okN, gagal, err := a.SendGroupMsg(gc, "", "barang masuk jam 3")
	if err != nil {
		t.Fatalf("SendGroupMsg: %v (gagal %v)", err, gagal)
	}
	if okN != 2 {
		t.Fatalf("terkirim ke %d anggota, ingin 2", okN)
	}
	for i := 0; i < 2; i++ {
		select {
		case l := <-ch:
			if l.grup != gid || l.nama != "Tim Ekspedisi" || l.teks != "barang masuk jam 3" {
				t.Fatalf("pesan grup salah: %+v", l)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ada anggota yang tidak menerima pesan grup")
		}
	}
}

// TestSinkronGrupBaruKeRekan: grup yang dibuat di satu PC harus dikenal di PC
// anggota lainnya tanpa perlu pesan obrolan.
func TestSinkronGrupBaruKeRekan(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)

	gid := testGroupID(7)
	gc := proto.GroupChange{ID: gid, Name: "Admin Gudang", Owner: uidA, Version: 1,
		Members: []string{uidA, uidB}}

	ch := make(chan Event, 4)
	b.OnEvent(func(e Event) {
		if e.Kind == EvGroupInfo && e.Group == gid {
			ch <- e
		}
	})
	if okN, _ := a.BroadcastGroupInfo(gc, []string{uidB}); okN != 1 {
		t.Fatalf("info grup tidak terkirim: %d", okN)
	}
	select {
	case e := <-ch:
		if e.GroupInfo.Name != "Admin Gudang" || len(e.GroupInfo.Members) != 2 {
			t.Fatalf("info grup salah: %+v", e.GroupInfo)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("grup baru tidak pernah sampai ke rekan")
	}
}

// TestUIDPalsuDibuang: identitas dari jaringan harus berformat sah karena
// dipakai sebagai nama berkas riwayat.
func TestUIDPalsuDibuang(t *testing.T) {
	b := newLAN(t, 1)[0]
	var mu sync.Mutex
	terima := 0
	b.OnEvent(func(e Event) {
		if e.Kind == EvMsgIn {
			mu.Lock()
			terima++
			mu.Unlock()
		}
	})
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	untuk := []string{"../../windows", "sangat panjang sekali", "ZZZZ", ""}
	for i, from := range untuk {
		if err := proto.Write(conn, &proto.Envelope{T: proto.TMsg, ID: fmt.Sprintf("p%d", i),
			From: from, To: b.MyUID(), Text: "jahat", TS: time.Now().Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	n := terima
	mu.Unlock()
	if n != 0 {
		t.Fatalf("%d pesan ber-UID palsu masuk", n)
	}
}
