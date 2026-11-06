package p2p

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"belgindo/internal/logx"
	"belgindo/internal/proto"
)

// pendingOffer adalah tawaran file masuk yang menunggu jawaban pengguna.
type pendingOffer struct {
	conn  net.Conn
	uid   string
	name  string
	size  int64
	sum   string // sha256 hex dari pengirim; "" = pengirim versi lama
	group string // ID ruang grup tujuan bila file dikirim ke grup
	at    time.Time
}

// rxSession transfer file masuk yang sedang berjalan.
type rxSession struct {
	fid, uid   string
	name, path string
	size       int64
	sum        string
	group      string // ID ruang grup tujuan bila file datang dari grup
	got        int64
	f          *os.File
	h          hash.Hash
	conn       net.Conn
	lastPct    float64
	m          sync.Mutex
}

func (r *rxSession) writeChunk(seq int64, data []byte) error {
	if r.f == nil {
		return fmt.Errorf("transfer sudah selesai")
	}
	// Batas ukuran dijaga di sini: pengirim nakal atau chunk ganda tidak boleh
	// menulis melewati ukuran yang diumumkan di tawaran.
	if r.size > 0 && r.got+int64(len(data)) > r.size {
		return fmt.Errorf("data melebihi ukuran yang dijanjikan")
	}
	if _, err := r.f.Write(data); err != nil {
		return err
	}
	if r.h != nil {
		_, _ = r.h.Write(data)
	}
	r.got += int64(len(data))
	return nil
}

func (r *rxSession) finish() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	if err != nil {
		return err
	}
	batal := func(msg string) error {
		_ = os.Remove(r.path + ".part")
		return errors.New(msg)
	}
	if r.got != r.size {
		return batal(fmt.Sprintf("ukuran berkas tidak sama: diterima %d, dijanjikan %d", r.got, r.size))
	}
	if r.sum != "" && r.h != nil && hex.EncodeToString(r.h.Sum(nil)) != r.sum {
		return batal("isi berkas rusak saat transfer (tanda tangan tidak cocok)")
	}
	// pindahkan .part ke nama final
	return os.Rename(r.path+".part", r.path)
}

func (r *rxSession) abort() {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	_ = os.Remove(r.path + ".part")
}

// ---------- kirim pesan ----------

// dialPeer membuka koneksi TCP ke peer (port TCP peer disimpan dari hello).
func (h *Hub) dialPeer(uid string, timeout time.Duration) (net.Conn, Peer, error) {
	p, ok := h.peerOf(uid)
	if !ok {
		return nil, p, fmt.Errorf("pengguna belum dikenal jaringan")
	}
	if p.IP == "" {
		return nil, p, fmt.Errorf("alamat %s belum diketahui (tunggu sebentar)", p.Nick)
	}
	if !p.Online {
		return nil, p, fmt.Errorf("%s sedang offline", p.Nick)
	}
	port := p.TCP
	if port == 0 {
		port = h.o.TCPPort
	}
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort(p.IP, fmt.Sprint(port)), timeout)
	if err != nil {
		var evOff Event
		h.mu.Lock()
		if pp, ok := h.peers[uid]; ok && pp.Online {
			pp.Online = false
			pp.LastSeen = time.Now().Unix()
			evOff = Event{Kind: EvPeerOffline, UID: pp.UID, Nick: pp.Nick, PC: pp.PC, TS: pp.LastSeen}
		}
		h.mu.Unlock()
		if evOff.UID != "" {
			h.emit(evOff)
		}
		return nil, p, fmt.Errorf("%s tidak dapat dihubungi (offline / firewall)", p.Nick)
	}
	return conn, p, nil
}

// SendMsg mengirim pesan chat langsung ke peer dan menunggu konfirmasi.
// msgID dipakai untuk menandai balon di antarmuka; kosong = id dibuat di sini.
func (h *Hub) SendMsg(uid, msgID, text string) (id string, err error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("pesan kosong")
	}
	if len(text) > proto.MaxTextLen {
		text = truncateText(text, proto.MaxTextLen)
	}
	conn, p, err := h.dialPeer(uid, 4*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	id = msgID
	if id == "" {
		id = randHex(8)
	}
	msg := &proto.Envelope{T: proto.TMsg, ID: id, From: h.o.UID, Nick: h.nick,
		To: uid, Text: text, TS: time.Now().Unix()}
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	if err := proto.Write(conn, msg); err != nil {
		return "", fmt.Errorf("gagal mengirim: %w", err)
	}
	br := bufio.NewReader(conn)
	for {
		e, err := proto.Read(br)
		if err != nil {
			return "", fmt.Errorf("tidak ada konfirmasi dari %s", p.Nick)
		}
		if e.T == proto.TAck && e.ID == id {
			return id, nil
		}
		if e.T == proto.TNo {
			return "", fmt.Errorf("pesan ditolak penerima")
		}
	}
}

// SendBroadcast mengirim pesan ke semua pengguna yang online.
// Mengembalikan jumlah yang berhasil dan gagal.
func (h *Hub) SendBroadcast(text string) (okN, failN int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, 0
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, p := range h.SnapshotPeers() {
		if !p.Online || p.UID == h.o.UID {
			continue
		}
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			if _, err := h.SendMsg(uid, "", text); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			} else {
				mu.Lock()
				failN++
				mu.Unlock()
			}
		}(p.UID)
	}
	wg.Wait()
	return okN, failN
}

// SendTyping mengirim sinyal "sedang mengetik" (UDP unicast, tanpa konfirmasi).
func (h *Hub) SendTyping(uid string) {
	p, ok := h.peerOf(uid)
	if !ok || !p.Online || p.IP == "" {
		return
	}
	port := p.UDP
	if port == 0 {
		port = h.o.UDPPort
	}
	raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(p.IP, fmt.Sprint(port)))
	if err != nil {
		return
	}
	h.udp.WriteToUDP([]byte(proto.BuildTyping(h.o.UID, uid)), raddr)
}

// SendRead memberitahu rekan bahwa pesan-pesan darinya sudah kita baca.
// Tanpa konfirmasi: bila gagal, tanda centang di PC lawan tetap abu-abu.
func (h *Hub) SendRead(uid string, ids []string) {
	ids = cleanIDs(ids)
	if len(ids) == 0 {
		return
	}
	conn, _, err := h.dialPeer(uid, 3*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_ = proto.Write(conn, &proto.Envelope{T: proto.TRead, From: h.o.UID, Nick: h.nick,
		To: uid, IDs: ids, TS: time.Now().Unix()})
}

func cleanIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > 100 {
		out = out[len(out)-100:]
	}
	return out
}

// ---------- kirim file ----------

// SendCtl mengendalikan pembatalan satu pengiriman berkas.
// Batal menghentikan berkas yang sedang berjalan; berkas yang sudah selesai
// tetap terkirim (sama seperti membatalkan salinan di Windows).
type SendCtl struct {
	once sync.Once
	done chan struct{}
}

// NewSendCtl membuat pengendali pembatalan.
func NewSendCtl() *SendCtl { return &SendCtl{done: make(chan struct{})} }

// Cancel meminta pengiriman berhenti. Aman dipanggil berkali-kali.
func (c *SendCtl) Cancel() {
	if c == nil {
		return
	}
	c.once.Do(func() { close(c.done) })
}

// Done tertutup bila pembatalan diminta.
func (c *SendCtl) Done() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.done
}

// Stopped melaporkan apakah pembatalan sudah diminta.
func (c *SendCtl) Stopped() bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// ErrBatal adalah kesalahan yang dikembalikan pengiriman yang dibatalkan.
var ErrBatal = errors.New("pengiriman dibatalkan")

// IsBatal true bila err berasal dari pembatalan pengguna.
func IsBatal(err error) bool { return errors.Is(err, ErrBatal) }

// SendFile mengirim file ke peer (blokir sampai selesai/gagal).
// Alur: foffer -> (penerima setuju) fok -> fchunk* (dengan fcack) -> fend -> feack.
func (h *Hub) SendFile(uid, path string) error {
	return h.SendFileCtl(uid, path, nil)
}

// SendFileCtl mengirim satu berkas ke peer; ctl boleh nil.
// Blokir sampai selesai, gagal, atau dibatalkan.
func (h *Hub) SendFileCtl(uid, path string, ctl *SendCtl) error {
	return h.sendFileCtl(uid, path, "", ctl)
}

// SendFileGroupCtl mengirim satu berkas ke anggota grup; group dipakai agar
// penerima menaruh berkas di ruang grup yang benar.
func (h *Hub) SendFileGroupCtl(uid, path, group string, ctl *SendCtl) error {
	return h.sendFileCtl(uid, path, group, ctl)
}

func (h *Hub) sendFileCtl(uid, path, group string, ctl *SendCtl) error {
	p, ok := h.peerOf(uid)
	if !ok {
		return fmt.Errorf("pengguna belum dikenal jaringan")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file tidak dapat dibaca: %w", err)
	}
	if fi.IsDir() {
		return fmt.Errorf("folder tidak bisa dikirim sebagai satu berkas")
	}
	if fi.Size() > proto.MaxFileSize {
		return fmt.Errorf("file terlalu besar (maksimum %d MB)", proto.MaxFileSize>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("file tidak dapat dibuka: %w", err)
	}
	defer f.Close()

	conn, _, err := h.dialPeer(uid, 4*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	fid := randHex(8)
	name := safeFileName(filepath.Base(path))
	emitP := func(kind EventKind, pct float64, msg string) {
		h.emit(Event{Kind: kind, UID: uid, Nick: p.Nick, PC: p.PC, FID: fid, Group: group,
			FileName: name, Size: fi.Size(), Percent: pct, Dir: DirOut, Path: path, Err: msg})
	}
	if ctl.Stopped() {
		emitP(EvFileRefused, 0, "dibatalkan")
		return ErrBatal
	}

	// tanda tangan dihitung streaming: berkas besar tidak dimuat ke memori.
	sumw := sha256.New()
	if fi.Size() > 0 {
		if _, err := io.Copy(sumw, f); err != nil {
			return fmt.Errorf("file tidak dapat dibaca: %w", err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("file tidak dapat dibaca: %w", err)
		}
	}
	sum := hex.EncodeToString(sumw.Sum(nil))

	env := &proto.Envelope{T: proto.TOffer, From: h.o.UID, Nick: h.nick,
		FID: fid, Name: name, Size: fi.Size(), Sum: sum, G: group}
	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	if err := proto.Write(conn, env); err != nil {
		emitP(EvFileFailed, 0, "gagal mengirim tawaran")
		return fmt.Errorf("gagal mengirim tawaran: %w", err)
	}

	// tunggu jawaban penerima
	br := bufio.NewReader(conn)
	accepted := false
	for !accepted {
		if ctl.Stopped() {
			emitP(EvFileRefused, 0, "dibatalkan")
			return ErrBatal
		}
		e, err := proto.Read(br)
		if err != nil {
			emitP(EvFileFailed, 0, "tidak ada jawaban dari penerima")
			return fmt.Errorf("tidak ada jawaban dari %s", p.Nick)
		}
		switch {
		case e.T == proto.TOk && e.FID == fid:
			accepted = true
		case e.T == proto.TNo && e.FID == fid:
			emitP(EvFileRefused, 0, "")
			return fmt.Errorf("%s menolak file", p.Nick)
		}
	}

	// kirim potongan dengan stop-and-wait
	buf := make([]byte, proto.ChunkSize)
	sent := int64(0)
	seq := int64(0)
	lastPct := -100.0
	for {
		if ctl.Stopped() {
			// beri tahu penerima agar membuang berkas separuh, lalu berhenti.
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			_ = proto.Write(conn, &proto.Envelope{T: proto.TNo, FID: fid})
			logx.Printf("kirim %s ke %s dibatalkan pada %d%%", name, p.Nick, int(pctOf(sent, fi.Size())))
			emitP(EvFileFailed, pctOf(sent, fi.Size()), "pengiriman dibatalkan")
			return ErrBatal
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			if err := proto.Write(conn, &proto.Envelope{T: proto.TChunk, FID: fid, Seq: seq, Data: buf[:n]}); err != nil {
				emitP(EvFileFailed, pctOf(sent, fi.Size()), "koneksi terputus")
				return fmt.Errorf("gagal mengirim data: %w", err)
			}
			acked := false
			for !acked {
				e, err := proto.Read(br)
				if err != nil {
					emitP(EvFileFailed, pctOf(sent, fi.Size()), "koneksi terputus")
					return fmt.Errorf("konfirmasi terputus: %w", err)
				}
				switch {
				case e.T == proto.TCAck && e.FID == fid && e.Seq == seq:
					acked = true
				case e.T == proto.TNo && e.FID == fid:
					emitP(EvFileFailed, pctOf(sent, fi.Size()), "penerima membatalkan")
					return fmt.Errorf("transfer dibatalkan oleh %s", p.Nick)
				}
			}
			sent += int64(n)
			seq++
			pct := pctOf(sent, fi.Size())
			if pct-lastPct >= 2 || sent >= fi.Size() {
				lastPct = pct
				emitP(EvFileProgress, pct, "")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			emitP(EvFileFailed, pctOf(sent, fi.Size()), "gagal membaca file")
			return fmt.Errorf("gagal membaca file: %w", rerr)
		}
	}

	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := proto.Write(conn, &proto.Envelope{T: proto.TEnd, FID: fid}); err != nil {
		emitP(EvFileFailed, 100, "koneksi terputus di akhir")
		return fmt.Errorf("gagal menutup transfer: %w", err)
	}
	for {
		e, err := proto.Read(br)
		if err != nil {
			emitP(EvFileFailed, 100, "tidak ada konfirmasi akhir")
			return fmt.Errorf("tidak ada konfirmasi akhir dari %s", p.Nick)
		}
		if e.T == proto.TEAck && e.FID == fid {
			break
		}
		if e.T == proto.TNo && e.FID == fid {
			emitP(EvFileFailed, 100, "berkas ditolak saat penyimpanan")
			return fmt.Errorf("%s gagal menyimpan berkas", p.Nick)
		}
	}
	emitP(EvFileDone, 100, "")
	return nil
}

// AcceptFile menyetujui tawaran file masuk; data akan disimpan ke destPath.
func (h *Hub) AcceptFile(fid, destPath string) error {
	h.fmu.Lock()
	po := h.pending[fid]
	delete(h.pending, fid)
	h.fmu.Unlock()
	if po == nil {
		return fmt.Errorf("tawaran file sudah kedaluwarsa")
	}
	batal := func(err error) error {
		_ = proto.Write(po.conn, &proto.Envelope{T: proto.TNo, FID: fid})
		_ = po.conn.Close()
		return err
	}
	dest := filepath.Clean(destPath)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return batal(fmt.Errorf("tidak bisa menyiapkan folder: %w", err))
	}
	if po.size > proto.MaxFileSize {
		return batal(fmt.Errorf("file terlalu besar (maksimum %d MB)", proto.MaxFileSize>>20))
	}
	if err := checkDiskSpace(filepath.Dir(dest), po.size); err != nil {
		return batal(err)
	}
	f, err := os.Create(dest + ".part")
	if err != nil {
		return batal(fmt.Errorf("tidak bisa membuat file: %w", err))
	}
	rxs := &rxSession{
		fid: fid, uid: po.uid, name: po.name, path: dest,
		size: po.size, sum: po.sum, group: po.group, f: f, conn: po.conn,
	}
	if hexSha256(po.sum) {
		rxs.h = sha256.New()
	}
	h.fmu.Lock()
	h.rx[fid] = rxs
	h.fmu.Unlock()
	po.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	if err := proto.Write(po.conn, &proto.Envelope{T: proto.TOk, FID: fid}); err != nil {
		h.fmu.Lock()
		delete(h.rx, fid)
		h.fmu.Unlock()
		rxs.abort()
		return fmt.Errorf("gagal menyetujui: %w", err)
	}
	return nil
}

// RejectFile menolak tawaran file masuk.
func (h *Hub) RejectFile(fid string) {
	h.fmu.Lock()
	po := h.pending[fid]
	delete(h.pending, fid)
	h.fmu.Unlock()
	if po == nil {
		return
	}
	_ = proto.Write(po.conn, &proto.Envelope{T: proto.TNo, FID: fid})
	_ = po.conn.Close()
}

// CancelReceive menghentikan unduhan yang sedang berjalan.
// Berkas separuh dibuang, pengirim diberi tahu.
func (h *Hub) CancelReceive(fid string) bool {
	h.fmu.Lock()
	rxs := h.rx[fid]
	delete(h.rx, fid)
	h.fmu.Unlock()
	if rxs == nil {
		return false
	}
	if rxs.conn != nil {
		_ = rxs.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_ = proto.Write(rxs.conn, &proto.Envelope{T: proto.TNo, FID: fid})
		_ = rxs.conn.Close()
	}
	rxs.abort()
	h.emit(Event{Kind: EvFileFailed, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name,
		Dir: DirIn, Err: "penerima membatalkan"})
	return true
}

func pctOf(a, b int64) float64 {
	if b <= 0 {
		return 100
	}
	p := float64(a) / float64(b) * 100
	if p > 100 {
		p = 100
	}
	return p
}

// UniquePath mengembalikan path yang belum dipakai:
// "nama.pdf" -> "nama (2).pdf" dst. Nama selalu dibersihkan lebih dulu
// sehingga hasilnya dijamin tetap berada di dalam folder dir.
func UniquePath(dir, name string) string {
	aman, err := pathAman(dir, name)
	if err != nil {
		aman = filepath.Join(dir, "file")
	}
	name = filepath.Base(aman)
	full := filepath.Join(dir, name)
	if _, err := os.Stat(full); os.IsNotExist(err) {
		return full
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", base, randHex(3), ext))
}
