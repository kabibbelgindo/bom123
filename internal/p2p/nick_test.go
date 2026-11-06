package p2p

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"belgindo/internal/proto"
)

// TestNickPropagationMenuLegacy memastikan SetNick tetap propagasi ke peer lain.
func TestNickPropagationMenuLegacy(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	ren := make(chan Event, 4)
	b.OnEvent(func(e Event) {
		if e.Kind == EvPeerRenamed && e.UID == uidA && e.Nick == "Kabib Gudang" {
			ren <- e
		}
	})

	a.SetNick("Kabib Gudang")
	select {
	case e := <-ren:
		t.Logf("OK: peer menerima rename -> %q", e.Nick)
	case <-time.After(4 * time.Second):
		t.Fatal("perubahan nick tidak diterima peer lain")
	}

	got := ""
	for _, p := range b.SnapshotPeers() {
		if p.UID == uidA {
			got = p.Nick
		}
	}
	if got != "Kabib Gudang" {
		t.Fatalf("snapshot nick = %q, ingin %q", got, "Kabib Gudang")
	}
}

// peerNick membaca satu catatan peer dari snapshot hub.
func peerNick(h *Hub, uid string) string {
	for _, p := range h.SnapshotPeers() {
		if p.UID == uid {
			return p.Nick
		}
	}
	return ""
}

// TestPeerAddrsMencakupPeerOnline: pengumuman ulang (mis. sehabis ganti nama)
// harus juga dikirim unicast ke peer yang dikenal, bukan hanya broadcast.
func TestPeerAddrsMencakupPeerOnline(t *testing.T) {
	a := newLAN(t, 2)[0]
	waitOnline(t, a, uidB)
	got := a.peerAddrs()
	if len(got) != 1 {
		t.Fatalf("peerAddrs = %v, ingin satu tujuan unicast", got)
	}
}

// TestStatMsgDisiarkanKePeer: pesan status yang ditulis pengguna harus terlihat
// di PC lain (dipakai daftar rekan: "User1 (pesan status...)").
func TestStatMsgDisiarkanKePeer(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	a.SetStatMsg("verifikasi ukuran")
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range b.SnapshotPeers() {
			if p.UID == uidA && p.StatMsg == "verifikasi ukuran" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("pesan status tidak sampai ke peer lain")
}

// TestNickDariPesanTCP: bila siaran UDP terhalang (firewall / beda subnet),
// nama baru tetap dikenal begitu rekan mengirim pesan chat.
func TestNickDariPesanTCP(t *testing.T) {
	b := newLAN(t, 1)[0]
	b.mu.Lock()
	b.peers[uidZ] = &Peer{UID: uidZ, Nick: "ADMPRD9", PC: "ADMPRD9",
		Online: true, LastSeen: time.Now().Unix()}
	b.mu.Unlock()

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.o.TCPPort), 2*time.Second)
	if err != nil {
		t.Fatalf("dial ke hub: %v", err)
	}
	defer conn.Close()

	if err := proto.Write(conn, &proto.Envelope{T: proto.TMsg, ID: "m1", From: uidZ,
		Nick: "Zubir Gudang", To: b.MyUID(), Text: "halo", TS: time.Now().Unix()}); err != nil {
		t.Fatalf("kirim pesan: %v", err)
	}
	br := bufio.NewReader(conn)
	if e, err := proto.Read(br); err != nil || e.T != proto.TAck {
		t.Fatalf("ack tidak diterima: err=%v envelope=%+v", err, e)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := peerNick(b, uidZ); got == "Zubir Gudang" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nama dari pesan TCP tidak dipakai: %q", peerNick(b, uidZ))
}
