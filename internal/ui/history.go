//go:build windows

package ui

import (
	"fmt"
	"strings"
	"time"

	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// historyRow satu percakapan yang punya riwayat di disk — perorangan atau grup.
type historyRow struct {
	uid     string // UID rekan atau ID grup
	group   bool
	label   string
	entries int
	last    time.Time
}

// key adalah kunci peta LockedChats untuk baris riwayat ini.
func (r historyRow) key() string { return store.SafeKey(r.uid) }

// historyItems memuat daftar percakapan berriwayat, terbaru lebih dulu.
// Percakapan terkunci hanya menampilkan jumlahnya; isinya tidak terbuka
// sebelum password benar (Update.txt: riwayat ikut terkunci).
func (a *App) historyItems() []historyRow {
	var out []historyRow
	for _, h := range store.ListHistory() {
		nick, pc := a.displayNameForUID(h.UID), ""
		if p, ok := findPeer(a.hub.SnapshotPeers(), h.UID); ok {
			pc = p.PC
		}
		label := nick
		if pc != "" {
			label += " (" + pc + ")"
		}
		if a.chatLocked(store.SafeKey(h.UID)) {
			label = T("Terkunci") + " — " + label
		}
		out = append(out, historyRow{
			uid:     h.UID,
			label:   fmt.Sprintf("%s — %d — %s", label, h.Entries, h.Last.Format("02-01 15:04")),
			entries: h.Entries,
			last:    h.Last,
		})
	}
	for _, h := range store.ListGroupHistory() {
		label := T("Grup") + " " + a.groupNameFor(h.UID)
		if a.chatLocked(store.SafeKey(h.UID)) {
			label = T("Terkunci") + " — " + label
		}
		out = append(out, historyRow{
			uid:     h.UID,
			group:   true,
			label:   fmt.Sprintf("%s — %d — %s", label, h.Entries, h.Last.Format("02-01 15:04")),
			entries: h.Entries,
			last:    h.Last,
		})
	}
	sortHistoryRows(out)
	return out
}

// sortHistoryRows mengurutkan terbaru lebih dulu.
func sortHistoryRows(rows []historyRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].last.After(rows[j-1].last); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// showHistory — menu Pengaturan > Riwayat.
func (a *App) showHistory() {
	if a.histWin != nil {
		a.histWin.Show()
		a.histWin.Activate()
		return
	}

	var lb *walk.ListBox
	var log *walk.TextEdit
	var lblInfo *walk.TextLabel
	var rows []historyRow
	var showSelected, reload func()

	reload = func() {
		rows = a.historyItems()
		items := make([]string, len(rows))
		for i, r := range rows {
			items[i] = r.label
		}
		_ = lb.SetModel(items)
		_ = log.SetText("")
		if len(rows) == 0 {
			_ = lblInfo.SetText(T("Belum ada riwayat tersimpan di PC ini."))
			return
		}
		_ = lblInfo.SetText(fmt.Sprintf(T("%d percakapan tersimpan di %s"), len(rows), store.HistoryDir()))
		showSelected()
	}

	showSelected = func() {
		i := lb.CurrentIndex()
		if i < 0 || i >= len(rows) {
			return
		}
		r := rows[i]
		if a.chatLocked(r.key()) && !a.unlockGate(r.key()) {
			_ = log.SetText(T("Percakapan ini dikunci. Masukkan password untuk membaca isinya."))
			return
		}
		if r.group {
			_ = log.SetText(renderGroupHistory(store.LoadGroupHistory(r.uid, 1000)))
			return
		}
		_ = log.SetText(renderHistory(store.LoadHistory(r.uid, 1000), a.displayNameForUID(r.uid)))
	}

	if err := (MainWindow{
		AssignTo: &a.histWin,
		Title:    T("Riwayat — Belgindo Office Messenger"),
		MinSize:  Size{Width: 380, Height: 240},
		Size:     Size{Width: 620, Height: 400},
		Font:     Font{Family: "Segoe UI", PointSize: 9},
		Layout:   VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 6},
		Children: []Widget{
			Composite{
				Layout:        HBox{MarginsZero: true, Spacing: 6},
				StretchFactor: 1,
				Children: []Widget{
					ListBox{
						AssignTo:              &lb,
						MinSize:               Size{Width: 230},
						OnCurrentIndexChanged: showSelected,
					},
					TextEdit{
						AssignTo:      &log,
						ReadOnly:      true,
						VScroll:       true,
						StretchFactor: 1,
						Background:    SolidColorBrush{Color: walk.RGB(0xFB, 0xFC, 0xFE)},
					},
				},
			},
			TextLabel{AssignTo: &lblInfo, Text: T("Memuat riwayat..."), TextColor: walk.RGB(0x5B, 0x6B, 0x7A)},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					PushButton{Text: T("Segarkan"), OnClicked: reload},
					PushButton{Text: T("Buka Folder Riwayat"), OnClicked: func() { openFolder(store.HistoryDir()) }},
					HSpacer{},
					PushButton{Text: T("Hapus Riwayat Ini"), OnClicked: func() {
						i := lb.CurrentIndex()
						if i < 0 || i >= len(rows) {
							return
						}
						if a.clearHistoryRow(rows[i]) {
							reload()
						}
					}},
				},
			},
		},
	}).Create(); err != nil {
		a.balloon(T("Gagal"), T("Tidak bisa membuka jendela Riwayat: ")+err.Error())
		return
	}
	_ = a.histWin.SetIcon(a.iconApp)
	a.histWin.Closing().Attach(func(canceled *bool, reason walk.CloseReason) { a.histWin = nil })
	centerWindow(a.histWin)
	a.histWin.Show()
	reload()
}

// clearHistoryRow menghapus riwayat SATU percakapan secara permanen; true bila
// jadi dihapus. Riwayat percakapan terkunci tidak bisa dihapus tanpa password.
func (a *App) clearHistoryRow(r historyRow) bool {
	if a.chatLocked(r.key()) && !a.unlockGate(r.key()) {
		return false
	}
	var owner walk.Form = a.mw
	if a.histWin != nil {
		owner = a.histWin
	}
	if walk.MsgBox(owner, T("Hapus Riwayat"),
		fmt.Sprintf(T("Hapus seluruh riwayat %s? Riwayat yang dihapus tidak bisa dikembalikan."),
			a.historyRowName(r)),
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return false
	}
	if r.group {
		_ = store.ClearGroupHistory(r.uid)
	} else {
		_ = store.ClearHistory(r.uid)
	}
	a.refreshList()
	return true
}

// historyRowName mengambil nama tampilan sebuah baris riwayat.
func (a *App) historyRowName(r historyRow) string {
	if r.group {
		return T("Grup") + " " + a.groupNameFor(r.uid)
	}
	return a.displayNameForUID(r.uid)
}

// clearSelectedHistory — klik kanan pada daftar: hapus permanen riwayat
// percakapan terpilih.
func (a *App) clearSelectedHistory() {
	uid, gid := a.selected()
	switch {
	case gid != "":
		if _, ok := a.groupSnap(gid); !ok {
			return
		}
		a.clearHistoryRow(historyRow{uid: gid, group: true})
	case uid != "":
		a.clearHistoryRow(historyRow{uid: uid})
	}
}

// splitFileEntry mengurai "nama|ukuran|path" seperti yang ditulis riwayat.
func splitFileEntry(s string) (name string, size int64, path string) {
	parts := strings.SplitN(s, "|", 3)
	if len(parts) != 3 {
		return s, 0, ""
	}
	_, _ = fmt.Sscanf(parts[1], "%d", &size)
	return parts[0], size, parts[2]
}

// indentLines menulis isi pesan berbaris dengan CRLF di dalam penampil riwayat.
func indentLines(text string) string {
	var sb strings.Builder
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		sb.WriteString("  " + ln + "\r\n")
	}
	sb.WriteString("\r\n")
	return sb.String()
}

// renderHistory memformat riwayat perorangan menjadi teks dengan CRLF (\r\n).
func renderHistory(entries []store.Entry, peerNick string) string {
	var sb strings.Builder
	for _, e := range entries {
		stamp := time.Unix(e.TS, 0).Format("15:04")
		switch e.Kind {
		case "msg":
			who := T("Saya")
			if e.Dir == "in" {
				who = peerNick
			}
			sb.WriteString(fmt.Sprintf("[%s] %s:\r\n", stamp, who))
			sb.WriteString(indentLines(e.Text))
		case "file":
			name, size, path := splitFileEntry(e.Text)
			label := T("Saya kirim")
			if e.Dir == "in" {
				label = T("Diterima")
			}
			sb.WriteString(fmt.Sprintf("[%s] %s: %s (%s)\r\n", stamp, label, name, humanSize(size)))
			if path != "" && e.Dir == "in" {
				sb.WriteString("  " + T("Tersimpan") + ": " + path + "\r\n")
			}
			sb.WriteString("\r\n")
		case "ntf":
			sb.WriteString(fmt.Sprintf("( %s )\r\n\r\n", e.Text))
		}
	}
	return sb.String()
}

// renderGroupHistory memformat riwayat grup; nama pengirim ikut tersimpan.
func renderGroupHistory(entries []store.Entry) string {
	var sb strings.Builder
	for _, e := range entries {
		stamp := time.Unix(e.TS, 0).Format("15:04")
		switch e.Kind {
		case "msg":
			who := T("Saya")
			if e.Dir == "in" {
				who = e.Who
				if who == "" {
					who = T("Anggota")
				}
			}
			sb.WriteString(fmt.Sprintf("[%s] %s:\r\n", stamp, who))
			sb.WriteString(indentLines(e.Text))
		case "file":
			name, size, path := splitFileEntry(e.Text)
			who := T("Saya")
			if e.Dir == "in" {
				who = e.Who
				if who == "" {
					who = T("Anggota")
				}
			}
			sb.WriteString(fmt.Sprintf("[%s] %s — %s (%s)\r\n", stamp, who, name, humanSize(size)))
			if path != "" && e.Dir == "in" {
				sb.WriteString("  " + T("Tersimpan") + ": " + path + "\r\n")
			}
			sb.WriteString("\r\n")
		case "ntf":
			sb.WriteString(fmt.Sprintf("( %s )\r\n\r\n", e.Text))
		}
	}
	return sb.String()
}
