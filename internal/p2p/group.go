package p2p

import (
	"bufio"
	"fmt"
	"strings"
	"sync"
	"time"

	"belgindo/internal/logx"
	"belgindo/internal/proto"
)

// writeAsk mengirim satu envelope ke peer dan (opsional) menunggu ACK.
func (h *Hub) writeAsk(uid string, env *proto.Envelope, waitAck bool) error {
	conn, p, err := h.dialPeer(uid, 4*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	env.From = h.o.UID
	if env.Nick == "" {
		env.Nick = h.Nick()
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := proto.Write(conn, env); err != nil {
		return fmt.Errorf("gagal mengirim: %w", err)
	}
	if !waitAck {
		return nil
	}
	br := bufio.NewReader(conn)
	for {
		e, err := proto.Read(br)
		if err != nil {
			return fmt.Errorf("tidak ada konfirmasi dari %s", p.Nick)
		}
		if e.T == proto.TAck && (env.ID == "" || e.ID == env.ID) {
			return nil
		}
		if e.T == proto.TNo {
			return fmt.Errorf("pesan ditolak penerima")
		}
	}
}

// SendGroupMsg mengirim satu pesan ke seluruh anggota grup yang online.
// Semua penerima membawa ID yang sama sehingga gelembung di PC pengirim tetap
// satu; identitas grup ikut dikirim agar PC yang belum mengenal grup ini
// tetap bisa menampilkan pesannya.
func (h *Hub) SendGroupMsg(gc proto.GroupChange, msgID, text string) (okN int, gagal []string, err error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil, fmt.Errorf("pesan kosong")
	}
	if len(text) > proto.MaxTextLen {
		text = truncateText(text, proto.MaxTextLen)
	}
	if !proto.ValidGroupID(gc.ID) {
		return 0, nil, fmt.Errorf("grup tidak dikenali")
	}
	if msgID == "" {
		msgID = randHex(8)
	}
	env := func() *proto.Envelope {
		return &proto.Envelope{T: proto.TMsg, ID: msgID, To: "", Text: text,
			TS: time.Now().Unix(), G: gc.ID, GN: gc.Name, GO: gc.Owner, GV: gc.Version}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, uid := range gc.Members {
		if uid == h.o.UID || !proto.ValidUID(uid) {
			continue
		}
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			defer logx.Recover("SendGroupMsg")
			if e := h.writeAsk(uid, env(), true); e != nil {
				mu.Lock()
				gagal = append(gagal, uid)
				mu.Unlock()
				return
			}
			mu.Lock()
			okN++
			mu.Unlock()
		}(uid)
	}
	wg.Wait()
	if okN == 0 && len(gagal) > 0 {
		err = fmt.Errorf("tidak ada anggota yang menerima pesan")
	}
	return okN, gagal, err
}

// BroadcastGroupInfo menyiarkan definisi grup (nama, anggota, pemilik, versi)
// ke setiap UID pada daftar to. Grup dibuat di satu PC tetapi harus hidup di
// semua PC anggotanya, jadi daftar to sebaiknya berisi seluruh anggota
// termasuk yang baru saja dikeluarkan (agar mereka melihat versi terbarunya).
func (h *Hub) BroadcastGroupInfo(gc proto.GroupChange, to []string) (okN, failN int) {
	if !gc.Valid() {
		return 0, len(to)
	}
	id := randHex(8)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, uid := range to {
		if uid == h.o.UID || !proto.ValidUID(uid) {
			continue
		}
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			defer logx.Recover("BroadcastGroupInfo")
			env := &proto.Envelope{T: proto.TGroupInfo, ID: id, To: uid, TS: gc.TS,
				G: gc.ID, GN: gc.Name, GO: gc.Owner, GV: gc.Version,
				Members: gc.Members, Deleted: gc.Deleted}
			err := h.writeAsk(uid, env, true)
			mu.Lock()
			if err == nil {
				okN++
			} else {
				failN++
			}
			mu.Unlock()
		}(uid)
	}
	wg.Wait()
	return okN, failN
}

// AnnounceGroups menyiarkan seluruh grup milik daftar ke semua rekan online.
// Dipanggil saat PC baru masuk jaringan, supaya pengguna lama langsung melihat
// grup yang dibuat orang lain tanpa harus menunggu pesan berikutnya.
func (h *Hub) AnnounceGroups(groups []proto.GroupChange) {
	for _, gc := range groups {
		if !gc.Valid() {
			continue
		}
		targets := make([]string, 0, len(gc.Members))
		for _, uid := range gc.Members {
			if h.PeerOnline(uid) {
				targets = append(targets, uid)
			}
		}
		if len(targets) == 0 {
			continue
		}
		h.BroadcastGroupInfo(gc, targets)
	}
}
