package p2p

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"belgindo/internal/proto"
)

var portSeq int64 = 21000

func nextTwoPorts() (tcp, udp int) {
	tcp = int(portSeq) + 1
	udp = int(portSeq) + 2
	portSeq += 2
	return
}

// UID uji harus berformat sah (16 digit hex): identitas dari jaringan
// dipakai langsung sebagai nama berkas riwayat dan avatar.
func testUID(n int) string { return fmt.Sprintf("%016x", 0xbe00+n) }

var (
	uidA = testUID(0)
	uidB = testUID(1)
	uidC = testUID(2)
	uidZ = testUID(26)
)

// groupID uji dengan format sah: 'g' + 12 hex.
func testGroupID(n int) string { return "g" + fmt.Sprintf("%012x", 0x9c00+n) }

// newLAN membuat n hub uji di loopback yang saling mendengar
// (menggantikan broadcast UDP di lingkungan pengujian).
func newLAN(t *testing.T, n int) []*Hub {
	t.Helper()
	opts := make([]Options, n)
	var udpAddrs []string
	for i := 0; i < n; i++ {
		tcp, udp := nextTwoPorts()
		udpAddrs = append(udpAddrs, fmt.Sprintf("127.0.0.1:%d", udp))
		opts[i] = Options{
			UID: testUID(i), Nick: fmt.Sprintf("User%d", i),
			PC:            fmt.Sprintf("PC-%d", i),
			Inbox:         t.TempDir(),
			TCPPort:       tcp,
			UDPPort:       udp,
			OfflineAfter:  1500 * time.Millisecond,
			AnnounceEvery: 300 * time.Millisecond,
			QuietFor:      10 * time.Millisecond,
			SaveEvery:     time.Hour,
		}
	}
	hubs := make([]*Hub, n)
	for i := 0; i < n; i++ {
		opts[i].AdvertiseAddrs = udpAddrs
		h, err := NewHub(opts[i])
		if err != nil {
			t.Fatalf("NewHub(%d): %v", i, err)
		}
		if err := h.Start(); err != nil {
			t.Fatalf("Start(%d): %v", i, err)
		}
		hubs[i] = h
	}
	t.Cleanup(func() {
		for _, h := range hubs {
			h.Stop()
		}
	})
	return hubs
}

type collector struct {
	mu   sync.Mutex
	evts []Event
}

func (c *collector) events(h *Hub) <-chan Event {
	ch := make(chan Event, 64)
	h.OnEvent(func(e Event) {
		c.mu.Lock()
		c.evts = append(c.evts, e)
		c.mu.Unlock()
		select {
		case ch <- e:
		default:
		}
	})
	return ch
}

func waitOnline(t *testing.T, h *Hub, uid string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if h.PeerOnline(uid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s tidak pernah online di hub %s", uid, h.MyUID())
}

func TestDiscoveryAndChat(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]

	colB := &collector{}
	evB := colB.events(b)

	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	if _, err := a.SendMsg(uidB, "", "Halo Budi!"); err != nil {
		t.Fatalf("SendMsg A->B: %v", err)
	}
	// lewati event lain (mis. online) sampai pesan chat benar-benar tiba.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-evB:
			if e.Kind != EvMsgIn {
				continue
			}
			if e.Text != "Halo Budi!" || e.UID != uidA {
				t.Fatalf("event tidak sesuai: %+v", e)
			}
			goto received
		case <-deadline:
			t.Fatal("pesan tidak diterima B")
		}
	}
received:
	if _, err := b.SendMsg(uidA, "", "Halo juga!"); err != nil {
		t.Fatalf("SendMsg B->A: %v", err)
	}
}

func TestMessageToOfflineFails(t *testing.T) {
	lan := newLAN(t, 1)
	a := lan[0]
	if _, err := a.SendMsg("uid-tidak-ada", "", "halo"); err == nil {
		t.Fatal("harusnya gagal: penerima tidak dikenal")
	}
}

func TestTypingSignal(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	colB := &collector{}
	evB := colB.events(b)
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	a.SendTyping(uidB)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case e := <-evB:
			if e.Kind == EvTypingIn {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("sinyal typing tidak diterima")
}

func TestFileTransfer(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	src := filepath.Join(t.TempDir(), "laporan uji.bin")
	data := make([]byte, 700*1024) // > 1 chunk, paksa multi-roundtrip
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}

	doneIn := make(chan Event, 8)
	b.OnEvent(func(e Event) {
		switch e.Kind {
		case EvFileOfferIn:
			dest := filepath.Join(b.o.Inbox, "diterima.bin")
			if err := b.AcceptFile(e.FID, dest); err != nil {
				t.Errorf("AcceptFile: %v", err)
			}
		case EvFileDone:
			if e.Dir == DirIn {
				doneIn <- e
			}
		}
	})

	go func() {
		if err := a.SendFile(uidB, src); err != nil {
			t.Errorf("SendFile: %v", err)
		}
	}()

	select {
	case e := <-doneIn:
		if e.Size != int64(len(data)) {
			t.Fatalf("ukuran beda: %d vs %d", e.Size, len(data))
		}
		got, err := os.ReadFile(e.Path)
		if err != nil {
			t.Fatalf("baca hasil: %v", err)
		}
		if len(got) != len(data) {
			t.Fatalf("panjang beda: %d vs %d", len(got), len(data))
		}
		for i := range data {
			if got[i] != data[i] {
				t.Fatalf("byte beda di %d", i)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("transfer file tidak selesai")
	}
}

func TestBroadcast(t *testing.T) {
	lan := newLAN(t, 3)
	a, b, c := lan[0], lan[1], lan[2]
	waitOnline(t, a, uidB)
	waitOnline(t, a, uidC)
	waitOnline(t, b, uidA)
	waitOnline(t, c, uidA)

	var mu sync.Mutex
	got := 0
	h := make(chan struct{}, 4)
	for _, x := range []*Hub{b, c} {
		x.OnEvent(func(e Event) {
			if e.Kind == EvMsgIn && e.Text == "rapat jam 2!" {
				mu.Lock()
				got++
				mu.Unlock()
				h <- struct{}{}
			}
		})
	}
	okN, failN := a.SendBroadcast("rapat jam 2!")
	if okN != 2 || failN != 0 {
		t.Fatalf("broadcast: ok=%d fail=%d (ingin 2/0)", okN, failN)
	}
	select {
	case <-h:
	case <-time.After(3 * time.Second):
		t.Fatalf("hanya %d penerima mendapat broadcast", got)
	}
}

func TestByeMakesOffline(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	off := make(chan Event, 4)
	a.OnEvent(func(e Event) {
		if e.Kind == EvPeerOffline && e.UID == uidB {
			off <- e
		}
	})

	b.Stop() // BYE disiarkan
	select {
	case <-off:
	case <-time.After(5 * time.Second):
		t.Fatal("B tidak terdeteksi offline setelah BYE")
	}
	if a.PeerOnline(uidB) {
		t.Fatal("peer masih dianggap online")
	}
}

func TestNickChangePropagates(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	ren := make(chan Event, 4)
	a.OnEvent(func(e Event) {
		if e.Kind == EvPeerRenamed && e.UID == uidB && e.Nick == "Budi S.Kom" {
			ren <- e
		}
	})
	b.SetNick("Budi S.Kom")
	select {
	case <-ren:
	case <-time.After(4 * time.Second):
		t.Fatal("perubahan nama tidak diterima")
	}
}

func TestProtoUDPRoundTrip(t *testing.T) {
	payload := proto.BuildHello("0000beef00000001", "Andi | Test", "PC-1", 51515, proto.StatOnline, -1, "", "di ruang meeting")
	m := proto.ParseUDP(payload)
	if !m.OK || m.Kind != proto.UHello || m.UID != "0000beef00000001" || m.Nick != "Andi Test" || m.TCPPort != 51515 {
		t.Fatalf("parse hello salah: %+v", m)
	}
	if m.Stat != proto.StatOnline || m.AvatarID != -1 || m.AvatarHash != "" {
		t.Fatalf("field status/avatar salah: %+v", m)
	}
	if m.StatMsg != "di ruang meeting" {
		t.Fatalf("pesan status tidak ikut tersiar: %+v", m)
	}
	if m := proto.ParseUDP("payload acak"); m.OK {
		t.Fatal("payload acak tidak boleh valid")
	}
	if m := proto.ParseUDP(proto.BuildTyping("a", "b")); !m.OK || m.Kind != proto.UTyping || m.To != "b" {
		t.Fatalf("parse typing salah: %+v", m)
	}
}

// Pastikan hello versi lama (tanpa field status/avatar) tetap bisa diparse.
func TestProtoHelloBackwardCompat(t *testing.T) {
	legacy := proto.Magic + "|HELLO|uid-lama|Budi|PC-L|51515"
	m := proto.ParseUDP(legacy)
	if !m.OK || m.Nick != "Budi" || m.TCPPort != 51515 {
		t.Fatalf("hello versi lama tidak terparse: %+v", m)
	}
	if m.Stat != proto.StatOnline || m.AvatarID != -1 {
		t.Fatalf("default status/avatar salah: %+v", m)
	}
}

// Ganti status harus langsung terdengar ke PC lain, tidak menunggu jeda.
func TestStatusChangeAnnouncedImmediately(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	colB := &collector{}
	evB := colB.events(b)

	waitOnline(t, b, uidA)
	a.SetStatus(proto.StatAway)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-evB:
			if e.Kind != EvPeerStatus || e.UID != uidA {
				continue
			}
			if p, ok := b.peerOf(uidA); !ok || p.Stat != proto.StatAway {
				t.Fatalf("status rekan tidak tersimpan: %+v", p)
			}
			return
		case <-deadline:
			t.Fatal("pergantian status tidak pernah disiarkan ke B")
		}
	}
}

// Tanda terima baca mengubah centang pesan keluar di PC pengirim.
func TestReadReceipt(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	colA := &collector{}
	evA := colA.events(a)

	waitOnline(t, a, uidB)
	id, err := a.SendMsg(uidB, "", "sedang dikerjakan ya")
	if err != nil {
		t.Fatalf("SendMsg A->B: %v", err)
	}
	b.SendRead(uidA, []string{id})

	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-evA:
			if e.Kind != EvMsgRead || e.UID != uidB {
				continue
			}
			for _, got := range e.IDs {
				if got == id {
					return
				}
			}
			t.Fatalf("id pesan tidak ikut dilaporkan: %+v", e.IDs)
		case <-deadline:
			t.Fatal("tanda terima baca tidak sampai ke A")
		}
	}
}
