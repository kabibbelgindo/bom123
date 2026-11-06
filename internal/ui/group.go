//go:build windows

package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"belgindo/internal/p2p"
	"belgindo/internal/proto"
	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// GroupConv satu jendela ruang percakapan grup. Grup bersifat GLOBAL:
// definisinya (nama, anggota, pemilik, versi) disinkronkan lewat jaringan,
// jadi satu grup yang sama ada di PC semua anggotanya, bukan hanya di PC
// pembuatnya.
type GroupConv struct {
	app   *App
	id    string
	owner string

	win      *walk.MainWindow
	wv       *walk.WebView
	bridge   pageBridge
	htmlPath string

	mu        sync.Mutex
	name      string
	members   []string
	version   int64
	messages  []chatMessage
	viewDay   string // tanggal yang sedang tampil di layar
	sendCtl   *p2p.SendCtl
	sendQueue []string
}

// ---------- definisi grup ----------

func newGroupID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "g" + hex.EncodeToString(b)
}

func (a *App) groupDef(gid string) *store.Group {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.groupDefs {
		if a.groupDefs[i].ID == gid {
			return &a.groupDefs[i]
		}
	}
	return nil
}

// groupSnap menyalin definisi grup. Salinan diperlukan karena dialog modal
// dapat menyelingi proses dan a.groupDefs bisa berubah (append membuat pointer
// lama menunjuk array usang).
func (a *App) groupSnap(gid string) (store.Group, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.groupDefs {
		if a.groupDefs[i].ID == gid && !a.groupDefs[i].Deleted {
			g := a.groupDefs[i]
			g.Members = append([]string(nil), a.groupDefs[i].Members...)
			return g, true
		}
	}
	return store.Group{}, false
}

// groupOwnerOnly memeriksa hak ubah grup. Grup warisan tanpa pemilik dianggap
// milik PC ini agar tidak terkunci.
func (a *App) groupOwnerOnly(g store.Group) bool {
	if g.Owner == "" || g.Owner == a.hub.MyUID() {
		return true
	}
	walk.MsgBox(a.mw, T("Hanya Pemilik Grup"),
		fmt.Sprintf(T("Hanya %s yang bisa mengubah grup \"%s\"."),
			a.displayNameForUID(g.Owner), g.Name),
		walk.MsgBoxOK|walk.MsgBoxIconInformation)
	return false
}

func groupChangeOf(g store.Group, owner string) proto.GroupChange {
	if owner == "" {
		owner = g.Owner
	}
	return proto.GroupChange{
		ID:      g.ID,
		Name:    g.Name,
		Owner:   owner,
		Version: g.Version,
		Members: append([]string(nil), g.Members...),
		Deleted: g.Deleted,
		TS:      g.Updated,
	}
}

// applyGroupChange menyatukan perubahan grup yang datang dari jaringan.
// Versi lebih baru menang; versi sama dengan isi berbeda dianggap perubahan
// pemilik (hanya pemilik yang berhak menulis ulang). true bila ada perubahan.
func (a *App) applyGroupChange(gc proto.GroupChange) bool {
	if !gc.Valid() {
		return false
	}
	a.mu.Lock()
	idx := -1
	for i := range a.groupDefs {
		if a.groupDefs[i].ID == gc.ID {
			idx = i
			break
		}
	}
	changed := false
	g := store.Group{
		ID: gc.ID, Name: gc.Name, Members: gc.Members,
		Owner: gc.Owner, Version: gc.Version, Deleted: gc.Deleted,
		Updated: maxInt64(gc.TS, time.Now().Unix()),
	}
	switch {
	case idx < 0:
		a.groupDefs = append(a.groupDefs, g)
		changed = true
	case a.groupDefs[idx].Version < gc.Version:
		a.groupDefs[idx] = g
		changed = true
	case a.groupDefs[idx].Version == gc.Version && a.groupDefs[idx].Deleted && !gc.Deleted:
		// batu nisan tidak boleh bangkit kembali
	default:
	}
	list := append([]store.Group(nil), a.groupDefs...)
	a.mu.Unlock()
	if !changed {
		return false
	}
	_ = store.SaveGroups(list)
	a.refreshList()
	return true
}

func maxInt64(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}

// ---------- pemilihan anggota ----------

type peerPickModel struct {
	walk.TableModelBase
	rows    []p2p.Peer
	checked []bool
}

func (m *peerPickModel) RowCount() int { return len(m.rows) }

func (m *peerPickModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	p := m.rows[row]
	if col == 0 {
		return p.Nick
	}
	return p.PC
}

func (m *peerPickModel) Checked(index int) bool {
	if index < 0 || index >= len(m.checked) {
		return false
	}
	return m.checked[index]
}

func (m *peerPickModel) SetChecked(index int, checked bool) error {
	if index < 0 || index >= len(m.checked) {
		return fmt.Errorf("indeks di luar jangkauan")
	}
	m.checked[index] = checked
	return nil
}

// pickPeers menampilkan dialog pilihan rekan; hadExclude menandai yang sudah
// menjadi anggota.
func (a *App) pickPeers(title string, hadExclude map[string]bool) []string {
	var dlg *walk.Dialog
	var tv *walk.TableView
	var db, cb *walk.PushButton

	var peers []p2p.Peer
	for _, p := range a.hub.SnapshotPeers() {
		if p.Online && !hadExclude[p.UID] {
			peers = append(peers, p)
		}
	}
	if len(peers) == 0 {
		walk.MsgBox(a.mw, title, T("Tidak ada rekan yang sedang online."),
			walk.MsgBoxOK|walk.MsgBoxIconInformation)
		return nil
	}
	model := &peerPickModel{rows: peers, checked: make([]bool, len(peers))}
	if err := (Dialog{
		AssignTo: &dlg, Title: title,
		MinSize: Size{Width: 340, Height: 320}, Size: Size{Width: 380, Height: 360},
		Font:   Font{Family: "Segoe UI", PointSize: 9},
		Layout: VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 6},
		Children: []Widget{
			Label{Text: T("Anggota (centang rekan yang ikut serta):")},
			TableView{
				AssignTo:   &tv,
				CheckBoxes: true,
				Columns:    []TableViewColumn{{Title: T("Pengguna"), Width: 170}, {Title: T("Komputer"), Width: 150}},
				Model:      model,
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &db, Text: T("OK"), OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cb, Text: T("Batal"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}).Create(a.mw); err != nil {
		return nil
	}
	if dlg.Run() != walk.DlgCmdOK {
		dlg.Dispose()
		return nil
	}
	var out []string
	for i, p := range model.rows {
		if model.checked[i] {
			out = append(out, p.UID)
		}
	}
	dlg.Dispose()
	return out
}

// ---------- aksi grup ----------

// createGroup — menu Chat > Buat Grup. Grup langsung disiarkan ke semua
// anggota sehingga muncul di PC mereka juga.
func (a *App) createGroup() {
	members := a.pickPeers(T("Buat Grup"), nil)
	if len(members) < 2 {
		walk.MsgBox(a.mw, T("Buat Grup"), T("Pilih minimal 2 rekan."),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	var name string
	if !a.promptText(T("Buat Grup"), T("Nama grup:"), "Grup "+time.Now().Format("1504"), &name) {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Grup " + time.Now().Format("1504")
	}
	g := store.Group{
		ID: newGroupID(), Name: name, Members: members,
		Owner: a.hub.MyUID(), Version: 1, Updated: time.Now().Unix(),
	}
	a.mu.Lock()
	a.groupDefs = append(a.groupDefs, g)
	list := append([]store.Group(nil), a.groupDefs...)
	a.mu.Unlock()
	_ = store.SaveGroups(list)
	a.broadcastGroup(groupChangeOf(g, ""))
	a.refreshList()
	a.openGroup(g.ID)
}

// broadcastGroup mengirim definisi grup ke seluruh anggotanya.
func (a *App) broadcastGroup(gc proto.GroupChange) {
	if !gc.Valid() {
		return
	}
	go a.hub.BroadcastGroupInfo(gc, gc.Members)
}

// renameGroup — klik kanan pada nama grup.
func (a *App) renameGroup(gid string) {
	def, ok := a.groupSnap(gid)
	if !ok || !a.groupOwnerOnly(def) {
		return
	}
	var name string
	if !a.promptText(T("Ubah Nama Grup"), T("Nama grup baru:"), def.Name, &name) {
		return
	}
	name = truncateRunes(strings.TrimSpace(name), 40)
	if name == "" || name == def.Name {
		return
	}
	a.mu.Lock()
	for i := range a.groupDefs {
		if a.groupDefs[i].ID == gid {
			a.groupDefs[i].Name = name
			a.groupDefs[i].Version++
			a.groupDefs[i].Updated = time.Now().Unix()
		}
	}
	list := append([]store.Group(nil), a.groupDefs...)
	a.mu.Unlock()
	_ = store.SaveGroups(list)
	gc := groupChangeOf(findGroup(list, gid), "")
	a.broadcastGroup(gc)
	a.mu.Lock()
	if g := a.groups[gid]; g != nil {
		g.setName(name, gc.Version)
	}
	a.mu.Unlock()
	a.refreshList()
}

// addGroupMembers — menambah anggota berlaku global (semua PC ikut berubah).
func (a *App) addGroupMembers(gid string) {
	def, ok := a.groupSnap(gid)
	if !ok || !a.groupOwnerOnly(def) {
		return
	}
	had := make(map[string]bool, len(def.Members))
	for _, m := range def.Members {
		had[m] = true
	}
	added := a.pickPeers(T("Tambah Anggota Grup"), had)
	if len(added) == 0 {
		return
	}
	a.mu.Lock()
	for i := range a.groupDefs {
		if a.groupDefs[i].ID == gid {
			a.groupDefs[i].Members = append(a.groupDefs[i].Members, added...)
			a.groupDefs[i].Version++
			a.groupDefs[i].Updated = time.Now().Unix()
		}
	}
	list := append([]store.Group(nil), a.groupDefs...)
	a.mu.Unlock()
	_ = store.SaveGroups(list)
	gc := groupChangeOf(findGroup(list, gid), "")
	a.broadcastGroup(gc)
	a.refreshList()
}

// deleteGroup menghapus grup dari daftar SEMUA anggotanya (batu nisan ikut
// disiarkan supaya grup tidak muncul kembali).
func (a *App) deleteGroup(gid string) {
	def, ok := a.groupSnap(gid)
	if !ok {
		return
	}
	if !a.unlockGate(gid) {
		return
	}
	global := def.Owner == "" || def.Owner == a.hub.MyUID()
	prompt := fmt.Sprintf(T("Hapus grup \"%s\" dari daftar semua anggotanya?"), def.Name)
	if !global {
		prompt = fmt.Sprintf(T("Anda bukan pemilik grup \"%s\". Grup hanya dihapus dari PC ini, tetap ada untuk anggota lain. Lanjutkan?"), def.Name)
	}
	if walk.MsgBox(a.mw, T("Hapus Grup"), prompt,
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	if !global {
		a.mu.Lock()
		list := dropTombstone(append([]store.Group(nil), a.groupDefs...), gid)
		a.groupDefs = list
		g := a.groups[gid]
		delete(a.groups, gid)
		a.mu.Unlock()
		_ = store.SaveGroups(list)
		if g != nil && g.win != nil {
			_ = g.win.Close()
		}
		a.refreshList()
		return
	}
	a.mu.Lock()
	for i := range a.groupDefs {
		if a.groupDefs[i].ID == gid {
			a.groupDefs[i].Deleted = true
			a.groupDefs[i].Version++
			a.groupDefs[i].Updated = time.Now().Unix()
		}
	}
	gc := groupChangeOf(a.groupDefs[indexOfGroup(a.groupDefs, gid)], "")
	list := append([]store.Group(nil), a.groupDefs...)
	a.groupDefs = dropTombstone(list, gid)
	g := a.groups[gid]
	delete(a.groups, gid)
	a.mu.Unlock()
	_ = store.SaveGroups(a.groupDefs)
	a.broadcastGroup(gc)
	if g != nil && g.win != nil {
		g.win.Close()
	}
	a.refreshList()
}

func indexOfGroup(gs []store.Group, gid string) int {
	for i := range gs {
		if gs[i].ID == gid {
			return i
		}
	}
	return 0
}

func dropTombstone(gs []store.Group, gid string) []store.Group {
	var keep []store.Group
	for _, g := range gs {
		if g.ID != gid {
			keep = append(keep, g)
		}
	}
	return keep
}

func findGroup(gs []store.Group, gid string) store.Group {
	for _, g := range gs {
		if g.ID == gid {
			return g
		}
	}
	return store.Group{}
}

// deleteSelectedGroup — aksi klik kanan "Hapus Grup".
func (a *App) deleteSelectedGroup() {
	_, gid := a.selected()
	if gid != "" {
		a.deleteGroup(gid)
	}
}

// renameSelectedGroup — aksi klik kanan "Ubah Nama Grup".
func (a *App) renameSelectedGroup() {
	_, gid := a.selected()
	if gid != "" {
		a.renameGroup(gid)
	}
}

// addMembersSelectedGroup — aksi klik kanan "Tambah Anggota Grup".
func (a *App) addMembersSelectedGroup() {
	_, gid := a.selected()
	if gid != "" {
		a.addGroupMembers(gid)
	}
}

// clearSelectedChat mengosongkan TAMPILAN percakapan terpilih. Isi riwayat di
// disk tetap utuh (Update.txt: yang hilang hanya di jendela).
func (a *App) clearSelectedChat() {
	uid, gid := a.selected()
	switch {
	case gid != "":
		if g := a.groupConvIfOpen(gid); g != nil {
			g.clearView()
			return
		}
		if _, ok := a.groupSnap(gid); ok {
			walk.MsgBox(a.mw, T("Bersihkan obrolan"),
				T("Buka dulu ruangnya untuk membersihkan tampilan."),
				walk.MsgBoxOK|walk.MsgBoxIconInformation)
		}
	case uid != "":
		if c := a.convoIfOpen(uid); c != nil {
			c.clearView()
			return
		}
		walk.MsgBox(a.mw, T("Bersihkan obrolan"),
			T("Buka dulu percakapannya untuk membersihkan tampilan."),
			walk.MsgBoxOK|walk.MsgBoxIconInformation)
	}
}

// ---------- jendela ruang grup ----------

// Batas kecil jendela grup; tinggi dan lebar pakai groupWinSize().
const (
	groupMinW = 320
	groupMinH = convoMinH
)

// groupWinSize mengembalikan ukuran jendela grup: lebar -15% dan tinggi -10%
// dari ukuran lama (chatWidth()+120) x 480.
func (a *App) groupWinSize() (w, h int) {
	return (a.chatWidth() + 120) * 85 / 100, convoH
}

func (g *GroupConv) writePage() error {
	g.mu.Lock()
	msgs := append([]chatMessage(nil), g.messages...)
	name, members := g.name, len(g.members)
	g.mu.Unlock()
	page := buildChatPageHTML(chatPageOpts{
		Name:        name,
		Sub:         fmt.Sprintf(T("%d anggota"), members),
		Group:       true,
		Placeholder: T("Tulis pesan untuk grup, Enter kirim, Ctrl+Enter baris baru"),
		Messages:    msgs,
	})
	if err := writeChatPageFile(g.htmlPath, []byte(page)); err != nil {
		return err
	}
	g.bridge.reset()
	if g.wv != nil {
		return g.wv.SetURL(g.htmlPath)
	}
	return nil
}

func (a *App) openGroup(gid string) {
	if !a.unlockGate(gid) {
		return
	}
	a.mu.Lock()
	if g := a.groups[gid]; g != nil {
		a.mu.Unlock()
		g.win.Show()
		g.win.Activate()
		return
	}
	a.mu.Unlock()

	def := a.groupDef(gid)
	if def == nil {
		return
	}
	g := &GroupConv{app: a, id: def.ID, name: def.Name, owner: def.Owner,
		members: append([]string(nil), def.Members...), version: def.Version}
	g.htmlPath = filepath.Join(store.Dir(), "group_"+store.SafeKey(g.id)+".html")
	g.seedToday()

	gw, gh := a.groupWinSize()
	if err := (MainWindow{
		AssignTo: &g.win,
		Title:    T("Grup") + " — " + g.name + "  (" + fmt.Sprint(len(g.members)) + ")",
		MinSize:  Size{Width: groupMinW, Height: groupMinH},
		Size:     Size{Width: gw, Height: gh},
		Font:     Font{Family: "Segoe UI", PointSize: 9},
		Layout:   VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{WebView{AssignTo: &g.wv}},
	}).Create(); err != nil {
		a.balloon(T("Gagal"), T("Tidak bisa membuka jendela grup: ")+err.Error())
		return
	}
	_ = g.win.SetIcon(a.iconApp)
	g.attachHandlers()
	if err := g.writePage(); err != nil {
		g.win.Dispose()
		a.balloon(T("Gagal"), err.Error())
		return
	}
	g.win.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.mu.Lock()
		delete(a.groups, g.id)
		a.mu.Unlock()
		_ = os.Remove(g.htmlPath)
	})
	a.mu.Lock()
	a.groups[gid] = g
	a.mu.Unlock()
	centerWindow(g.win)
	g.win.Show()
}

func (g *GroupConv) attachHandlers() {
	g.bridge.attach(g.wv)
	g.wv.Navigating().Attach(func(e *walk.WebViewNavigatingEventData) {
		u := appURL(e.Url())
		switch {
		case u == "app://ready":
			e.SetCanceled(true)
			g.bridge.markReady()
		case u == "app://emoji":
			e.SetCanceled(true)
			g.app.mw.Synchronize(func() { showEmojiPicker(g.win, g.insertEmoji) })
		case u == "app://attach":
			e.SetCanceled(true)
			g.app.mw.Synchronize(g.pickFiles)
		case u == "app://cancel":
			e.SetCanceled(true)
			g.cancelSend()
		case u == "app://clearchat":
			e.SetCanceled(true)
			g.clearView()
		case strings.HasPrefix(u, "app://copy"):
			e.SetCanceled(true)
			if t := parseAppQuery(u, "p"); t != "" {
				_ = salinTeks(t)
			}
		case strings.HasPrefix(u, "app://open"):
			e.SetCanceled(true)
			p, err := url.QueryUnescape(parseAppQuery(u, "p"))
			if err != nil || p == "" {
				return
			}
			if msg := handleOpenRequest(p, true); msg != "" {
				g.appendSys(msg)
			}
		default:
			if u == "about:blank" || strings.HasPrefix(u, "javascript:") || sameLocalPage(u, g.htmlPath) {
				return
			}
			e.SetCanceled(true)
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				_ = bukaBerkasBawaan(u)
			}
		}
	})
	g.wv.DocumentTitleChanged().Attach(func() {
		if id, text, ok := parseSendTitle(g.wv.DocumentTitle()); ok {
			g.sendText(id, text)
		}
	})
	// Navigasi "javascript:" terakhir sudah selesai: boleh lepas batch berikut.
	g.wv.Navigated().Attach(func(string) { g.bridge.finish() })
	for _, d := range []interface{ DropFiles() *walk.DropFilesEvent }{g.win, g.wv} {
		d.DropFiles().Attach(func(paths []string) {
			paths = expandDropPaths(paths)
			if len(paths) == 0 {
				return
			}
			g.app.mw.Synchronize(func() { g.queueFiles(paths) })
		})
	}
}

func (g *GroupConv) insertEmoji(s string) {
	g.bridge.pushCmd(pageCmd{C: "insert", T: s})
	g.bridge.pushCmd(pageCmd{C: "focus"})
}

// sendText mengirim satu pesan ke seluruh anggota dengan ID yang sama.
// id dipakai ulang dari balon yang sudah digambar halaman saat Enter ditekan,
// supaya pesan sendiri langsung tampil tanpa menunggu perjalanan Go -> halaman.
func (g *GroupConv) sendText(id, text string) {
	sent := g.app.hub.ClampText(text)
	gembar := id != "" && sent == text
	if !gembar {
		id = newMsgID()
	}
	if sent == "" {
		if gembar {
			g.bridge.pushCmd(dropCmd(id))
		}
		g.appendSys(T("Pesan terlalu panjang dan tidak dikirim."))
		return
	}
	now := time.Now().Unix()
	m := chatMessage{TS: now, Dir: "out", Who: "Saya", Text: sent, ID: id, State: "pending"}
	if gembar {
		g.recordMsg(m)
	} else {
		g.appendMsg(m)
	}
	g.broadcastMsg(id, sent, now)
}

// recordMsg menyimpan pesan di daftar jendela tanpa mengirimnya ke halaman.
func (g *GroupConv) recordMsg(m chatMessage) {
	g.mu.Lock()
	g.messages = append(g.messages, m)
	g.mu.Unlock()
}

func (g *GroupConv) broadcastMsg(id, text string, ts int64) {
	g.mu.Lock()
	gc := proto.GroupChange{ID: g.id, Name: g.name, Owner: g.owner,
		Version: g.version, Members: append([]string(nil), g.members...)}
	g.mu.Unlock()
	if !gc.Valid() {
		gc.Owner = g.app.hub.MyUID()
		if gc.Version < 1 {
			gc.Version = 1
		}
	}
	_ = store.AppendGroupHistory(g.id, store.Entry{TS: ts, Dir: "out", Kind: "msg", Text: text, ID: id, State: "pending"})
	go func() {
		okN, gagal, err := g.app.hub.SendGroupMsg(gc, id, text)
		g.app.mw.Synchronize(func() {
			if err != nil {
				g.appendSys(T("Pesan belum terkirim; akan dicoba lagi saat rekan online."))
				return
			}
			if okN > 0 {
				g.setState([]string{id}, "sent")
			}
			if len(gagal) > 0 {
				names := make([]string, 0, len(gagal))
				for _, uid := range gagal {
					names = append(names, g.app.displayNameForUID(uid))
				}
				g.appendSys(T("Tidak terkirim ke: ") + strings.Join(names, ", "))
			}
		})
	}()
}

// ---------- antrean berkas grup ----------

func (g *GroupConv) pickFiles() {
	var paths []string
	for {
		dlg := walk.FileDialog{Title: T("Pilih file untuk dikirim") + " — " + g.name, Filter: "Semua File (*.*)|*.*"}
		ok, err := dlg.ShowOpen(g.win)
		if !ok || err != nil {
			break
		}
		paths = append(paths, dlg.FilePath)
		if walk.MsgBox(g.win, T("Kirim berkas lain?"),
			T("Kirim berkas lain juga? Tekan Ya untuk menambah ke antrean."),
			walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			break
		}
	}
	if len(paths) > 0 {
		g.queueFiles(paths)
	}
}

func (g *GroupConv) queueFiles(paths []string) {
	lokos, terlewat := berkasBolehDikirim(paths)
	if len(terlewat) > 0 {
		g.appendSys(fmt.Sprintf(T("Berkas melebihi batas %d MB dan tidak dikirim: %s"),
			maxTransferSize>>20, strings.Join(terlewat, ", ")))
	}
	if len(lokos) == 0 {
		return
	}
	g.mu.Lock()
	busy := g.sendCtl != nil
	g.sendQueue = append(g.sendQueue, lokos...)
	g.mu.Unlock()
	if !busy {
		g.runQueue()
	}
}

// runQueue mengirim satu berkas ke SEMUA anggota, lalu lanjut ke berkas
// berikutnya. Tombol batal menghentikan berkas yang sedang berjalan.
func (g *GroupConv) runQueue() {
	g.mu.Lock()
	if len(g.sendQueue) == 0 {
		g.sendCtl = nil
		g.mu.Unlock()
		g.bridge.pushCmd(pageCmd{C: "cancelbtn", On: false})
		return
	}
	path := g.sendQueue[0]
	g.sendQueue = g.sendQueue[1:]
	ctl := p2p.NewSendCtl()
	g.sendCtl = ctl
	members := append([]string(nil), g.members...)
	g.mu.Unlock()

	name := filepath.Base(path)
	size := fileSizeOf(path)
	g.bridge.pushCmd(pageCmd{C: "cancelbtn", On: true})
	g.setFileProgressText(fmt.Sprintf(T("Mengirim %s ke grup ..."), name))
	go func() {
		var gagal []string
		for _, uid := range members {
			if ctl.Stopped() {
				break
			}
			if err := g.app.hub.SendFileGroupCtl(uid, path, g.id, ctl); err != nil && !p2p.IsBatal(err) {
				gagal = append(gagal, g.app.displayNameForUID(uid))
			}
		}
		g.app.mw.Synchronize(func() {
			g.mu.Lock()
			g.sendCtl = nil
			g.mu.Unlock()
			if ctl.Stopped() {
				g.appendSys(T("Pengiriman dibatalkan."))
			} else if len(gagal) > 0 {
				g.appendSys(T("Tidak terkirim ke: ") + strings.Join(gagal, ", "))
			} else {
				id := newMsgID()
				_ = store.AppendGroupHistory(g.id, store.Entry{TS: time.Now().Unix(), Dir: "out",
					Kind: "file", ID: id, State: "sent",
					Text: fmt.Sprintf("%s|%d|%s", name, size, path)})
				g.appendMsg(chatMessage{TS: time.Now().Unix(), Dir: "out", Who: "Saya", Text: name, ID: id,
					State: "sent", Att: &chatAttach{Name: name, Size: size, Path: path, IsPhoto: isPhotoName(name)}})
			}
			g.clearFileProgress()
			g.runQueue()
		})
	}()
}

func (g *GroupConv) cancelSend() {
	g.mu.Lock()
	ctl := g.sendCtl
	waiting := len(g.sendQueue)
	g.sendQueue = nil
	g.mu.Unlock()
	if ctl == nil {
		return
	}
	ctl.Cancel()
	if waiting > 0 {
		g.appendSys(fmt.Sprintf(T("Antrean kirim dibatalkan (%d berkas tidak jadi dikirim)."), waiting))
		return
	}
	g.appendSys(T("Pengiriman dibatalkan."))
}

// ---------- tampil di layar ----------

func (g *GroupConv) appendMsg(m chatMessage) {
	g.mu.Lock()
	g.messages = append(g.messages, m)
	g.mu.Unlock()
	g.bridge.pushCmd(pageCmd{C: "add", M: []pageMsg{newPageMsg(m)}})
}

func (g *GroupConv) appendIn(nick, text string, ts int64, id string) {
	g.appendMsg(chatMessage{TS: ts, Dir: "in", Who: nick, Text: text, ID: id})
}

func (g *GroupConv) appendAttachment(who, name string, size int64, path string) {
	g.appendMsg(chatMessage{TS: time.Now().Unix(), Dir: "in", Who: who, Text: name,
		Att: &chatAttach{Name: name, Size: size, Path: path, IsPhoto: isPhotoName(name)}})
}

func (g *GroupConv) appendSys(text string) {
	g.appendMsg(chatMessage{TS: time.Now().Unix(), Dir: "sys", Who: "Info", Text: text})
}

func (g *GroupConv) clearView() {
	g.mu.Lock()
	g.messages = nil
	g.mu.Unlock()
	g.bridge.pushCmd(pageCmd{C: "clear"})
	g.appendSys(T("Layar obrolan dibersihkan; riwayat tetap tersimpan."))
}

func (g *GroupConv) setState(ids []string, state string) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	g.mu.Lock()
	for i := range g.messages {
		m := &g.messages[i]
		if m.Dir == "out" && m.ID != "" && want[m.ID] && m.State != "read" {
			m.State = state
		}
	}
	g.mu.Unlock()
	_ = store.SetGroupHistoryState(g.id, ids, state)
	g.bridge.pushCmd(pageCmd{C: "ticks", IDs: ids, ST: state})
}

func (g *GroupConv) setFileProgressText(text string) {
	g.bridge.pushCmd(pageCmd{C: "status", T: text})
}

func (g *GroupConv) clearFileProgress() {
	g.setFileProgressText("")
	g.bridge.pushCmd(pageCmd{C: "cancelbtn", On: false})
}

// setName memperbarui judul jendela setelah grup berganti nama.
func (g *GroupConv) setName(name string, version int64) {
	g.mu.Lock()
	g.name, g.version = name, version
	n := len(g.members)
	g.mu.Unlock()
	_ = g.win.SetTitle(T("Grup") + " — " + name + "  (" + fmt.Sprint(n) + ")")
}
