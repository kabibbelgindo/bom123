// Package p2p mengelola jaringan peer-to-peer Belgindo Office Messenger:
// kehadiran via UDP broadcast, chat langsung via TCP, dan transfer file.
// Package ini murni logika jaringan (dapat dites di semua OS).
package p2p

import (
	"bufio"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"belgindo/internal/logx"
	"belgindo/internal/netutil"
	"belgindo/internal/proto"
)

// Options konfigurasi hub. Nilai nol = pakai default.
type Options struct {
	UID   string // identitas unik PC ini (hex)
	Nick  string // nama tampilan awal
	PC    string // nama komputer
	Inbox string // folder penyimpanan file diterima

	StatMsg string // pesan status sendiri, disiarkan ke rekan lain

	TCPPort int // 0 = proto.TCPPort
	UDPPort int // 0 = proto.UDPPort

	// Tujuan siaran kehadiran; kosong = broadcast semua subnet lokal.
	AdvertiseAddrs []string

	// Daftar peer lama (dari penyimpanan) untuk ditampilkan sebagai offline.
	SeedPeers []Peer

	// Status kehadiran awal (0..5) dan foto profil sendiri.
	Stat           int    // lihat proto.Stat*; 0 = online
	AvatarID       int    // pilihan avatar bawaan; -1 = otomatis
	AvatarPath     string // PNG foto profil custom milik sendiri (kosong = tidak ada)
	AvatarCacheDir string // folder cache foto profil rekan

	OfflineAfter  time.Duration // default 22s
	AnnounceEvery time.Duration // default 4s
	QuietFor      time.Duration // awal tenang tanpa balon online, default 30s
	SaveEvery     time.Duration // default 30s
}

// Peer adalah satu entri pengguna di jaringan.
type Peer struct {
	UID        string `json:"uid"`
	Nick       string `json:"nick"`
	PC         string `json:"pc"`
	IP         string `json:"ip"`
	TCP        int    `json:"tcp,omitempty"` // port TCP peer (dari hello)
	UDP        int    `json:"udp,omitempty"` // port UDP peer (port sumber hello)
	Online     bool   `json:"online"`
	LastSeen   int64  `json:"last_seen"`   // unix time
	Stat       int    `json:"stat"`        // status kehadiran (0..5)
	AvatarID   int    `json:"avatar_id"`   // avatar bawaan; -1 = otomatis
	AvatarHash string `json:"avatar_hash"` // md5 foto custom; "" = tidak ada
	StatMsg    string `json:"stat_msg,omitempty"`
}

// EventKind jenis peristiwa yang dikirim hub ke antarmuka.
type EventKind int

const (
	EvPeerOnline EventKind = iota
	EvPeerOffline
	EvPeerRenamed
	EvMsgIn
	EvMsgRead // penerima sudah membaca pesan keluar kita
	EvTypingIn
	EvFileOfferIn
	EvFileProgress
	EvFileDone
	EvFileRefused
	EvFileFailed
	EvAvatarUpd // foto profil rekan baru saja diterima & disimpan
	EvGroupInfo // definisi grup datang dari jaringan (global)
)

// EvPeerStatus memakai jalur penyegaran yang sama dengan pergantian nama:
// daftar rekan digambar ulang, tanpa balon notifikasi.
const EvPeerStatus = EvPeerRenamed

// Arah transfer file.
const (
	DirIn  = 0
	DirOut = 1
)

// Event peristiwa jaringan yang dikonsumsi UI.
type Event struct {
	Kind  EventKind
	UID   string
	Nick  string
	PC    string
	ID    string
	Text  string
	TS    int64
	IDs   []string // EvMsgRead: id pesan keluar yang sudah dibaca rekan
	Quiet bool     // true = jangan tampilkan balon (mis. saat baru mulai)

	// transfer file
	FID      string
	FileName string
	Size     int64
	Percent  float64
	Dir      int
	Path     string
	Err      string

	// grup (EvMsgIn dengan Group != "" berarti pesan ruang grup)
	Group      string
	GroupName  string
	GroupOwner string
	GroupInfo  proto.GroupChange
}

// Hub menjalankan seluruh jaringan aplikasi.
type Hub struct {
	o          Options
	nick       string
	stat       int
	statMsg    string // pesan status sendiri (ikut disiarkan)
	avatarID   int
	avatarHash string // md5 foto profil sendiri; "" = tidak ada

	mu    sync.RWMutex
	peers map[string]*Peer

	onEvent func(Event)
	evMu    sync.RWMutex
	onSave  func([]Peer)

	udp   *net.UDPConn
	ln    net.Listener
	quit  chan struct{}
	wg    sync.WaitGroup
	start time.Time

	fmu     sync.Mutex
	rx      map[string]*rxSession
	pending map[string]*pendingOffer

	// dedup pesan masuk: id yang sama dari pengirim yang sama hanya
	// ditampilkan sekali walau rekan mengirim ulang (retry outbox).
	dmu     sync.Mutex
	seenIDs map[string]struct{}
	seenQ   []string

	avMu      sync.Mutex
	avLoading map[string]string    // uid -> hash yang sedang diambil
	avTried   map[string]time.Time // uid -> terakhir kali gagal mengambil
}

// NewHub membuat hub (belum berjalan).
func NewHub(o Options) (*Hub, error) {
	if o.TCPPort == 0 {
		o.TCPPort = proto.TCPPort
	}
	if o.UDPPort == 0 {
		o.UDPPort = proto.UDPPort
	}
	if o.OfflineAfter == 0 {
		o.OfflineAfter = 22 * time.Second
	}
	if o.AnnounceEvery == 0 {
		o.AnnounceEvery = 4 * time.Second
	}
	if o.QuietFor == 0 {
		o.QuietFor = 30 * time.Second
	}
	if o.SaveEvery == 0 {
		o.SaveEvery = 30 * time.Second
	}
	if o.UID == "" {
		o.UID = randHex(8)
	}
	if o.Nick == "" {
		o.Nick = "PC"
	}
	if o.PC == "" {
		o.PC = "PC"
	}
	h := &Hub{
		o:         o,
		nick:      proto.CleanNick(o.Nick),
		stat:      clampStatP(o.Stat),
		statMsg:   truncateText(proto.CleanNick(o.StatMsg), proto.MaxStatMsg),
		avatarID:  o.AvatarID,
		peers:     make(map[string]*Peer),
		quit:      make(chan struct{}),
		rx:        make(map[string]*rxSession),
		pending:   make(map[string]*pendingOffer),
		seenIDs:   make(map[string]struct{}),
		avLoading: make(map[string]string),
		avTried:   make(map[string]time.Time),
	}
	if h.nick == "" {
		h.nick = "PC"
	}
	h.avatarHash = h.computeOwnAvatarHash()
	for _, sp := range o.SeedPeers {
		if sp.UID == "" || sp.UID == o.UID || !proto.ValidUID(sp.UID) {
			continue
		}
		sp.Online = false
		sp.Nick = proto.CleanNick(sp.Nick)
		cp := sp
		h.peers[cp.UID] = &cp
	}
	return h, nil
}

// OnEvent mendaftarkan konsumen peristiwa (UI). Dipanggil dari goroutine
// jaringan — penerima sebaiknya menyalurkan ke thread UI.
func (h *Hub) OnEvent(fn func(Event)) {
	h.evMu.Lock()
	h.onEvent = fn
	h.evMu.Unlock()
}

func (h *Hub) eventFn() func(Event) {
	h.evMu.RLock()
	fn := h.onEvent
	h.evMu.RUnlock()
	return fn
}

// OnSavePeers mendaftarkan hook persistensi daftar peer (dipanggil berkala).
func (h *Hub) OnSavePeers(fn func([]Peer)) { h.onSave = fn }

func (h *Hub) emit(e Event) {
	if fn := h.eventFn(); fn != nil {
		fn(e)
	}
}

// ClampText memotong teks ke batas pesan yang bisa dikirim jaringan.
// Antarmuka wajib menampilkan hasil fungsi ini, bukan teks aslinya, supaya
// gelembung di PC pengirim sama dengan yang sampai ke penerima.
func (h *Hub) ClampText(s string) string { return truncateText(strings.TrimSpace(s), proto.MaxTextLen) }

// MyUID mengembalikan UID PC ini.
func (h *Hub) MyUID() string { return h.o.UID }

// Nick mengembalikan nama tampilan saat ini.
func (h *Hub) Nick() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.nick
}

// SetNick mengganti nama tampilan lalu mengumumkan ke jaringan.
func (h *Hub) SetNick(nick string) {
	nick = strings.TrimSpace(sanitizeNick(nick))
	if nick == "" {
		return
	}
	h.mu.Lock()
	h.nick = nick
	h.mu.Unlock()
	h.AnnounceNow()
}

// StatMsg mengembalikan pesan status yang tertulis pada diri sendiri.
func (h *Hub) StatMsg() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.statMsg
}

// SetStatMsg mengganti pesan status lalu mengumumkan ke jaringan.
// Nilai kosong diperbolehkan (status dihapus).
func (h *Hub) SetStatMsg(msg string) {
	msg = truncateText(strings.TrimSpace(sanitizeNick(msg)), proto.MaxStatMsg)
	h.mu.Lock()
	if msg == h.statMsg {
		h.mu.Unlock()
		return
	}
	h.statMsg = msg
	h.mu.Unlock()
	h.AnnounceNow()
}

// Status mengembalikan status kehadiran saat ini (0..5).
func (h *Hub) Status() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stat
}

// SetStatus mengganti status kehadiran lalu mengumumkan ke jaringan.
func (h *Hub) SetStatus(st int) {
	h.mu.Lock()
	h.stat = clampStatP(st)
	h.mu.Unlock()
	h.AnnounceNow()
}

// AvatarID mengembalikan pilihan avatar bawaan saat ini.
func (h *Hub) AvatarID() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.avatarID
}

// AvatarHash sendiri (md5 foto custom; "" bila tidak pakai foto).
func (h *Hub) OwnAvatarHash() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.avatarHash
}

// ReloadAvatar dipanggil UI setelah foto profil/avatar diubah:
// menghitung ulang hash lalu mengumumkan ke jaringan.
func (h *Hub) ReloadAvatar(avatarPath string, avatarID int) {
	h.mu.Lock()
	h.o.AvatarPath = avatarPath
	h.o.AvatarID = avatarID
	h.avatarID = avatarID
	h.avatarHash = h.computeOwnAvatarHashLocked()
	h.mu.Unlock()
	h.AnnounceNow()
}

// OwnAvatarPNG mengembalikan isi file foto profil sendiri (bila ada).
func (h *Hub) OwnAvatarPNG() []byte {
	h.mu.RLock()
	p := h.o.AvatarPath
	h.mu.RUnlock()
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// computeOwnAvatarHash membaca file foto profil (harus dipanggil dengan lock
// atau sebelum hub dibagikan).
func (h *Hub) computeOwnAvatarHash() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.computeOwnAvatarHashLocked()
}

func (h *Hub) computeOwnAvatarHashLocked() string {
	if h.o.AvatarPath == "" {
		return ""
	}
	b, err := os.ReadFile(h.o.AvatarPath)
	if err != nil || len(b) == 0 {
		return ""
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func clampStatP(st int) int {
	if st < 0 || st > proto.StatMaxValue {
		return proto.StatOnline
	}
	return st
}

// helloPayload menyusun payload HELLO dengan identitas lengkap saat ini.
func (h *Hub) helloPayload() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return proto.BuildHello(h.o.UID, h.nick, h.o.PC, h.o.TCPPort, h.stat, h.avatarID, h.avatarHash, h.statMsg)
}

// Start menjalankan seluruh goroutine jaringan.
func (h *Hub) Start() error {
	h.start = time.Now()

	udpAddr := &net.UDPAddr{IP: net.IPv4zero, Port: h.o.UDPPort}
	udp, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return fmt.Errorf("tidak bisa membuka port UDP %d (dipakai aplikasi lain?): %w", h.o.UDPPort, err)
	}
	_ = udp.SetReadBuffer(1 << 20)
	h.udp = udp

	ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", h.o.TCPPort))
	if err != nil {
		udp.Close()
		return fmt.Errorf("tidak bisa membuka port TCP %d (dipakai aplikasi lain?): %w", h.o.TCPPort, err)
	}
	h.ln = ln

	addrs := h.o.AdvertiseAddrs
	if len(addrs) == 0 {
		addrs = netutil.BroadcastTargets(h.o.UDPPort)
	}
	if len(addrs) == 0 {
		addrs = []string{net.JoinHostPort("255.255.255.255", fmt.Sprint(h.o.UDPPort))}
	}
	h.o.AdvertiseAddrs = addrs

	h.wg.Add(4)
	go h.udpLoop()
	go h.announceLoop()
	go h.acceptLoop()
	go h.maintenanceLoop()
	return nil
}

// Stop menghentikan hub dan memberi tahu tetangga bahwa kita keluar.
func (h *Hub) Stop() {
	select {
	case <-h.quit:
		return
	default:
	}
	close(h.quit)
	// tutup socket & listener agar semua loop berhenti —
	// dijamin tidak ada hello lagi setelah titik ini.
	if h.udp != nil {
		h.udp.Close()
	}
	if h.ln != nil {
		h.ln.Close()
	}
	h.wg.Wait()
	// kirim BYE lewat socket sementara agar pasti menjadi pesan terakhir.
	sendBye(h.o.AdvertiseAddrs, h.o.UID)
	h.fmu.Lock()
	for fid, po := range h.pending {
		po.conn.Close()
		delete(h.pending, fid)
	}
	for fid, rxs := range h.rx {
		rxs.abort()
		delete(h.rx, fid)
	}
	h.fmu.Unlock()
	if h.onSave != nil {
		h.onSave(h.SnapshotPeers())
	}
}

// sendBye menyiarkan pesan pamit lewat socket sementara.
func sendBye(addrs []string, uid string) {
	b := []byte(proto.BuildBye(uid))
	for _, a := range addrs {
		raddr, err := net.ResolveUDPAddr("udp4", a)
		if err != nil {
			continue
		}
		c, err := net.DialUDP("udp4", nil, raddr)
		if err != nil {
			continue
		}
		_, _ = c.Write(b)
		c.Close()
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeNick(s string) string {
	return proto.CleanNick(s)
}

// ---------- UDP: kehadiran ----------

func (h *Hub) udpLoop() {
	defer h.wg.Done()
	defer logx.Recover("udpLoop")
	buf := make([]byte, proto.UDPBufSize)
	for {
		n, raddr, err := h.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-h.quit:
				return
			default:
			}
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				continue
			}
			return
		}
		if raddr == nil || raddr.IP == nil {
			continue
		}
		msg := proto.ParseUDP(string(buf[:n]))
		if !msg.OK || msg.UID == h.o.UID || !proto.ValidUID(msg.UID) {
			continue
		}
		switch msg.Kind {
		case proto.UHello:
			h.onHello(msg, raddr)
		case proto.UBye:
			h.onBye(msg)
		case proto.UQuery:
			// jawab sebentar lagi dengan jitter agar tidak saling menabrak.
			go func(raddr *net.UDPAddr) {
				d := time.Duration(100+time.Now().UnixNano()%250) * time.Millisecond
				select {
				case <-h.quit:
					return
				case <-time.After(d):
				}
				h.sendHelloTo([]string{raddr.String()})
			}(raddr)
		case proto.UTyping:
			if msg.To == h.o.UID {
				nick, pc := h.displayName(msg.UID)
				h.emit(Event{Kind: EvTypingIn, UID: msg.UID, Nick: nick, PC: pc, TS: time.Now().Unix()})
			}
		}
	}
}

func (h *Hub) onHello(msg proto.UDPMsg, raddr *net.UDPAddr) {
	msg.Nick = proto.CleanNick(msg.Nick)
	msg.PC = proto.CleanNick(msg.PC)
	if msg.Nick == "" {
		msg.Nick = "Tanpa Nama"
	}
	ip := raddr.IP.String()
	if ip4 := raddr.IP.To4(); ip4 != nil {
		ip = ip4.String()
	}
	h.mu.Lock()
	old, existed := h.peers[msg.UID]
	quiet := time.Since(h.start) < h.o.QuietFor
	newStatMsg := truncateText(msg.StatMsg, proto.MaxStatMsg)
	kind := EventKind(-1) // -1 = tidak ada perubahan yang perlu dilaporkan
	switch {
	case !existed:
		kind = EvPeerOnline
	case !old.Online:
		kind = EvPeerOnline
	case old.Nick != msg.Nick || old.StatMsg != newStatMsg:
		kind = EvPeerRenamed
	case old.Stat != msg.Stat:
		// ganti status kehadiran (online/away/sibuk...) harus langsung
		// digambar ulang di daftar rekan, tidak menunggu repaint berikutnya.
		kind = EvPeerStatus
	}
	p := &Peer{
		UID:        msg.UID,
		Nick:       msg.Nick,
		PC:         msg.PC,
		IP:         ip,
		TCP:        msg.TCPPort,
		UDP:        raddr.Port,
		Online:     true,
		LastSeen:   time.Now().Unix(),
		Stat:       msg.Stat,
		AvatarID:   msg.AvatarID,
		AvatarHash: msg.AvatarHash,
		StatMsg:    truncateText(msg.StatMsg, proto.MaxStatMsg),
	}
	h.peers[msg.UID] = p
	evUID, evNick, evPC, evTS := p.UID, p.Nick, p.PC, p.LastSeen
	h.mu.Unlock()

	// foto profil rekan berubah / belum ada di cache -> ambil di latar belakang
	if p.AvatarHash != "" {
		go h.maybeFetchAvatar(*p)
	}

	if kind == EvPeerOnline {
		h.emit(Event{Kind: kind, UID: evUID, Nick: evNick, PC: evPC, Quiet: quiet, TS: evTS})
		// balas hello langsung agar pendatang baru cepat melihat kita.
		h.sendHelloTo([]string{raddr.String()})
	} else if kind == EvPeerRenamed || kind == EvPeerStatus {
		h.emit(Event{Kind: kind, UID: evUID, Nick: evNick, PC: evPC, Quiet: quiet, TS: evTS})
	}
}

func (h *Hub) onBye(msg proto.UDPMsg) {
	h.mu.Lock()
	p, ok := h.peers[msg.UID]
	var went Peer
	had := false
	if ok && p.Online {
		p.Online = false
		p.LastSeen = time.Now().Unix()
		went = *p
		had = true
	}
	h.mu.Unlock()
	if had {
		h.emit(Event{Kind: EvPeerOffline, UID: went.UID, Nick: went.Nick, PC: went.PC, TS: went.LastSeen})
	}
}

func (h *Hub) announceLoop() {
	defer h.wg.Done()
	defer logx.Recover("announceLoop")
	// semburan awal agar cepat dikenali jaringan.
	for i := 0; i < 3; i++ {
		h.broadcastUDP(h.helloPayload(), "")
		select {
		case <-h.quit:
			return
		case <-time.After(700 * time.Millisecond):
		}
	}
	t := time.NewTicker(h.o.AnnounceEvery)
	defer t.Stop()
	for {
		select {
		case <-h.quit:
			return
		case <-t.C:
			h.announceAll()
		}
	}
}

func (h *Hub) maintenanceLoop() {
	defer h.wg.Done()
	defer logx.Recover("maintenanceLoop")
	offT := time.NewTicker(5 * time.Second)
	saveT := time.NewTicker(h.o.SaveEvery)
	defer offT.Stop()
	defer saveT.Stop()
	for {
		select {
		case <-h.quit:
			return
		case <-offT.C:
			h.expirePeers()
		case <-saveT.C:
			if h.onSave != nil {
				h.onSave(h.SnapshotPeers())
			}
		}
	}
}

func (h *Hub) expirePeers() {
	now := time.Now().Unix()
	h.mu.Lock()
	var went []Peer
	for _, p := range h.peers {
		if p.Online && now-p.LastSeen > int64(h.o.OfflineAfter.Seconds()) {
			p.Online = false
			went = append(went, *p)
		}
	}
	h.mu.Unlock()
	for _, p := range went {
		h.emit(Event{Kind: EvPeerOffline, UID: p.UID, Nick: p.Nick, PC: p.PC, TS: p.LastSeen})
	}
}

func (h *Hub) broadcastUDP(payload, onlyAddr string) {
	if h.udp == nil {
		return
	}
	targets := h.o.AdvertiseAddrs
	if onlyAddr != "" {
		targets = []string{onlyAddr}
	}
	b := []byte(payload)
	for _, a := range targets {
		raddr, err := net.ResolveUDPAddr("udp4", a)
		if err != nil {
			continue
		}
		if _, err := h.udp.WriteToUDP(b, raddr); err != nil {
			logx.Printf("kirim UDP ke %s: %v", a, err)
		}
	}
}

func (h *Hub) sendHelloTo(addrs []string) {
	h.broadcastUDP(h.helloPayload(), firstOf(addrs))
}

func firstOf(a []string) string {
	if len(a) > 0 {
		return a[0]
	}
	return ""
}

// AnnounceNow menyebarkan kehadiran + pertanyaan pencarian segera.
func (h *Hub) AnnounceNow() {
	go func() {
		h.announceAll()
		h.broadcastUDP(proto.BuildQuery(h.o.UID), "")
	}()
}

// announceAll menyiarkan kehadiran. Selain broadcast ke subnet lokal, HELLO
// dikirim unicast ke setiap peer yang dikenal: tanpa itu, PC di luar subnet
// broadcast (atau yang siaran-nya terblokir firewall) tidak pernah mengetahui
// bahwa nama/status kita sudah berganti.
func (h *Hub) announceAll() {
	hello := h.helloPayload()
	h.broadcastUDP(hello, "")
	for _, addr := range h.peerAddrs() {
		h.broadcastUDP(hello, addr)
	}
}

// peerAddrs alamat UDP peer yang sedang online.
func (h *Hub) peerAddrs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.peers))
	for _, p := range h.peers {
		if p.UID == h.o.UID || !p.Online || p.IP == "" {
			continue
		}
		port := p.UDP
		if port <= 0 {
			port = h.o.UDPPort
		}
		out = append(out, net.JoinHostPort(p.IP, strconv.Itoa(port)))
	}
	return out
}

// ---------- Daftar peer ----------

// SnapshotPeers mengembalikan salinan daftar peer terurut
// (online lebih dulu, lalu nama menaik).
func (h *Hub) SnapshotPeers() []Peer {
	h.mu.RLock()
	out := make([]Peer, 0, len(h.peers))
	for _, p := range h.peers {
		out = append(out, *p)
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return strings.ToLower(out[i].Nick) < strings.ToLower(out[j].Nick)
	})
	return out
}

func (h *Hub) peerOf(uid string) (Peer, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.peers[uid]
	if !ok {
		return Peer{}, false
	}
	return *p, true
}

// PeerOnline melaporkan apakah uid sedang online.
func (h *Hub) PeerOnline(uid string) bool {
	p, ok := h.peerOf(uid)
	return ok && p.Online
}

func (h *Hub) displayName(uid string) (nick, pc string) {
	if p, ok := h.peerOf(uid); ok {
		return p.Nick, p.PC
	}
	return uid, ""
}

// DisplayName mengembalikan nama tampilan & nama PC sebuah UID.
func (h *Hub) DisplayName(uid string) (nick, pc string) { return h.displayName(uid) }

// touchPeer menandai peer hidup saat ada aktivitas TCP langsung.
// noteNick menyegarkan nama rekan dari identitas yang dibawa pesan TCP.
// Berguna bila siaran UDP terhalang (beda subnet / firewall): begitu rekan
// mengirim pesan, nama barunya langsung dikenal.
func (h *Hub) noteNick(uid, nick string) {
	if uid == "" || uid == h.o.UID {
		return
	}
	nick = strings.TrimSpace(sanitizeNick(nick))
	if nick == "" {
		return
	}
	h.mu.Lock()
	p, ok := h.peers[uid]
	if !ok || p.Nick == nick {
		h.mu.Unlock()
		return
	}
	p.Nick = nick
	evPC, evTS := p.PC, p.LastSeen
	h.mu.Unlock()
	h.emit(Event{Kind: EvPeerRenamed, UID: uid, Nick: nick, PC: evPC, Quiet: true, TS: evTS})
}

func (h *Hub) touchPeer(uid string) {
	if uid == h.o.UID {
		return
	}
	h.mu.Lock()
	p, ok := h.peers[uid]
	quiet := time.Since(h.start) < h.o.QuietFor
	if !ok {
		p = &Peer{UID: uid, Online: true, LastSeen: time.Now().Unix()}
		h.peers[uid] = p
	} else {
		p.LastSeen = time.Now().Unix()
	}
	wasOnline := ok && p.Online
	p.Online = true
	nick, pc := p.Nick, p.PC
	h.mu.Unlock()
	if !wasOnline {
		h.emit(Event{Kind: EvPeerOnline, UID: uid, Nick: nick, PC: pc, Quiet: quiet, TS: time.Now().Unix()})
	}
}

// ---------- TCP: menerima koneksi ----------

func (h *Hub) acceptLoop() {
	defer h.wg.Done()
	defer logx.Recover("acceptLoop")
	for {
		conn, err := h.ln.Accept()
		if err != nil {
			select {
			case <-h.quit:
				return
			default:
			}
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				continue
			}
			logx.Printf("accept: %v", err)
			return
		}
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.serveConn(conn)
		}()
	}
}

func (h *Hub) serveConn(conn net.Conn) {
	defer h.dropRxOf(conn)
	defer conn.Close()
	defer logx.Recover("serveConn")
	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	for {
		e, err := proto.Read(br)
		if err != nil {
			return
		}
		if !h.handleInbound(conn, e) {
			return
		}
	}
}

// handleInbound memproses satu pesan dari tetangga. false = tutup koneksi.
func (h *Hub) handleInbound(conn net.Conn, e *proto.Envelope) bool {
	// Identitas jaringan tidak pernah boleh berupa sembarang teks: UID dipakai
	// sebagai nama berkas riwayat/avatar. Pesan dengan pengirim palsu dibuang.
	if e.From != "" && !proto.ValidUID(e.From) {
		logx.Printf("pesan %s dari UID tidak sah (%q) dibuang", e.T, e.From)
		return true
	}
	switch e.T {
	case proto.TPing:
		_ = proto.Write(conn, &proto.Envelope{T: proto.TPong})

	case proto.TMsg:
		if !proto.ValidUID(e.From) {
			return true
		}
		text := truncateText(e.Text, proto.MaxTextLen)
		ts := e.TS
		if ts == 0 {
			ts = time.Now().Unix()
		}
		_ = proto.Write(conn, &proto.Envelope{T: proto.TAck, ID: e.ID})
		// ACK selalu dikirim, tetapi isi pesan hanya ditampilkan sekali:
		// ulang kirim (retry outbox) tidak boleh membuat pesan dobel.
		key := "msg:" + e.From + ":" + e.ID
		if e.ID == "" || h.seenOnce(key) {
			return true
		}
		h.touchPeer(e.From)
		h.noteNick(e.From, e.Nick)
		nick, pc := h.displayName(e.From)
		if e.Nick != "" {
			nick = proto.CleanNick(e.Nick)
		}
		ev := Event{Kind: EvMsgIn, UID: e.From, Nick: nick, PC: pc, ID: e.ID, Text: text, TS: ts}
		if proto.ValidGroupID(e.G) {
			ev.Group = e.G
			ev.GroupName = proto.CleanNick(e.GN)
			ev.GroupOwner = e.GO
			// identitas grup menempel pada pesan: PC yang belum pernah melihat
			// grup ini tetap bisa menampilkannya. Cukup diberitahu sekali per versi.
			meta := proto.GroupChange{ID: e.G, Name: ev.GroupName, Owner: e.GO, Version: e.GV}
			if !h.seenOnce("gmeta:" + groupKey(meta)) {
				h.emit(Event{Kind: EvGroupInfo, Group: e.G, GroupName: ev.GroupName, GroupInfo: meta})
			}
		}
		h.emit(ev)

	case proto.TRead:
		// rekan memberi tahu bahwa pesan-pesan kita sudah dibaca.
		if len(e.IDs) == 0 || !proto.ValidUID(e.From) {
			return true
		}
		h.touchPeer(e.From)
		nick, pc := h.displayName(e.From)
		h.emit(Event{Kind: EvMsgRead, UID: e.From, Nick: nick, PC: pc, IDs: e.IDs})
		return true

	case proto.TGroupInfo:
		gc := proto.GroupChange{ID: e.G, Name: proto.CleanNick(e.GN), Owner: e.GO,
			Version: e.GV, Members: e.Members, Deleted: e.Deleted, TS: e.TS}
		if !gc.Valid() {
			logx.Printf("info grup %s ditolak: identitas/anggota tidak sah", e.G)
			return true
		}
		_ = proto.Write(conn, &proto.Envelope{T: proto.TAck, ID: e.ID})
		if e.ID != "" && h.seenOnce("ginfo:"+e.ID) {
			return true
		}
		h.touchPeer(e.From)
		h.emit(Event{Kind: EvGroupInfo, UID: e.From, Group: gc.ID, GroupName: gc.Name,
			GroupOwner: gc.Owner, GroupInfo: gc})
		return true

	case proto.TOffer:
		if !proto.ValidUID(e.From) || e.Size <= 0 || e.Size > proto.MaxFileSize || e.FID == "" {
			_ = proto.Write(conn, &proto.Envelope{T: proto.TNo, FID: e.FID})
			return true
		}
		h.fmu.Lock()
		grp := ""
		if proto.ValidGroupID(e.G) {
			grp = e.G
		}
		h.pending[e.FID] = &pendingOffer{
			conn:  conn,
			uid:   e.From,
			name:  safeFileName(e.Name),
			size:  e.Size,
			sum:   strings.ToLower(e.Sum),
			group: grp,
			at:    time.Now(),
		}
		h.fmu.Unlock()
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute)) // menunggu jawaban pengguna
		h.noteNick(e.From, e.Nick)
		nick, pc := h.displayName(e.From)
		h.emit(Event{Kind: EvFileOfferIn, UID: e.From, Nick: nick, PC: pc, Group: grp,
			FID: e.FID, FileName: safeFileName(e.Name), Size: e.Size, Dir: DirIn})

	case proto.TChunk:
		h.fmu.Lock()
		rxs := h.rx[e.FID]
		h.fmu.Unlock()
		if rxs == nil {
			_ = proto.Write(conn, &proto.Envelope{T: proto.TNo, FID: e.FID})
			return false
		}
		if err := rxs.writeChunk(e.Seq, e.Data); err != nil {
			_ = proto.Write(conn, &proto.Envelope{T: proto.TNo, FID: e.FID})
			h.failRx(rxs, err.Error())
			return false
		}
		_ = proto.Write(conn, &proto.Envelope{T: proto.TCAck, FID: e.FID, Seq: e.Seq})
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		pct := 0.0
		if rxs.size > 0 {
			pct = float64(rxs.got) / float64(rxs.size) * 100
		}
		rxs.m.Lock()
		last := rxs.lastPct
		rxs.lastPct = pct
		rxs.m.Unlock()
		if pct-last >= 2 || pct >= 100 {
			h.emit(Event{Kind: EvFileProgress, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name,
				Size: rxs.size, Percent: pct, Dir: DirIn})
		}

	case proto.TEnd:
		h.fmu.Lock()
		rxs := h.rx[e.FID]
		delete(h.rx, e.FID)
		h.fmu.Unlock()
		if rxs == nil {
			_ = proto.Write(conn, &proto.Envelope{T: proto.TNo, FID: e.FID})
			return false
		}
		if err := rxs.finish(); err != nil {
			// sesi sudah dihapus dari daftar: laporkan langsung, failRx
			// tidak akan memicu peristiwa lagi.
			rxs.abort()
			logx.Printf("berkas %s dari %s ditolak: %v", rxs.name, rxs.uid, err)
			h.emit(Event{Kind: EvFileFailed, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name,
				Size: rxs.size, Dir: DirIn, Err: err.Error()})
			return false
		}
		_ = proto.Write(conn, &proto.Envelope{T: proto.TEAck, FID: e.FID})
		h.emit(Event{Kind: EvFileDone, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name,
			Size: rxs.size, Path: rxs.path, Dir: DirIn})

	case proto.TAvatarReq:
		// rekan meminta foto profil kita; balas satu envelope lalu tutup.
		_ = proto.Write(conn, &proto.Envelope{T: proto.TAvatar, To: e.From, Data: h.OwnAvatarPNG()})
		return false

	case proto.TNo:
		// penerima menolak saat transfer berjalan (langka) — tandai gagal.
		h.fmu.Lock()
		rxs := h.rx[e.FID]
		delete(h.rx, e.FID)
		h.fmu.Unlock()
		if rxs != nil {
			rxs.abort()
			h.emit(Event{Kind: EvFileRefused, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name, Dir: DirIn})
		}
	}
	return true
}

// ---------- avatar rekan ----------

// cachedAvatarPath path cache foto profil sebuah uid (kosong bila tidak diset).
func (h *Hub) cachedAvatarPath(uid string) string {
	if h.o.AvatarCacheDir == "" || uid == "" {
		return ""
	}
	return filepath.Join(h.o.AvatarCacheDir, uid+".png")
}

// cachedAvatarHash md5 isi file cache foto rekan; "" bila tidak ada.
func (h *Hub) cachedAvatarHash(uid string) string {
	p := h.cachedAvatarPath(uid)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return ""
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// maybeFetchAvatar mengambil foto profil rekan lewat TCP bila hash-nya
// berbeda dengan yang ada di cache. Dipanggil di goroutine terpisah.
func (h *Hub) maybeFetchAvatar(p Peer) {
	if h.cachedAvatarPath(p.UID) == "" || p.TCP <= 0 || p.IP == "" {
		return
	}
	if h.cachedAvatarHash(p.UID) == p.AvatarHash {
		return
	}
	h.avMu.Lock()
	if inflight, ok := h.avLoading[p.UID]; ok && inflight == p.AvatarHash {
		h.avMu.Unlock()
		return // sudah ada yang mengambil versi ini
	}
	if tried, ok := h.avTried[p.UID]; ok && time.Since(tried) < 30*time.Second {
		h.avMu.Unlock()
		return // baru saja gagal; jangan spam
	}
	h.avLoading[p.UID] = p.AvatarHash
	h.avMu.Unlock()

	ok := h.fetchAvatar(p)

	h.avMu.Lock()
	delete(h.avLoading, p.UID)
	if !ok {
		h.avTried[p.UID] = time.Now()
	}
	h.avMu.Unlock()
}

// fetchAvatar membuka koneksi TCP singkat ke rekan dan meminta fotonya.
// true bila foto berhasil diambil dan disimpan ke cache.
func (h *Hub) fetchAvatar(p Peer) bool {
	addr := net.JoinHostPort(p.IP, fmt.Sprint(p.TCP))
	conn, err := net.DialTimeout("tcp4", addr, 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))

	if err := proto.Write(conn, &proto.Envelope{T: proto.TAvatarReq, From: h.o.UID}); err != nil {
		return false
	}
	reply, err := proto.Read(bufio.NewReader(conn))
	if err != nil || reply.T != proto.TAvatar || len(reply.Data) == 0 {
		return false
	}
	// batas ukuran wajar untuk foto profil
	if len(reply.Data) > 2<<20 {
		return false
	}
	dst := h.cachedAvatarPath(p.UID)
	if dst == "" {
		return false
	}
	if err := os.MkdirAll(h.o.AvatarCacheDir, 0o755); err != nil {
		return false
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, reply.Data, 0o644); err != nil {
		return false
	}
	if err := os.Rename(tmp, dst); err != nil {
		return false
	}
	h.emit(Event{Kind: EvAvatarUpd, UID: p.UID, Nick: p.Nick, PC: p.PC})
	return true
}

func (h *Hub) failRx(rxs *rxSession, msg string) {
	h.fmu.Lock()
	_, alive := h.rx[rxs.fid]
	delete(h.rx, rxs.fid)
	h.fmu.Unlock()
	if alive {
		rxs.abort()
		h.emit(Event{Kind: EvFileFailed, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name,
			Dir: DirIn, Err: msg})
	}
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

// seenOnce true bila key ini sudah pernah terlihat. Dipakai untuk membuang
// pesan duplikat dan siaran grup yang berulang.
func (h *Hub) seenOnce(key string) bool {
	h.dmu.Lock()
	defer h.dmu.Unlock()
	if _, ada := h.seenIDs[key]; ada {
		return true
	}
	h.seenIDs[key] = struct{}{}
	h.seenQ = append(h.seenQ, key)
	for len(h.seenQ) > maxSeenIDs {
		old := h.seenQ[0]
		h.seenQ = h.seenQ[1:]
		delete(h.seenIDs, old)
	}
	return false
}

// maxSeenIDs banyaknya id pesan yang diingat untuk pembuangan duplikat.
const maxSeenIDs = 4096

// groupKey adalah tanda tangan isi grup; versi nol dihitung dari isinya agar
// siaran berulang dari pesan grup tidak memicu peristiwa berkali-kali.
func groupKey(g proto.GroupChange) string {
	return fmt.Sprintf("%s|%s|%s|%d|%s", g.ID, g.Name, g.Owner, g.Version, strings.Join(g.Members, ","))
}

// dropRxOf membersihkan transfer masuk yang berjalan di koneksi ini.
// Koneksi putus = transfer batal: berkas separuh dihapus, tidak dibiarkan menggantung.
func (h *Hub) dropRxOf(conn net.Conn) {
	h.fmu.Lock()
	var yatim []*rxSession
	for fid, rxs := range h.rx {
		if rxs.conn == conn {
			delete(h.rx, fid)
			yatim = append(yatim, rxs)
		}
	}
	for fid, po := range h.pending {
		if po.conn == conn {
			delete(h.pending, fid)
		}
	}
	h.fmu.Unlock()
	for _, rxs := range yatim {
		rxs.abort()
		h.emit(Event{Kind: EvFileFailed, UID: rxs.uid, FID: rxs.fid, Group: rxs.group, FileName: rxs.name,
			Dir: DirIn, Err: "koneksi terputus, berkas separuh dibuang"})
	}
}

// safeFileName membuang karakter berbahaya dari nama file.
// Implementasinya ada di files.go (menjaga UTF-8, nama cadangan Windows, dan
// batas panjang tetap utuh).
