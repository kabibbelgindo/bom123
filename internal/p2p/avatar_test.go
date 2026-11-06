package p2p

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"belgindo/internal/proto"
)

// TestStatusPropagates memastikan perubahan status kehadiran tersiarkan
// dan tersimpan pada peer lain.
func TestStatusPropagates(t *testing.T) {
	lan := newLAN(t, 2)
	a, b := lan[0], lan[1]
	waitOnline(t, a, uidB)
	waitOnline(t, b, uidA)

	a.SetStatus(proto.StatBusy)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range b.SnapshotPeers() {
			if p.UID == uidA {
				if p.Online && p.Stat == proto.StatBusy {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("peer tidak melihat perubahan status")
}

// TestAvatarExchange memastikan foto profil dikirim saat diminta rekan
// dan disimpan di folder cache.
func TestAvatarExchange(t *testing.T) {
	avDirA := t.TempDir()
	avDirB := t.TempDir()

	opts := make([]Options, 2)
	var udpAddrs []string
	for i := 0; i < 2; i++ {
		tcp, udp := nextTwoPorts()
		udpAddrs = append(udpAddrs, "127.0.0.1:"+itoa(udp))
		opts[i] = Options{
			UID: testUID(100 + i), Nick: "Avatar" + itoa(i), PC: "PC-AV-" + itoa(i),
			Inbox:         t.TempDir(),
			TCPPort:       tcp,
			UDPPort:       udp,
			OfflineAfter:  1500 * time.Millisecond,
			AnnounceEvery: 300 * time.Millisecond,
			QuietFor:      10 * time.Millisecond,
			SaveEvery:     time.Hour,
		}
	}
	for i := 0; i < 2; i++ {
		opts[i].AdvertiseAddrs = udpAddrs
	}
	// A punya foto profil; B tidak. Folder cache B mengarah ke avDirB.
	png := []byte("\x89PNG fake-avatar-bytes-for-hash-test")
	photo := filepath.Join(t.TempDir(), "avatar.png")
	if err := os.WriteFile(photo, png, 0o644); err != nil {
		t.Fatal(err)
	}
	opts[0].AvatarPath = photo
	opts[0].AvatarCacheDir = avDirA
	opts[1].AvatarCacheDir = avDirB

	a, err := NewHub(opts[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewHub(opts[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Stop(); b.Stop() })

	if a.OwnAvatarHash() == "" {
		t.Fatal("hash foto A kosong padahal foto ada")
	}
	waitOnline(t, a, b.MyUID())
	waitOnline(t, b, a.MyUID())

	// B harus mengambil foto A (hash beda dari cache kosong).
	cacheB := filepath.Join(avDirB, a.MyUID()+".png")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := os.ReadFile(cacheB); err == nil && len(got) == len(png) {
			return // sukses
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("foto profil A tidak pernah sampai ke cache B")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
