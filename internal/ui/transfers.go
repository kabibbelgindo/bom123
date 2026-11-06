//go:build windows

package ui

import (
	"fmt"
	"os/exec"
	"sort"
	"time"

	"belgindo/internal/p2p"
	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// transferKeep jumlah maksimum catatan transfer yang disimpan.
const transferKeep = 400

// ---------- pencatatan ----------

// txStart membuka satu baris log transfer untuk fid.
func (a *App) txStart(fid, dir, peer, name string, size int64) {
	a.mu.Lock()
	a.transfers = append(a.transfers, store.Transfer{
		TS: time.Now().Unix(), Dir: dir, Peer: peer, Name: name, Size: size, Status: "berjalan",
	})
	if fid != "" {
		a.txLive[fid] = len(a.transfers) - 1
	}
	if len(a.transfers) > transferKeep {
		a.transfers = append([]store.Transfer(nil), a.transfers[len(a.transfers)-transferKeep:]...)
		a.txLive = make(map[string]int)
	}
	rows := append([]store.Transfer(nil), a.transfers...)
	a.mu.Unlock()
	_ = store.SaveTransfers(rows)
	a.txRefreshView()
}

// txFindLocked mencari baris berjalan yang cocok dengan peristiwa e.
func (a *App) txFindLocked(e p2p.Event) int {
	if i, ok := a.txLive[e.FID]; ok && i >= 0 && i < len(a.transfers) && a.transfers[i].Status == "berjalan" {
		return i
	}
	dir := "out"
	if e.Dir == p2p.DirIn {
		dir = "in"
	}
	for i := len(a.transfers) - 1; i >= 0; i-- {
		t := a.transfers[i]
		if t.Dir == dir && t.Name == e.FileName && t.Status == "berjalan" {
			if e.FID != "" {
				a.txLive[e.FID] = i
			}
			return i
		}
	}
	return -1
}

func dirName(e p2p.Event) string {
	if e.Dir == p2p.DirIn {
		return "in"
	}
	return "out"
}

// txProgress memperbarui persen transfer berjalan.
func (a *App) txProgress(e p2p.Event) {
	a.mu.Lock()
	i := a.txFindLocked(e)
	if i < 0 {
		a.mu.Unlock()
		a.txStart(e.FID, dirName(e), e.Nick, e.FileName, e.Size)
		a.mu.Lock()
		i = a.txFindLocked(e)
		if i < 0 {
			a.mu.Unlock()
			return
		}
	}
	a.transfers[i].Pct = e.Percent
	if a.transfers[i].Size == 0 {
		a.transfers[i].Size = e.Size
	}
	a.mu.Unlock()
	a.txRefreshView()
}

// txFinish menutup satu baris log transfer dengan status akhir.
func (a *App) txFinish(e p2p.Event, status string) {
	a.mu.Lock()
	i := a.txFindLocked(e)
	if i < 0 {
		a.mu.Unlock()
		a.txStart(e.FID, dirName(e), e.Nick, e.FileName, e.Size)
		a.mu.Lock()
		i = a.txFindLocked(e)
		if i < 0 {
			a.mu.Unlock()
			return
		}
	}
	t := &a.transfers[i]
	t.Status = status
	if e.Path != "" {
		t.Path = e.Path
	}
	if e.Size != 0 {
		t.Size = e.Size
	}
	if status == "selesai" {
		t.Pct = 100
	}
	if e.FID != "" {
		delete(a.txLive, e.FID)
	}
	rows := append([]store.Transfer(nil), a.transfers...)
	a.mu.Unlock()
	_ = store.SaveTransfers(rows)
	a.txRefreshView()
}

// ---------- jendela log transfer ----------

type transferModel struct {
	walk.TableModelBase
	rows []store.Transfer // terbaru di atas
}

func (m *transferModel) RowCount() int { return len(m.rows) }

func (m *transferModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	t := m.rows[row]
	switch col {
	case 0:
		return time.Unix(t.TS, 0).Format("02-01 15:04")
	case 1:
		if t.Dir == "in" {
			return "Terima"
		}
		return "Kirim"
	case 2:
		return t.Peer
	case 3:
		return t.Name
	case 4:
		return humanSize(t.Size)
	case 5:
		if t.Status == "berjalan" {
			return fmt.Sprintf("%.0f%%", t.Pct)
		}
		return t.Status
	}
	return ""
}

func (a *App) txRefreshView() {
	if a.txTV == nil || a.txModel == nil {
		return
	}
	a.mu.Lock()
	rows := append([]store.Transfer(nil), a.transfers...)
	a.mu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TS > rows[j].TS })
	a.txModel.rows = rows
	a.txModel.RowsChanged()
}

// showTransfers — menu Pengaturan > Transfer File.
func (a *App) showTransfers() {
	if a.txWin != nil {
		a.txWin.Show()
		a.txWin.Activate()
		a.txRefreshView()
		return
	}
	a.txModel = &transferModel{}

	if err := (MainWindow{
		AssignTo:      &a.txWin,
		Title:         T("Transfer File — Belgindo Office Messenger"),
		MinSize:       Size{Width: 360, Height: 220},
		Size:          Size{Width: 560, Height: 340},
		Font:          Font{Family: "Segoe UI", PointSize: 9},
		Layout:        VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 6},
		OnSizeChanged: a.txLayout,
		Children: []Widget{
			TableView{
				AssignTo: &a.txTV,
				Columns: []TableViewColumn{
					{Name: "waktu", Title: T("Waktu"), Width: 84},
					{Name: "arah", Title: T("Arah"), Width: 56},
					{Name: "rekan", Title: T("Rekan"), Width: 110},
					{Name: "file", Title: T("File"), Width: 180},
					{Name: "ukuran", Title: T("Ukuran"), Width: 70},
					{Name: "status", Title: T("Status"), Width: 70},
				},
				Model:            a.txModel,
				ContextMenuItems: []MenuItem{Action{Text: T("Buka Lokasi File"), OnTriggered: a.openTransferPath}},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					PushButton{Text: T("Buka Folder File"), OnClicked: func() { openFolder(store.InboxDir()) }},
					PushButton{Text: T("Segarkan"), OnClicked: a.txRefreshView},
					HSpacer{},
					PushButton{Text: T("Hapus Catatan"), OnClicked: a.clearTransfers},
				},
			},
		},
	}).Create(); err != nil {
		a.balloon(T("Gagal"), T("Tidak bisa membuka jendela Transfer File: ")+err.Error())
		return
	}
	_ = a.txWin.SetIcon(a.iconApp)
	a.txWin.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.txWin = nil
		a.txTV = nil
		a.txModel = nil
	})
	centerWindow(a.txWin)
	a.txRefreshView()
	a.txLayout()
	a.txWin.Show()
}

func (a *App) txLayout() {
	if a.txWin == nil || a.txTV == nil {
		return
	}
	w := a.txWin.ClientBounds().Width
	if col := a.txTV.Columns().ByName("file"); col != nil {
		avail := w - 380
		if avail < 60 {
			avail = 60
		}
		_ = col.SetWidth(avail)
	}
	for name, hide := range map[string]bool{"rekan": w < 420, "waktu": w < 330} {
		if col := a.txTV.Columns().ByName(name); col != nil {
			_ = col.SetVisible(!hide)
		}
	}
}

func (a *App) clearTransfers() {
	if walk.MsgBox(a.txWin, T("Hapus Catatan Transfer"),
		T("Hapus semua catatan transfer file di PC ini?"),
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	a.mu.Lock()
	a.transfers = nil
	a.txLive = make(map[string]int)
	a.mu.Unlock()
	_ = store.SaveTransfers(nil)
	a.txRefreshView()
}

func (a *App) openTransferPath() {
	if a.txTV == nil || a.txModel == nil {
		return
	}
	i := a.txTV.CurrentIndex()
	if i < 0 || i >= len(a.txModel.rows) {
		return
	}
	t := a.txModel.rows[i]
	if t.Path == "" {
		openFolder(store.InboxDir())
		return
	}
	_ = exec.Command("explorer.exe", "/select,"+t.Path).Start()
}

func openFolder(path string) {
	_ = exec.Command("explorer.exe", path).Start()
}
