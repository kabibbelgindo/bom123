// Package proto mendefinisikan format pesan jaringan Belgindo Office Messenger.
//
// Arsitektur: peer-to-peer murni tanpa server.
//   - Kehadiran (online/offline) disiarkan lewat UDP broadcast.
//   - Pesan chat, typing, dan transfer file berjalan lewat koneksi TCP
//     langsung antara dua PC (satu baris JSON per pesan).
package proto

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Port tetap yang dipakai di seluruh jaringan (tidak perlu dikonfigurasi).
const (
	TCPPort = 51515 // pesan chat + transfer file (TCP langsung antar PC)
	UDPPort = 51516 // siaran kehadiran (discovery broadcast)
)

// Batas aman payload.
const (
	MaxTextLen  = 8000      // maksimum karakter per pesan chat
	MaxFileSize = 500 << 20 // maksimum ukuran file (500 MB)
	ChunkSize   = 96 * 1024 // ukuran chunk transfer file (sebelum base64)
	MaxLineLen  = 8 << 20   // batas panjang satu baris JSON
	UDPBufSize  = 4096      // buffer baca UDP
	MaxStatMsg  = 40        // maksimum karakter pesan status yang disiarkan
)

// Jenis siaran UDP (payload teks dipisah '|', diawali magic).
const (
	Magic   = "BLGM1"
	UHello  = "HELLO" // kehadiran berkala: siapa saya
	UBye    = "BYE"   // pamit keluar aplikasi
	UQuery  = "QRY"   // "siapa saja yang online?"
	UTyping = "TYP"   // indikator sedang mengetik (unicast)
)

// Envelope adalah struktur JSON untuk semua pesan TCP.
// Field []byte otomatis dienkode base64 oleh encoding/json.
type Envelope struct {
	T    string `json:"t"`
	ID   string `json:"id,omitempty"`
	From string `json:"from,omitempty"`
	Nick string `json:"nick,omitempty"`
	To   string `json:"to,omitempty"`
	Text string `json:"text,omitempty"`
	TS   int64  `json:"ts,omitempty"`

	// tanda terima baca: daftar id pesan yang sudah dibaca
	IDs []string `json:"ids,omitempty"`

	// transfer file
	FID  string `json:"fid,omitempty"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
	Seq  int64  `json:"seq,omitempty"`
	Data []byte `json:"data,omitempty"`
	Sum  string `json:"sum,omitempty"` // sha256 hex isi file (tawaran kirim)

	// grup global: satu pesan TMsg membawa identitas grupnya supaya penerima
	// yang belum mengenal grup tetap bisa menampilkan pesan (tidak dibuang).
	G  string `json:"g,omitempty"`  // ID grup
	GN string `json:"gn,omitempty"` // nama grup
	GO string `json:"go,omitempty"` // UID pemilik grup
	// info grup (TGroupInfo): versi, anggota, dan status penghapusan
	GV      int64    `json:"gv,omitempty"`
	Members []string `json:"mem,omitempty"`
	Deleted bool     `json:"del,omitempty"`
}

// Status kehadiran yang disiarkan lewat HELLO.
// Urutan mengikuti menu dropdown di jendela utama.
const (
	StatOnline   = 0 // tersedia / online
	StatBusy     = 1 // sibuk
	StatDND      = 2 // jangan ganggu
	StatBRB      = 3 // segera kembali
	StatAway     = 4 // pergi
	StatInvis    = 5 // tampil offline
	StatCount    = 6
	StatMaxValue = StatCount - 1
)

// Tipe pesan TCP.
const (
	TMsg   = "msg"  // pesan chat
	TAck   = "ack"  // konfirmasi pesan diterima
	TPing  = "ping" // detak jantung
	TPong  = "pong"
	TOffer = "foffer" // tawaran kirim file
	TOk    = "fok"    // penerima setuju
	TNo    = "fno"    // penerima menolak / transfer dibatalkan
	TChunk = "fchunk" // potongan file
	TCAck  = "fcack"  // konfirmasi potongan
	TEnd   = "fend"   // pengiriman selesai
	TEAck  = "feack"  // konfirmasi selesai

	TAvatarReq = "avreq"  // "kirimkan foto profilmu"
	TAvatar    = "avatar" // balasan: PNG foto profil (bisa kosong)

	TRead = "read" // penanda bahwa daftar id pesan sudah dibaca penerima

	TGroupInfo = "ginfo" // sinkronisasi definisi grup (nama/anggota/hapus)
)

// MaxNickLen adalah panjang maksimum (rune) nama tampilan yang boleh
// disiarkan; nilai sama dengan batas kolom nama di jendela utama.
const MaxNickLen = 24

// ValidUID true bila s adalah 16 karakter heksadesimal kecil, format UID
// yang dibuat aplikasi. Data jaringan yang tidak cocok selalu ditolak
// karena dipakai sebagai nama berkas dan kunci daftar rekan.
func ValidUID(s string) bool { return isHexLower(s, 8) }

// ValidGroupID true bila s adalah ID grup bikinan aplikasi ("g" + 12 heks).
func ValidGroupID(s string) bool {
	return len(s) == 13 && s[0] == 'g' && isHexLower(s[1:], 6)
}

// ValidMsgID true bila s adalah id pesan heksadesimal 8 byte.
func ValidMsgID(s string) bool { return isHexLower(s, 8) }

func isHexLower(s string, bytesN int) bool {
	if len(s) != bytesN*2 {
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

// GroupChange adalah definisi grup yang dikirim antar PC.
type GroupChange struct {
	ID      string
	Name    string
	Owner   string
	Version int64
	Members []string
	Deleted bool
	TS      int64
}

// Valid reports whether the change is safe to apply (identity well-formed,
// name and membership bounded).
func (g *GroupChange) Valid() bool {
	if g == nil || !ValidGroupID(g.ID) || !ValidUID(g.Owner) || g.Version < 1 {
		return false
	}
	if len(g.Members) > 64 {
		return false
	}
	for _, m := range g.Members {
		if !ValidUID(m) {
			return false
		}
	}
	return true
}

// UDPMsg adalah hasil parsing satu payload UDP.
type UDPMsg struct {
	Kind       string
	UID        string
	To         string
	Nick       string
	PC         string
	TCPPort    int
	Stat       int    // status kehadiran (0..5); 0 bila pengirim versi lama
	AvatarID   int    // pilihan avatar bawaan (-1..11); -1 = otomatis
	AvatarHash string // md5 foto profil custom; "" = tidak pakai foto
	StatMsg    string // pesan status yang ditulis pengguna; "" = pengirim versi lama
	OK         bool
}

// CleanNick strips characters that would break the '|'-separated UDP payload
// plus control and bidi overrides, so a remote display name can never spoof
// the UI or hide its real direction. Runes beyond MaxNickLen are cut.
func CleanNick(s string) string {
	s = strings.NewReplacer("|", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u200e', '\u200f', '\ufeff':
			continue
		}
		if unicode.IsControl(r) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	cleaned := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(cleaned); len(r) > MaxNickLen {
		cleaned = string(r[:MaxNickLen])
	}
	return cleaned
}

// sanitize is the light payload guard for machine fields (UID, PC, hashes).
func sanitize(s string) string {
	r := strings.NewReplacer("|", " ", "\n", " ", "\r", " ")
	return r.Replace(s)
}

func clampStat(st int) int {
	if st < 0 || st > StatMaxValue {
		return StatOnline
	}
	return st
}

func clampAvatarID(id int) int {
	if id < -1 || id > 11 {
		return -1
	}
	return id
}

// BuildHello menyusun payload kehadiran.
// Field stat/avatarID/avatarHash/statMsg opsional: versi lama yang tidak
// mengirimnya tetap diparse oleh ParseUDP dengan nilai default, dan payload
// dari versi baru tetap diparse versi lama (field ekstra diabaikan).
func BuildHello(uid, nick, pc string, tcpPort, stat, avatarID int, avatarHash, statMsg string) string {
	return Magic + "|" + UHello + "|" + sanitize(uid) + "|" + CleanNick(nick) + "|" +
		sanitize(pc) + "|" + strconv.Itoa(tcpPort) + "|" +
		strconv.Itoa(stat) + "|" + strconv.Itoa(avatarID) + "|" + sanitize(avatarHash) + "|" +
		sanitize(statMsg)
}

func BuildBye(uid string) string   { return Magic + "|" + UBye + "|" + sanitize(uid) }
func BuildQuery(uid string) string { return Magic + "|" + UQuery + "|" + sanitize(uid) }

// BuildTyping: from = pengirim, to = penerima yang sedang diketik.
func BuildTyping(from, to string) string {
	return Magic + "|" + UTyping + "|" + sanitize(from) + "|" + sanitize(to)
}

// ParseUDP mengurai payload UDP; OK=false berarti bukan pesan Belgindo.
func ParseUDP(payload string) UDPMsg {
	payload = strings.TrimRight(payload, "\x00\r\n ")
	parts := strings.Split(payload, "|")
	if len(parts) < 3 || parts[0] != Magic {
		return UDPMsg{}
	}
	m := UDPMsg{Kind: parts[1], UID: parts[2], OK: true}
	switch m.Kind {
	case UHello:
		if len(parts) < 6 {
			return UDPMsg{}
		}
		m.Nick, m.PC = parts[3], parts[4]
		p, err := strconv.Atoi(parts[5])
		if err != nil || p <= 0 || p > 65535 {
			return UDPMsg{}
		}
		m.TCPPort = p
		m.AvatarID = -1
		if len(parts) > 6 {
			if st, err := strconv.Atoi(parts[6]); err == nil {
				m.Stat = clampStat(st)
			}
		}
		if len(parts) > 7 {
			if id, err := strconv.Atoi(parts[7]); err == nil {
				m.AvatarID = clampAvatarID(id)
			}
		}
		if len(parts) > 8 {
			m.AvatarHash = parts[8]
		}
		if len(parts) > 9 {
			m.StatMsg = parts[9]
		}
	case UBye, UQuery:
		// cukup uid
	case UTyping:
		if len(parts) < 4 {
			return UDPMsg{}
		}
		m.To = parts[3]
	default:
		return UDPMsg{}
	}
	return m
}

// Write menulis satu envelope sebagai satu baris JSON.
// Satu panggilan = satu paket logis; pembaca memisahkan per baris.
func Write(w interface{ Write([]byte) (int, error) }, e *Envelope) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if len(b) > MaxLineLen {
		return fmt.Errorf("pesan terlalu besar (%d byte)", len(b))
	}
	_, err = w.Write(b)
	return err
}

// Read membaca satu baris JSON dari reader buffered.
func Read(r *bufio.Reader) (*Envelope, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > MaxLineLen {
		return nil, fmt.Errorf("pesan terlalu besar")
	}
	var e Envelope
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, fmt.Errorf("pesan rusak: %w", err)
	}
	return &e, nil
}
