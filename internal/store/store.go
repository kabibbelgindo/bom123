// Package store mengelola data lokal: konfigurasi, riwayat chat,
// dan daftar kenalan (peer) yang pernah ditemui di jaringan.
// Semua data disimpan di folder data per-pengguna Windows.
package store

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"belgindo/internal/p2p"
)

// Config preferensi aplikasi.
type Config struct {
	UID       string            `json:"uid"`                 // identitas tetap PC ini
	Nick      string            `json:"nick"`                // nama tampilan (default: nama PC)
	Nicknames map[string]string `json:"nicknames,omitempty"` // nama lokal rekan berdasarkan UID
	Sound     bool              `json:"sound"`               // bunyi notifikasi (master)
	Balloon   bool              `json:"balloon"`             // notifikasi balon tray

	SoundMsg     bool `json:"sound_msg"`     // bunyi saat pesan masuk
	SoundOnline  bool `json:"sound_online"`  // bunyi saat rekan online
	SoundOffline bool `json:"sound_offline"` // bunyi saat rekan offline

	Status    int    `json:"status"`     // status kehadiran (0..5), lihat proto.Stat*
	StatusMsg string `json:"status_msg"` // pesan status di bawah nama pada jendela utama
	AvatarID  int    `json:"avatar_id"`  // pilihan avatar bawaan (0..11, -1 = otomatis)

	Language string `json:"language,omitempty"` // kode bahasa antarmuka ("id", "en", "zh")

	SaveDir string `json:"save_dir,omitempty"` // folder kustom penyimpanan file diterima

	AutoAway     bool `json:"auto_away"`      // otomatis ganti status ke Away saat idle
	AutoAwayMins int  `json:"auto_away_mins"` // menit idle sebelum auto away (default 5)

	StartInTray bool `json:"start_in_tray"` // mulai tersembunyi di tray
	HistoryDays int  `json:"history_days"`  // simpan riwayat selama N hari

	// PrefsV2 menandai bahwa pilihan awal sudah pernah ditentukan. Nilai ini
	// tidak lagi mengaktifkan autostart diam-diam; autostart adalah pilihan
	// pengguna di jendela Pengaturan.
	PrefsV2 bool `json:"prefs_v2"`

	// Collapsed daftar kunci bagian yang ditutup di jendela utama
	// ("teman" atau ID grup).
	Collapsed []string `json:"collapsed,omitempty"`

	// LockedChats peta kunci percakapan (UID rekan atau ID grup) -> salt:hash.
	LockedChats map[string]string `json:"locked_chats,omitempty"`
}

// Dir mengembalikan folder data aplikasi
// (%LOCALAPPDATA%\BelgindoMessenger, fallback UserConfigDir).
func Dir() string {
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		return filepath.Join(la, "BelgindoMessenger")
	}
	if ucd, err := os.UserConfigDir(); err == nil {
		return filepath.Join(ucd, "BelgindoMessenger")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "BelgindoMessenger")
}

// LogDir folder berkas log aplikasi.
func LogDir() string { return filepath.Join(Dir(), "logs") }

// InboxFolderName nama folder default file diterima di Documents.
const InboxFolderName = "Belgindo_Office_Messenger"

// DefaultInboxDir mengembalikan folder default file diterima
// (Documents\Belgindo_Office_Messenger).
func DefaultInboxDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(Dir(), "File-Diterima")
	}
	return filepath.Join(home, "Documents", InboxFolderName)
}

// EnsureDir membuat folder bila belum ada.
func EnsureDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("folder kosong")
	}
	return os.MkdirAll(path, 0o755)
}

// EnsureInboxDir membuat folder penyimpanan file diterima bila belum ada.
func EnsureInboxDir(c *Config) (string, error) {
	d := c.InboxDir()
	if err := EnsureDir(d); err != nil {
		return d, err
	}
	return d, nil
}

// InboxDir mengembalikan folder penyimpanan file diterima dari Config (atau default jika kosong).
func (c *Config) InboxDir() string {
	if c != nil && strings.TrimSpace(c.SaveDir) != "" {
		return c.SaveDir
	}
	return DefaultInboxDir()
}

// InboxDir mengembalikan folder default file diterima.
func InboxDir() string {
	return DefaultInboxDir()
}

// OwnAvatarPath path foto profil milik sendiri (hasil upload).
func OwnAvatarPath() string { return filepath.Join(Dir(), "avatar.png") }

// AvatarDir folder cache foto profil rekan.
func AvatarDir() string { return filepath.Join(Dir(), "avatars") }

// MediaDir folder salinan foto yang ditampilkan inline di ruang chat.
func MediaDir() string { return filepath.Join(Dir(), "media") }

func loadJSON(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Editor seperti PowerShell Set-Content -Encoding UTF8 menulis BOM; tanpa
	// dibuang parse gagal dan identitas lama tertimpa nilai baru.
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	return json.Unmarshal(b, v)
}

func saveJSON(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// cfgMu melindungi tulis-menulis konfigurasi dari banyak goroutine.
var cfgMu sync.Mutex

// LoadConfig membaca konfigurasi; membuat default bila belum ada.
// Key yang tidak ada di file lama otomatis memakai nilai default di bawah.
func LoadConfig() *Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c := &Config{
		Sound: true, Balloon: true,
		SoundMsg: true, SoundOnline: true, SoundOffline: true,
		HistoryDays:  90,
		Status:       0,
		AvatarID:     -1,
		AutoAway:     true,
		AutoAwayMins: 5,
		Language:     DefaultLanguage,
	}
	p := filepath.Join(Dir(), "config.json")
	_ = loadJSON(p, c)
	if c.UID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		c.UID = fmt.Sprintf("%x", b)
	}
	if c.AvatarID < -1 || c.AvatarID > 11 {
		c.AvatarID = -1
	}
	if c.Status < 0 || c.Status > 5 {
		c.Status = 0
	}
	if c.AutoAwayMins <= 0 {
		c.AutoAwayMins = 5
	}
	if !IsLanguageCode(c.Language) {
		c.Language = DefaultLanguage
	}
	return c
}

// SaveConfig menyimpan konfigurasi.
// Selamanya memakai satu objek konfigurasi milik aplikasi; komponen lain
// (mis. password percakapan) mengubah objek yang sama, bukan salinan baru,
// supaya kunci tidak hilang tertimpa simpanan berikutnya.
func SaveConfig(c *Config) error {
	if c == nil {
		return fmt.Errorf("konfigurasi kosong")
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return saveJSON(filepath.Join(Dir(), "config.json"), c)
}

// LoadPeers membaca daftar peer lama.
func LoadPeers() []p2p.Peer {
	var ps []p2p.Peer
	_ = loadJSON(filepath.Join(Dir(), "peers.json"), &ps)
	return ps
}

// SavePeers menyimpan daftar peer.
func SavePeers(ps []p2p.Peer) error {
	return saveJSON(filepath.Join(Dir(), "peers.json"), ps)
}

// ---------- kunci identitas ----------

// IsValidUID true bila s adalah 16 karakter heksadesimal (format UID aplikasi).
func IsValidUID(s string) bool { return IsHexByteLen(s, 8) }

// IsHexByteLen true bila s adalah heksadesimal kecil sepanjang 2*n karakter.
func IsHexByteLen(s string, n int) bool {
	if len(s) != n*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// IsValidGroupID true bila s format ID grup buatan aplikasi ("g" + 12 heks).
func IsValidGroupID(s string) bool {
	return len(s) == 13 && s[0] == 'g' && IsHexByteLen(s[1:], 6)
}

// SafeKey mengubah kunci (UID / ID grup) menjadi nama berkas yang aman.
// Kunci resmi bikinan aplikasi dipertahankan apa adanya supaya riwayat lama
// tetap terbaca; nilai lain dari jaringan digantikan ringkasannya sehingga
// tidak bisa menyisipkan pemisah path atau karakter berbahaya.
func SafeKey(s string) string {
	if isPlainToken(s) {
		return s
	}
	sum := sha256.Sum256([]byte("belgindo-key\x00" + s))
	return "k" + hex.EncodeToString(sum[:])[:24]
}

// isPlainToken true bila s hanya berisi huruf, angka, '-' atau '_'
// dan panjangnya wajar.
func isPlainToken(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// CleanName membatasi nama tampilan: karakter kontrol dibuang, spasi dirapikan,
// maksimal 24 rune. Dipakai untuk nick/ps nama yang datang dari jaringan.
func CleanName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\u202e' || r == '\u202d' || r == '\u200e' || r == '\u200f': // RLO/LRO/LRM/RLM
			continue
		case unicode.IsControl(r):
			r = ' '
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > 24 {
		out = string(r[:24]) + "…"
	}
	return out
}

// ---------- grup ----------

// Group adalah kumpulan rekan yang diajak chat bersamaan.
// Grup bersifat global: definisinya disinkronkan antar PC lewat jaringan,
// Version naik setiap kali ada perubahan dan Owner menentukan siapa yang
// berhak menghapus grup.
type Group struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"` // UID rekan
	Owner   string   `json:"owner,omitempty"`
	Version int64    `json:"ver,omitempty"`
	Deleted bool     `json:"del,omitempty"` // batu nisan: grup sudah dihapus
	Updated int64    `json:"upd,omitempty"` // waktu perubahan terakhir (unix)
}

var groupsMu sync.Mutex

// LoadGroups membaca daftar grup (termasuk batu nisan, untuk mencegah grup
// yang sudah hapus muncul kembali dari PC tetangga).
func LoadGroups() []Group {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	var gs []Group
	_ = loadJSON(filepath.Join(Dir(), "groups.json"), &gs)
	return gs
}

// SaveGroups menyimpan daftar grup.
func SaveGroups(gs []Group) error {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	return saveJSON(filepath.Join(Dir(), "groups.json"), gs)
}

// PruneGroupTombstones membuang batu nisan yang lebih tua dari days hari.
func PruneGroupTombstones(gs []Group, days int) []Group {
	if days <= 0 {
		days = 30
	}
	cut := time.Now().AddDate(0, 0, -days).Unix()
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		if g.Deleted && g.Updated < cut {
			continue
		}
		out = append(out, g)
	}
	return out
}

// ---------- log transfer file ----------

// Transfer satu catatan kirim/terima file.
// Dir: "in" atau "out". Status: "berjalan", "selesai", "gagal", "ditolak", "dibatalkan".
type Transfer struct {
	TS     int64   `json:"t"`
	Dir    string  `json:"d"`
	Peer   string  `json:"p"`
	Name   string  `json:"n"`
	Size   int64   `json:"s"`
	Status string  `json:"st"`
	Pct    float64 `json:"pc,omitempty"`
	Path   string  `json:"x,omitempty"`
}

var transfersMu sync.Mutex

// SaveTransfers menulis ulang seluruh catatan transfer.
func SaveTransfers(ts []Transfer) error {
	transfersMu.Lock()
	defer transfersMu.Unlock()
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	for _, t := range ts {
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return writeFileAtomic(filepath.Join(Dir(), "transfers.jsonl"), []byte(sb.String()))
}

// LoadTransfers membaca hingga max catatan transfer terakhir.
func LoadTransfers(max int) []Transfer {
	transfersMu.Lock()
	defer transfersMu.Unlock()
	var all []Transfer
	for _, e := range readLines(filepath.Join(Dir(), "transfers.jsonl")) {
		var t Transfer
		if err := json.Unmarshal(e, &t); err == nil {
			all = append(all, t)
		}
	}
	if max > 0 && len(all) > max {
		all = all[len(all)-max:]
	}
	return all
}

// ---------- riwayat chat ----------

// Entry satu baris riwayat.
// Dir: "in" (masuk), "out" (keluar), "sys" (info sistem).
// Kind: "msg", "file", "img", "ntf".
// State (khusus pesan keluar): "" / "pending" / "sent" / "read".
// Who: nama pengirim (dipakai pada riwayat grup).
type Entry struct {
	TS    int64  `json:"t"`
	Dir   string `json:"d"`
	Kind  string `json:"k"`
	Text  string `json:"x"`
	ID    string `json:"i,omitempty"`
	State string `json:"s,omitempty"`
	Who   string `json:"w,omitempty"`
}

// historyDir folder riwayat percakapan perorangan.
func historyDir() string { return filepath.Join(Dir(), "history") }

// GroupHistoryDir folder riwayat percakapan grup.
func GroupHistoryDir() string { return filepath.Join(Dir(), "group_history") }

// HistoryDir folder tempat riwayat percakapan disimpan.
func HistoryDir() string { return historyDir() }

// histMu melindungi operasi pada berkas riwayat. Tanpa kunci ini, pembaruan
// status baca (yang menulis ulang seluruh berkas) bisa menimpa pesan yang
// baru saja ditambahkan goroutine lain.
var histMu sync.Mutex

func pathIn(dir, key string) string {
	return filepath.Join(dir, SafeKey(key)+".jsonl")
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readLines membaca seluruh baris berkas tanpa batas panjang baris.
// Baris yang terlalu panjang (di atas cap) dibuang agar memori aman.
func readLines(path string) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64*1024)
	const maxLine = 32 << 20
	var out [][]byte
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 && len(line) <= maxLine {
			out = append(out, append([]byte(nil), line...))
		}
		if err != nil {
			break
		}
	}
	return out
}

// appendEntry menambah satu entri riwayat pada folder dan kunci tertentu.
func appendEntry(dir, key string, e Entry) error {
	if key == "" {
		return fmt.Errorf("kunci kosong")
	}
	histMu.Lock()
	defer histMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(pathIn(dir, key), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// AppendHistory menambah satu entri riwayat percakapan dengan peer uid.
func AppendHistory(uid string, e Entry) error { return appendEntry(historyDir(), uid, e) }

// AppendGroupHistory menambah satu entri riwayat grup.
func AppendGroupHistory(gid string, e Entry) error {
	return appendEntry(GroupHistoryDir(), gid, e)
}

// loadEntries membaca hingga max entri terakhir dari satu berkas riwayat.
func loadEntries(dir, key string, max int) []Entry {
	histMu.Lock()
	defer histMu.Unlock()
	var all []Entry
	for _, ln := range readLines(pathIn(dir, key)) {
		var e Entry
		if err := json.Unmarshal(ln, &e); err == nil {
			all = append(all, e)
		}
	}
	if max > 0 && len(all) > max {
		all = all[len(all)-max:]
	}
	return all
}

// LoadHistory membaca hingga max entri terakhir percakapan dengan peer uid.
func LoadHistory(uid string, max int) []Entry { return loadEntries(historyDir(), uid, max) }

// LoadGroupHistory membaca hingga max entri terakhir riwayat grup.
func LoadGroupHistory(gid string, max int) []Entry {
	return loadEntries(GroupHistoryDir(), gid, max)
}

// setHistoryState memperbarui status pesan keluar pada satu berkas riwayat.
func setHistoryState(dir, key string, ids []string, state string) error {
	if key == "" || len(ids) == 0 {
		return nil
	}
	histMu.Lock()
	defer histMu.Unlock()
	path := pathIn(dir, key)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	lines := strings.Split(string(b), "\n")
	changed := false
	out := lines[:0]
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			out = append(out, ln)
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			out = append(out, ln)
			continue
		}
		if e.Dir == "out" && e.ID != "" && want[e.ID] && e.State != state {
			if e.State == "read" && state != "read" {
				out = append(out, ln)
				continue
			}
			e.State = state
			if nb, err := json.Marshal(e); err == nil {
				out = append(out, string(nb))
				changed = true
				continue
			}
		}
		out = append(out, ln)
	}
	if !changed {
		return nil
	}
	return writeFileAtomic(path, []byte(strings.Join(out, "\n")))
}

// SetHistoryState memperbarui state pesan keluar dengan id yang diberikan.
// Hanya id yang cocok dan hanya pesan keluar yang berubah; state tidak boleh
// mundur (read tidak bisa kembali menjadi pending).
func SetHistoryState(uid string, ids []string, state string) error {
	return setHistoryState(historyDir(), uid, ids, state)
}

// SetGroupHistoryState sama seperti SetHistoryState untuk riwayat grup.
func SetGroupHistoryState(gid string, ids []string, state string) error {
	return setHistoryState(GroupHistoryDir(), gid, ids, state)
}

// pruneFile membuang entri yang lebih tua dari batas waktu pada satu berkas.
// Berkas yang menjadi kosong dihapus. true bila berkas masih ada.
func pruneFile(path string, cut int64) bool {
	lines := readLines(path)
	if len(lines) == 0 {
		return false
	}
	kept := lines[:0]
	for _, ln := range lines {
		var e Entry
		if err := json.Unmarshal(ln, &e); err != nil {
			continue
		}
		if e.TS == 0 || e.TS >= cut {
			kept = append(kept, ln)
		}
	}
	if len(kept) == 0 {
		_ = os.Remove(path)
		return false
	}
	if len(kept) != len(lines) {
		var sb strings.Builder
		for _, ln := range kept {
			sb.Write(ln)
			sb.WriteByte('\n')
		}
		_ = writeFileAtomic(path, []byte(sb.String()))
	}
	return true
}

func pruneDir(dir string, days int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cut := time.Now().AddDate(0, 0, -days).Unix()
	for _, en := range ents {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".jsonl") {
			continue
		}
		pruneFile(filepath.Join(dir, en.Name()), cut)
	}
}

// PruneHistory menghapus entri riwayat (perorangan dan grup) yang usianya
// lebih tua dari days hari. Penilaian memakai waktu pesan di dalam berkas,
// bukan waktu ubah berkas, supaya percakapan lama yang baru disentuh tidak
// ikut tersimpan selamanya.
func PruneHistory(days int) {
	if days <= 0 {
		days = 90
	}
	pruneDir(historyDir(), days)
	pruneDir(GroupHistoryDir(), days)
}

// HistoryInfo ringkasan satu percakapan yang punya riwayat.
type HistoryInfo struct {
	UID     string
	Entries int
	Last    time.Time
	Group   bool
}

func listHistory(dir string, group bool) []HistoryInfo {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []HistoryInfo
	for _, en := range ents {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".jsonl") {
			continue
		}
		fi, err := en.Info()
		if err != nil {
			continue
		}
		lines := readLines(filepath.Join(dir, en.Name()))
		last := fi.ModTime()
		for i := len(lines) - 1; i >= 0; i-- {
			var e Entry
			if err := json.Unmarshal(lines[i], &e); err == nil && e.TS > 0 {
				last = time.Unix(e.TS, 0)
				break
			}
		}
		out = append(out, HistoryInfo{
			UID:     strings.TrimSuffix(en.Name(), ".jsonl"),
			Entries: len(lines),
			Last:    last,
			Group:   group,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// ListHistory mendaftarkan percakapan perorangan berriwayat, terbaru lebih dulu.
func ListHistory() []HistoryInfo { return listHistory(historyDir(), false) }

// ListGroupHistory mendaftarkan riwayat grup, terbaru lebih dulu.
func ListGroupHistory() []HistoryInfo { return listHistory(GroupHistoryDir(), true) }

// clearFile menghapus satu berkas riwayat.
func clearFile(dir, key string) error {
	if key == "" {
		return fmt.Errorf("kunci kosong")
	}
	histMu.Lock()
	defer histMu.Unlock()
	if err := os.Remove(pathIn(dir, key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ClearHistory menghapus riwayat satu percakapan perorangan secara permanen.
func ClearHistory(uid string) error { return clearFile(historyDir(), uid) }

// ClearGroupHistory menghapus riwayat satu grup secara permanen.
func ClearGroupHistory(gid string) error { return clearFile(GroupHistoryDir(), gid) }

// ClearAllHistory menghapus seluruh riwayat (perorangan dan grup).
func ClearAllHistory() error {
	histMu.Lock()
	defer histMu.Unlock()
	var firstErr error
	for _, dir := range []string{historyDir(), GroupHistoryDir()} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, en := range ents {
			if en.IsDir() {
				continue
			}
			if err := os.Remove(filepath.Join(dir, en.Name())); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// PeerName aman dipakai untuk pemanggilan cepat dari main.
func PeerName(p p2p.Peer) string {
	n := CleanName(p.Nick)
	if n == "" {
		return SafeKey(p.UID)
	}
	return n
}

// ---------- kunci percakapan ----------
// Semua fungsi di bawah mengubah objek konfigurasi milik aplikasi (bukan
// salinan hasil LoadConfig) lalu menyimpannya, sehingga perubahan lain pada
// konfigurasi tidak ikut tertimpa.

// SetChatPassword menyimpan password percakapan dalam bentuk hash bersalt.
func SetChatPassword(c *Config, key, password string) error {
	if c == nil || key == "" {
		return fmt.Errorf("kunci percakapan tidak valid")
	}
	if len([]rune(password)) < 3 {
		return fmt.Errorf("password minimal 3 karakter")
	}
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return err
	}
	salt := fmt.Sprintf("%x", saltBytes)
	sum := chatPasswordHash(salt, password)
	if c.LockedChats == nil {
		c.LockedChats = make(map[string]string)
	}
	c.LockedChats[SafeKey(key)] = salt + ":" + fmt.Sprintf("%x", sum)
	return SaveConfig(c)
}

// RemoveChatPassword membuka kunci percakapan (password benar diperlukan).
func RemoveChatPassword(c *Config, key string) error {
	if c == nil || c.LockedChats == nil {
		return nil
	}
	delete(c.LockedChats, SafeKey(key))
	return SaveConfig(c)
}

// IsChatLocked memeriksa status kunci percakapan pada konfigurasi aktif.
func IsChatLocked(c *Config, key string) bool {
	if c == nil || c.LockedChats == nil || key == "" {
		return false
	}
	return c.LockedChats[SafeKey(key)] != ""
}

// VerifyChatPassword memeriksa password tanpa menyimpan password asli.
func VerifyChatPassword(c *Config, key, password string) bool {
	if c == nil || c.LockedChats == nil || key == "" {
		return false
	}
	stored := c.LockedChats[SafeKey(key)]
	if stored == "" {
		return false
	}
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	sum := chatPasswordHash(parts[0], password)
	expected := fmt.Sprintf("%x", sum)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) == 1
}

// chatPasswordHash menggunakan iterasi HMAC-SHA256 untuk memperlambat
// percobaan password offline terhadap file konfigurasi.
func chatPasswordHash(salt, password string) []byte {
	key := []byte(password)
	message := append([]byte(salt), 0, 0, 0, 1)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	u := mac.Sum(nil)
	result := append([]byte(nil), u...)
	for i := 1; i < 25000; i++ {
		mac = hmac.New(sha256.New, key)
		_, _ = mac.Write(u)
		u = mac.Sum(nil)
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}
