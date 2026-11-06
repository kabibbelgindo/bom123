//go:build windows

package ui

import (
	"time"

	"belgindo/internal/store"
)

// Isi ruang chat sekarang bertahan: yang tampil adalah riwayat HARI INI.
//
// Riwayat tetap satu-satunya penyimpanan (hapus riwayat = hilang permanen),
// jadi tidak ada berkas "tampilan" kedua: jendela cukup diisi ulang dari
// entri hari ini saat dibuka. Saat tanggal berganti, semua jendela yang masih
// terbuka ikut dibersihkan oleh perputaran hari (rollViewsIfNewDay).
const (
	maxViewEntries = 2000 // entri riwayat yang dibaca untuk menyaring hari ini
	maxViewBubbles = 400  // balon maksimum yang dirender di satu ruang chat
)

// todayStart mengembalikan unix time awal hari ini menurut jam PC.
func todayStart() int64 {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location()).Unix()
}

// dayKey dipakai untuk mendeteksi pergantian hari ketika jendela terbuka.
func dayKey() string { return time.Now().Format("2006-01-02") }

// viewFromHistory mengubah entri riwayat menjadi balon hari ini saja.
// peerNick dipakai untuk pesan masuk perorangan (riwayat pribadi tidak
// menyimpan nama pengirim per baris).
func viewFromHistory(entries []store.Entry, peerNick string) []chatMessage {
	cut := todayStart()
	var out []chatMessage
	for _, e := range entries {
		if e.TS < cut {
			continue
		}
		m, ok := bubbleEntry(e, peerNick)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	if len(out) > maxViewBubbles {
		out = out[len(out)-maxViewBubbles:]
	}
	return out
}

// bubbleEntry menafsirkan satu entri riwayat. Entri di luar hari ini dibuang
// oleh pemanggil, jadi di sini tidak perlu cek tanggal lagi.
func bubbleEntry(e store.Entry, peerNick string) (chatMessage, bool) {
	m := chatMessage{TS: e.TS, Dir: e.Dir, Text: e.Text, ID: e.ID, State: e.State}
	switch e.Kind {
	case "msg":
	case "file":
		name, size, path := splitFileEntry(e.Text)
		m.Text = name
		m.Att = &chatAttach{Name: name, Size: size, Path: path, IsPhoto: isPhotoName(name)}
	case "ntf":
		m.Dir, m.Who = "sys", "Info"
	default:
		return m, false
	}
	if e.Kind != "ntf" {
		switch m.Dir {
		case "out":
			m.Who = "Saya"
		default:
			m.Who = peerNick
			if e.Who != "" {
				m.Who = e.Who
			}
		}
	}
	return m, true
}

// seedToday mengisi satu jendela dari riwayat hari ini (perorangan).
func (c *Convo) seedToday() {
	c.mu.Lock()
	c.messages = viewFromHistory(store.LoadHistory(c.uid, maxViewEntries), c.nick)
	c.viewDay = dayKey()
	c.mu.Unlock()
}

// seedTodayGrup melakukan hal yang sama untuk satu ruang grup.
func (g *GroupConv) seedToday() {
	g.mu.Lock()
	g.messages = viewFromHistory(store.LoadGroupHistory(g.id, maxViewEntries), "")
	g.viewDay = dayKey()
	g.mu.Unlock()
}

// rollViewsIfNewDay memanggil ulang semua jendela yang masih terbuka ketika
// tanggal berganti; balon kemarin hilang dari layar tetapi riwayatnya utuh.
func (a *App) rollViewsIfNewDay() {
	today := dayKey()
	a.mu.Lock()
	if a.viewDay == "" {
		a.viewDay = today
		a.mu.Unlock()
		return
	}
	if today == a.viewDay {
		a.mu.Unlock()
		return
	}
	a.viewDay = today
	var convos []*Convo
	for _, c := range a.convos {
		convos = append(convos, c)
	}
	var groups []*GroupConv
	for _, g := range a.groups {
		groups = append(groups, g)
	}
	a.mu.Unlock()
	if len(convos)+len(groups) == 0 {
		return
	}
	a.mw.Synchronize(func() {
		for _, c := range convos {
			c.reloadToday()
		}
		for _, g := range groups {
			g.reloadToday()
		}
	})
}

// reloadToday menggambar ulang ruang chat dengan isi hari baru.
func (c *Convo) reloadToday() {
	c.seedToday()
	if c.wv != nil {
		_ = c.writePage()
	}
}

// reloadToday menggambar ulang ruang grup dengan isi hari baru.
func (g *GroupConv) reloadToday() {
	g.seedToday()
	if g.wv != nil {
		_ = g.writePage()
	}
}
