//go:build windows

package ui

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"belgindo/internal/p2p"
	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// centerWindow mengatur posisi jendela agar selalu di tengah layar monitor.
func centerWindow(w *walk.MainWindow) {
	if w == nil {
		return
	}
	var r win.RECT
	if !win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&r), 0) {
		return
	}
	sz := w.SizePixels()
	workW := int(r.Right - r.Left)
	workH := int(r.Bottom - r.Top)
	x := int(r.Left) + (workW-sz.Width)/2
	y := int(r.Top) + (workH-sz.Height)/2
	if x < int(r.Left) {
		x = int(r.Left)
	}
	if y < int(r.Top) {
		y = int(r.Top)
	}
	_ = w.SetBoundsPixels(walk.Rectangle{X: x, Y: y, Width: sz.Width, Height: sz.Height})
}

// centerWindowOnOwner memusatkan jendela tambahan di atas jendela utama.
func centerWindowOnOwner(w *walk.MainWindow, owner walk.Form) {
	if w == nil || owner == nil {
		return
	}
	var r win.RECT
	if !win.GetWindowRect(owner.Handle(), &r) {
		return
	}
	sz := w.SizePixels()
	x := int(r.Left) + (int(r.Right-r.Left)-sz.Width)/2
	y := int(r.Top) + (int(r.Bottom-r.Top)-sz.Height)/2
	_ = w.SetBoundsPixels(walk.Rectangle{X: x, Y: y, Width: sz.Width, Height: sz.Height})
}

type chatMessage struct {
	TS    int64
	Dir   string // "in", "out", "sys"
	Who   string
	Text  string
	ID    string // id pesan keluar, dipakai untuk tanda terima baca
	State string // "" / "pending" / "sent" / "read"
	Att   *chatAttach
}

// Convo adalah satu jendela percakapan dengan satu rekan. Isi yang tampil
// adalah riwayat HARI INI: ditutup dan dibuka lagi isinya masih ada, dan
// otomatis berganti kosong saat tanggal berganti (lihat chatview.go).
type Convo struct {
	app *App
	uid string

	win    *walk.MainWindow
	wv     *walk.WebView
	bridge pageBridge

	mu           sync.Mutex
	nick         string
	pc           string
	htmlPath     string
	messages     []chatMessage
	viewDay      string   // tanggal yang sedang tampil di layar
	pendingRead  []string // id pesan masuk yang belum sempat dilaporkan terbaca
	typingSentAt time.Time
	typingHide   *time.Timer

	// antrean kirim berkas dengan tombol batal
	sendCtl   *p2p.SendCtl
	sendQueue []string
}

func (a *App) convoIfOpen(uid string) *Convo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.convos[uid]
}

// peerInfo mengambil info peer dari snapshot hub.
func (a *App) peerInfo(uid string) (nick, pc string, online bool) {
	for _, p := range a.hub.SnapshotPeers() {
		if p.UID == uid {
			return a.displayPeerName(p), p.PC, p.Online
		}
	}
	return uid, "", false
}

func writeChatPageFile(path string, pageHTML []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(150 * time.Millisecond)
		}
		if err := os.WriteFile(path, pageHTML, 0o600); err != nil {
			lastErr = err
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			lastErr = err
			continue
		}
		if info.Size() != int64(len(pageHTML)) {
			lastErr = fmt.Errorf("ukuran halaman chat tidak sesuai (%d != %d)", info.Size(), len(pageHTML))
			continue
		}
		return nil
	}
	return lastErr
}

// sameLocalPage menerima beberapa bentuk URL file:// yang dikembalikan
// MSHTML/Walk untuk path Windows.
func sameLocalPage(navURL, path string) bool {
	if navURL == "" || path == "" {
		return false
	}
	u := strings.TrimPrefix(navURL, "file://")
	u = strings.TrimPrefix(u, "file:")
	if dec, err := url.PathUnescape(u); err == nil {
		u = dec
	}
	u = strings.ReplaceAll(u, "/", "\\")
	u = strings.TrimPrefix(u, "\\")
	if strings.EqualFold(u, path) {
		return true
	}
	fwd := strings.ReplaceAll(path, "\\", "/")
	return strings.EqualFold(strings.TrimPrefix(navURL, "file:///"), fwd)
}

// writePage menulis halaman awal (kosong) dan memuatnya ke WebView.
func (c *Convo) writePage() error {
	c.mu.Lock()
	msgs := append([]chatMessage(nil), c.messages...)
	nick, pc := c.nick, c.pc
	c.mu.Unlock()
	if pc == "" {
		pc = "Belgindo Office Messenger"
	}
	page := buildChatPageHTML(chatPageOpts{
		Name: nick, Sub: pc, Typing: true,
		Placeholder: T("Tulis pesan, Enter kirim, Ctrl+Enter baris baru"), Messages: msgs,
	})
	if err := writeChatPageFile(c.htmlPath, []byte(page)); err != nil {
		return err
	}
	c.bridge.reset()
	if c.wv != nil {
		return c.wv.SetURL(c.htmlPath)
	}
	return nil
}

// Ukuran jendela percakapan. Base-nya 460x480; permintaan sekarang adalah
// lebar -15% dan tinggi -10%, jadi MinSize ikut diturunkan agar jendela tidak
// dikembalikan (clamped) ke ukuran lama oleh Windows.
const (
	convoMinW, convoMinH = 357, 414
	convoW, convoH       = 391, 432
)

func (a *App) openConvo(uid string) {
	if !a.unlockGate(uid) {
		return
	}
	if c := a.convoIfOpen(uid); c != nil {
		c.win.Show()
		c.win.Activate()
		c.flushRead()
		a.clearUnread(uid)
		return
	}
	nick, pc, _ := a.peerInfo(uid)
	c := &Convo{app: a, uid: uid, nick: nick, pc: pc}
	c.htmlPath = filepath.Join(store.Dir(), "chat_"+store.SafeKey(uid)+".html")
	c.seedToday()
	title := T("Chat") + " — " + nick
	if pc != "" {
		title += "  (" + pc + ")"
	}
	if err := (MainWindow{AssignTo: &c.win, Title: title,
		MinSize: Size{Width: convoMinW, Height: convoMinH}, Size: Size{Width: convoW, Height: convoH},
		Font: Font{Family: "Segoe UI", PointSize: 9}, Layout: VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{WebView{AssignTo: &c.wv}},
	}).Create(); err != nil {
		a.balloon(T("Gagal"), T("Tidak bisa membuka jendela chat: ")+err.Error())
		return
	}
	_ = c.win.SetIcon(a.iconApp)
	c.attachHandlers()
	if err := c.writePage(); err != nil {
		c.win.Dispose()
		a.balloon(T("Gagal"), err.Error())
		return
	}
	c.win.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.mu.Lock()
		delete(a.convos, c.uid)
		a.mu.Unlock()
		_ = os.Remove(c.htmlPath)
	})
	a.mu.Lock()
	a.convos[uid] = c
	a.mu.Unlock()
	centerWindow(c.win)
	c.win.Show()
	c.win.Activate()
	c.flushRead()
	a.clearUnread(uid)
	go a.retryPendingOut(uid)
}

// attachHandlers memasang jembatan halaman + terima drag & drop berkas.
func (c *Convo) attachHandlers() {
	c.bridge.attach(c.wv)
	c.wv.Navigating().Attach(func(e *walk.WebViewNavigatingEventData) {
		c.handleNavigate(e)
	})
	c.wv.DocumentTitleChanged().Attach(func() {
		if id, text, ok := parseSendTitle(c.wv.DocumentTitle()); ok {
			c.sendText(id, text)
		}
	})
	// Navigasi "javascript:" terakhir sudah selesai: boleh lepas batch berikut.
	c.wv.Navigated().Attach(func(string) { c.bridge.finish() })
	for _, d := range []struct {
		src interface{ DropFiles() *walk.DropFilesEvent }
	}{{c.win}, {c.wv}} {
		d.src.DropFiles().Attach(func(paths []string) { c.dropFiles(paths) })
	}
}

func (c *Convo) handleNavigate(e *walk.WebViewNavigatingEventData) {
	u := appURL(e.Url())
	switch {
	case u == "app://ready":
		e.SetCanceled(true)
		c.bridge.markReady()
	case u == "app://typing":
		e.SetCanceled(true)
		c.noteTypingLocal()
	case u == "app://emoji":
		e.SetCanceled(true)
		c.app.mw.Synchronize(func() { showEmojiPicker(c.win, c.insertEmoji) })
	case u == "app://attach":
		e.SetCanceled(true)
		c.app.mw.Synchronize(c.pickFiles)
	case u == "app://cancel":
		e.SetCanceled(true)
		c.cancelSend()
	case u == "app://clearchat":
		e.SetCanceled(true)
		c.clearView()
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
			c.appendSys(msg)
		}
	default:
		if u == "about:blank" || strings.HasPrefix(u, "javascript:") || sameLocalPage(u, c.htmlPath) {
			return
		}
		e.SetCanceled(true)
		// Tautan luar dibuka di browser standar, bukan di dalam jendela chat.
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			_ = bukaBerkasBawaan(u)
		}
	}
}

func (c *Convo) insertEmoji(s string) {
	c.bridge.pushCmd(pageCmd{C: "insert", T: s})
	c.bridge.pushCmd(pageCmd{C: "focus"})
}

func (c *Convo) noteTypingLocal() {
	c.mu.Lock()
	due := time.Since(c.typingSentAt) > 1500*time.Millisecond
	if due {
		c.typingSentAt = time.Now()
	}
	c.mu.Unlock()
	if due {
		go c.app.hub.SendTyping(c.uid)
	}
}

// sendText mengirim pesan teks. id adalah ID balon yang sudah digambar
// halaman saat Enter ditekan (lihat submit() di chatpage.go); balon itu tidak
// digambar ulang dari Go agar pesan sendiri muncul seketika.
func (c *Convo) sendText(id, text string) {
	// Jangan biarkan balon lokal menampilkan teks yang tidak akan pernah
	// diterima rekan karena melebihi batas protokol.
	sent := c.app.hub.ClampText(text)
	gembar := id != "" && sent == text
	if !gembar {
		id = newMsgID()
	}
	if sent == "" {
		if gembar {
			c.bridge.pushCmd(dropCmd(id))
		}
		c.appendSys(T("Pesan terlalu panjang dan tidak dikirim."))
		return
	}
	now := time.Now().Unix()
	m := chatMessage{TS: now, Dir: "out", Who: "Saya", Text: sent, ID: id, State: "pending"}
	if gembar {
		c.recordMsg(m)
	} else {
		c.appendMsg(m)
	}
	_ = store.AppendHistory(c.uid, store.Entry{TS: now, Dir: "out", Kind: "msg", Text: sent, ID: id, State: "pending"})
	go func() {
		_, err := c.app.hub.SendMsg(c.uid, id, sent)
		c.app.mw.Synchronize(func() {
			if err != nil {
				c.appendSys(T("Pesan belum terkirim; akan dicoba lagi saat rekan online."))
				return
			}
			c.setState([]string{id}, "sent")
		})
	}()
}

// recordMsg menyimpan pesan ke daftar jendela tanpa mengirimkannya ke
// halaman; dipakai untuk gema lokal yang sudah digambar JS.
func (c *Convo) recordMsg(m chatMessage) {
	c.mu.Lock()
	c.messages = append(c.messages, m)
	c.mu.Unlock()
}

// newMsgID membuat penanda unik untuk satu pesan keluar.
func newMsgID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("%x", b)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// ---------- kirim berkas: pilihan banyak, antrean, batal, drag & drop ----------

func (c *Convo) pickFiles() {
	var paths []string
	for {
		dlg := walk.FileDialog{
			Title:  T("Pilih file untuk dikirim") + " — " + c.nick,
			Filter: "Semua File (*.*)|*.*",
		}
		ok, err := dlg.ShowOpen(c.win)
		if !ok || err != nil {
			break
		}
		paths = append(paths, dlg.FilePath)
		if walk.MsgBox(c.win, T("Kirim berkas lain?"),
			T("Kirim berkas lain juga? Tekan Ya untuk menambah ke antrean."),
			walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			break
		}
	}
	if len(paths) > 0 {
		c.queueFiles(paths)
	}
}

func (c *Convo) dropFiles(paths []string) {
	paths = expandDropPaths(paths)
	if len(paths) == 0 {
		return
	}
	c.app.mw.Synchronize(func() { c.queueFiles(paths) })
}

// queueFiles menyusun antrean kirim; berkas yang terlalu besar ditolak di muka.
func (c *Convo) queueFiles(paths []string) {
	lokos, terlewat := berkasBolehDikirim(paths)
	if len(terlewat) > 0 {
		c.appendSys(fmt.Sprintf(T("Berkas melebihi batas %d MB dan tidak dikirim: %s"),
			maxTransferSize>>20, strings.Join(terlewat, ", ")))
	}
	if len(lokos) == 0 {
		return
	}
	c.mu.Lock()
	busy := c.sendCtl != nil
	c.sendQueue = append(c.sendQueue, lokos...)
	c.mu.Unlock()
	if busy {
		c.appendSys(fmt.Sprintf(T("Ditambahkan ke antrean kirim (%d berkas menunggu)."), len(lokos)))
		return
	}
	c.runQueue()
}

// runQueue mengirim antrean satu per satu; tombol batal menghentikan berkas
// berjalan sementara berkas yang sudah selesai tetap terkirim.
func (c *Convo) runQueue() {
	c.mu.Lock()
	if len(c.sendQueue) == 0 {
		c.sendCtl = nil
		c.mu.Unlock()
		c.bridge.pushCmd(pageCmd{C: "cancelbtn", On: false})
		return
	}
	path := c.sendQueue[0]
	c.sendQueue = c.sendQueue[1:]
	ctl := p2p.NewSendCtl()
	c.sendCtl = ctl
	c.mu.Unlock()

	name := filepath.Base(path)
	c.bridge.pushCmd(pageCmd{C: "cancelbtn", On: true})
	c.setFileProgressText(fmt.Sprintf(T("Mengirim %s ..."), name))
	c.app.txStart("", "out", c.nick, name, fileSizeOf(path))
	go func() {
		err := c.app.hub.SendFileCtl(c.uid, path, ctl)
		c.app.mw.Synchronize(func() {
			c.mu.Lock()
			c.sendCtl = nil
			c.mu.Unlock()
			if err != nil {
				if !p2p.IsBatal(err) {
					c.appendSys(fmt.Sprintf(T("Gagal mengirim file %s: %s"), name, err.Error()))
				}
			} else {
				c.appendMsg(chatMessage{TS: time.Now().Unix(), Dir: "out", Who: "Saya",
					Att:  &chatAttach{Name: name, Size: fileSizeOf(path), Path: path, IsPhoto: isPhotoName(name)},
					Text: name})
			}
			c.clearFileProgress()
			c.runQueue()
		})
	}()
}

func (c *Convo) cancelSend() {
	c.mu.Lock()
	ctl := c.sendCtl
	waiting := len(c.sendQueue)
	c.sendQueue = nil
	c.mu.Unlock()
	if ctl == nil {
		return
	}
	ctl.Cancel()
	if waiting > 0 {
		c.appendSys(fmt.Sprintf(T("Antrean kirim dibatalkan (%d berkas tidak jadi dikirim)."), waiting))
		return
	}
	c.appendSys(T("Pengiriman dibatalkan."))
}

func fileSizeOf(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// ---------- tampil di layar ----------

func (c *Convo) appendIn(text string, ts int64, id string) {
	m := chatMessage{TS: ts, Dir: "in", Who: c.nick, Text: text, ID: id}
	c.appendMsg(m)
	c.markSeen(id)
}

func (c *Convo) appendMsg(m chatMessage) {
	c.mu.Lock()
	c.messages = append(c.messages, m)
	c.mu.Unlock()
	c.bridge.pushCmd(pageCmd{C: "add", M: []pageMsg{newPageMsg(m)}})
}

func (c *Convo) appendSys(text string) {
	c.appendMsg(chatMessage{TS: time.Now().Unix(), Dir: "sys", Who: "Info", Text: text})
}

// appendAttachment menampilkan berkas/foto yang baru diterima atau terkirim.
func (c *Convo) appendAttachment(dir, name string, size int64, path string) {
	m := chatMessage{TS: time.Now().Unix(), Dir: dir, Text: name,
		Att: &chatAttach{Name: name, Size: size, Path: path, IsPhoto: isPhotoName(name)}}
	if dir == "in" {
		m.Who = c.nick
	} else {
		m.Who = "Saya"
	}
	c.appendMsg(m)
}

// clearView mengosongkan layar obrolan tanpa menghapus riwayat.
func (c *Convo) clearView() {
	c.mu.Lock()
	c.messages = nil
	c.mu.Unlock()
	c.bridge.pushCmd(pageCmd{C: "clear"})
	c.appendSys(T("Layar obrolan dibersihkan; riwayat tetap tersimpan."))
}

// markSeen melaporkan bahwa pesan masuk sudah dibaca.
func (c *Convo) markSeen(id string) {
	if id == "" {
		return
	}
	if c.app.windowActive(c.win) {
		go c.app.hub.SendRead(c.uid, []string{id})
		return
	}
	c.mu.Lock()
	c.pendingRead = append(c.pendingRead, id)
	c.mu.Unlock()
}

// flushRead mengirim semua laporan baca yang tertunda.
func (c *Convo) flushRead() {
	c.mu.Lock()
	ids := c.pendingRead
	c.pendingRead = nil
	c.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	go c.app.hub.SendRead(c.uid, ids)
}

// setState menaikkan status pesan keluar; "read" tidak pernah mundur.
func (c *Convo) setState(ids []string, state string) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	c.mu.Lock()
	changed := false
	for i := range c.messages {
		m := &c.messages[i]
		if m.Dir != "out" || m.ID == "" || !want[m.ID] || m.State == state || m.State == "read" {
			continue
		}
		m.State = state
		changed = true
	}
	c.mu.Unlock()
	if !changed {
		return
	}
	_ = store.SetHistoryState(c.uid, ids, state)
	c.bridge.pushCmd(pageCmd{C: "ticks", IDs: ids, ST: state})
}

func (c *Convo) showTyping() {
	c.bridge.pushCmd(pageCmd{C: "typing", On: true})
	c.mu.Lock()
	if c.typingHide != nil {
		c.typingHide.Stop()
	}
	c.typingHide = time.AfterFunc(2500*time.Millisecond, func() {
		c.app.mw.Synchronize(func() {
			c.bridge.pushCmd(pageCmd{C: "typing", On: false})
		})
	})
	c.mu.Unlock()
}

func (c *Convo) updateTitle(nick, pc string) {
	c.mu.Lock()
	c.nick, c.pc = nick, pc
	c.mu.Unlock()
	title := T("Chat") + " — " + nick
	if pc != "" {
		title += "  (" + pc + ")"
	}
	_ = c.win.SetTitle(title)
	c.bridge.pushCmd(pageCmd{C: "peer", N: nick, S: pc})
}

func (c *Convo) setFileProgress(e p2p.Event) {
	if e.Dir == p2p.DirOut {
		c.setFileProgressText(fmt.Sprintf(T("Mengirim %s: %.0f%%"), e.FileName, e.Percent))
	} else {
		c.setFileProgressText(fmt.Sprintf(T("Menerima %s: %.0f%%"), e.FileName, e.Percent))
	}
}

func (c *Convo) clearFileProgress() {
	c.setFileProgressText("")
	c.bridge.pushCmd(pageCmd{C: "cancelbtn", On: false})
}

func (c *Convo) setFileProgressText(text string) {
	c.bridge.pushCmd(pageCmd{C: "status", T: text})
}

// retryPendingOut mengirim ulang pesan keluar yang masih berstatus pending
// begitu rekan kembali online (outbox).
func (a *App) retryPendingOut(uid string) {
	var ids []string
	var texts []string
	for _, e := range store.LoadHistory(uid, 300) {
		if e.Dir == "out" && e.Kind == "msg" && e.State == "pending" && e.ID != "" {
			ids = append(ids, e.ID)
			texts = append(texts, e.Text)
		}
	}
	if len(ids) == 0 {
		return
	}
	sent := make([]string, 0, len(ids))
	for i, id := range ids {
		if _, err := a.hub.SendMsg(uid, id, texts[i]); err != nil {
			continue
		}
		sent = append(sent, id)
	}
	if len(sent) == 0 {
		return
	}
	_ = store.SetHistoryState(uid, sent, "sent")
	a.mu.Lock()
	c := a.convos[uid]
	a.mu.Unlock()
	if c != nil {
		a.mw.Synchronize(func() {
			c.setState(sent, "sent")
			c.appendSys(fmt.Sprintf(T("%d pesan tertunda berhasil dikirim ulang."), len(sent)))
		})
	}
}
