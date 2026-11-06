//go:build windows

// Package ui menggambar seluruh antarmuka Belgindo Office Messenger dengan
// pustaka walk (Win32 native): jendela utama, tray icon + balon notifikasi,
// jendela percakapan, grup, riwayat, log transfer, dan dialog pengaturan.
package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
	"unsafe"

	"belgindo/internal/netutil"
	"belgindo/internal/p2p"
	"belgindo/internal/proto"
	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	modUser32Idle        = windows.NewLazySystemDLL("user32.dll")
	procGetLastInputInfo = modUser32Idle.NewProc("GetLastInputInfo")
	modKernel32Idle      = windows.NewLazySystemDLL("kernel32.dll")
	procGetTickCount64   = modKernel32Idle.NewProc("GetTickCount64")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

func getIdleDuration() time.Duration {
	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))
	r1, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if r1 == 0 {
		return 0
	}
	r0, _, _ := procGetTickCount64.Call()
	now := uint32(r0)
	idleMs := now - lii.dwTime
	return time.Duration(idleMs) * time.Millisecond
}

const spiGetWorkArea = 0x0030

// Lebar (dalam DIP) di bawah mana kolom status disembunyikan.
const narrowWidth = 210

// Ukuran awal jendela utama (DIP), menempel di pojok kanan bawah di atas tray.
const (
	wantWidth  = 300
	wantHeight = 477
)

// statusBoxWidth adalah lebar kolom kanan header (ikon + keterangan status).
const statusBoxWidth = 66

// midMax adalah lebar maksimum blok nama+pesan status pada header. Pesan status
// harus muat ditulis tanpa membuka pengaturan.
const midMax = 118

// maxNickLen adalah panjang maksimum nama tampilan yang boleh ditulis.
const maxNickLen = 24

// App menyimpan seluruh state antarmuka.
type App struct {
	cfg *store.Config
	hub *p2p.Hub

	mw       *walk.MainWindow
	ni       *walk.NotifyIcon
	tv       *walk.TableView
	header   *walk.Composite
	lblNet   *walk.TextLabel
	ivStatus *walk.ImageView // ikon status sendiri (menu lewat tray / klik kanan)
	hdrMid   *walk.Composite // blok nama + keterangan status di bawahnya
	lblStat  *walk.TextLabel // teks status sendiri, di bawah ikon status
	leNick   *walk.LineEdit  // nama tampilan, langsung ditulis di header
	leStatus *walk.LineEdit  // pesan status, langsung ditulis di header
	ivAvatar *walk.ImageView // foto profil / avatar sendiri, di kiri nama

	iconNormal *walk.Icon
	iconAlert  *walk.Icon
	iconApp    *walk.Icon

	balloonWin *TrayBalloon // balon notifikasi mengambang di pojok kanan bawah

	model *userModel
	icons *iconCache

	// collapsed berisi kunci bagian daftar yang sedang ditutup ("teman" / ID grup)
	collapsed map[string]bool
	fontBold  *walk.Font

	// aksi menu dropdown status (checkmark diatur ulang oleh refreshHeader)
	actStatus [proto.StatCount]*walk.Action

	mu                 sync.Mutex
	convos             map[string]*Convo
	groups             map[string]*GroupConv
	groupDefs          []store.Group
	transfers          []store.Transfer
	txLive             map[string]int // fid -> indeks baris transfers
	txWin              *walk.MainWindow
	txTV               *walk.TableView
	txModel            *transferModel
	histWin            *walk.MainWindow
	unread             map[string]int
	lastMsgUID         string
	toldTray           bool
	quitting           bool
	isAutoAway         bool
	prevStatBeforeAway int
	localIPs           string
	pc                 string
	viewDay            string // tanggal yang sedang tampil di ruang chat terbuka
	// outboxBusy mencegah dua disapu outbox berjalan bersamaan.
	outboxBusy atomic.Bool
}

// Run menjalankan seluruh aplikasi (blokir sampai keluar).
func Run(cfg *store.Config, hub *p2p.Hub, pc string, startInTray bool) error {
	a := &App{
		cfg:    cfg,
		hub:    hub,
		pc:     pc,
		convos: make(map[string]*Convo),
		groups: make(map[string]*GroupConv),
		unread: make(map[string]int),
		txLive: make(map[string]int),
		icons:  newIconCache(),
	}
	a.model = &userModel{app: a}
	a.collapsed = map[string]bool{}
	for _, k := range cfg.Collapsed {
		a.collapsed[k] = true
	}
	a.fontBold, _ = walk.NewFont("Segoe UI", 9, walk.FontBold)
	a.groupDefs = store.LoadGroups()
	a.transfers = store.LoadTransfers(400)

	err := (MainWindow{
		AssignTo:      &a.mw,
		Title:         "Belgindo Office Messenger",
		MinSize:       Size{Width: 200, Height: 280},
		Size:          Size{Width: wantWidth, Height: wantHeight},
		Font:          Font{Family: "Segoe UI", PointSize: 9},
		Background:    SolidColorBrush{Color: walk.RGB(0xF3, 0xF6, 0xFB)},
		Layout:        VBox{Margins: Margins{Left: 6, Top: 6, Right: 6, Bottom: 6}, Spacing: 4},
		OnSizeChanged: a.layoutMain,
		MenuItems: []MenuItem{
			Menu{Text: T("Chat"), Items: []MenuItem{
				Action{Text: T("Buat &Grup..."), OnTriggered: a.createGroup},
				Separator{},
				Action{Text: T("&Refresh"), OnTriggered: a.refreshAll},
				Separator{},
				Action{Text: T("&Keluar"), OnTriggered: a.quitApp},
			}},
			Menu{Text: T("Peng&aturan"), Items: []MenuItem{
				Action{Text: T("&Riwayat..."), OnTriggered: a.showHistory},
				Action{Text: T("Transfer &File..."), OnTriggered: a.showTransfers},
				Separator{},
				Action{Text: T("&Pengaturan..."), OnTriggered: a.showSettings},
			}},
			Menu{Text: T("Ban&tu"), Items: []MenuItem{
				Menu{Text: T("Pilih Bahasa"), Items: a.languageMenuItems()},
				Separator{},
				Action{Text: T("Tentang &Belgindo..."), OnTriggered: a.showAbout},
			}},
		},
		Children: []Widget{
			// ---- header: avatar di kiri, nama di tengah, status di kanan ----
			Composite{
				AssignTo:   &a.header,
				Background: SolidColorBrush{Color: walk.RGB(0x1F, 0x4E, 0x79)},
				MaxSize:    Size{Height: 58},
				Layout:     HBox{Margins: Margins{Left: 8, Top: 6, Right: 8, Bottom: 6}, Spacing: 6},
				Children: []Widget{
					ImageView{
						AssignTo:    &a.ivAvatar,
						MinSize:     Size{Width: 34, Height: 34},
						MaxSize:     Size{Width: 34, Height: 34},
						Mode:        ImageViewModeZoom,
						ToolTipText: T("Klik untuk membuka Pengaturan (foto profil, nama, dsb.)"),
						OnMouseDown: func(x, y int, btn walk.MouseButton) {
							if btn == walk.LeftButton {
								a.showSettings()
							}
						},
					},
					Composite{
						AssignTo: &a.hdrMid,
						MaxSize:  Size{Width: midMax, Height: 0},
						Layout:   VBox{MarginsZero: true, SpacingZero: true},
						Children: []Widget{
							LineEdit{
								AssignTo:    &a.leNick,
								Text:        a.hub.Nick(),
								MaxSize:     Size{Width: midMax, Height: 0},
								Background:  SolidColorBrush{Color: walk.RGB(0x2A, 0x5A, 0x86)},
								TextColor:   walk.RGB(0xFF, 0xFF, 0xFF),
								Font:        Font{Family: "Segoe UI", PointSize: 10, Bold: true},
								CueBanner:   "Nama tampilan...",
								MaxLength:   maxNickLen,
								ToolTipText: T("Nama yang dilihat rekan lain; ketik lalu tekan Enter"),
								OnEditingFinished: func() {
									a.simpanNickEdit()
								},
								OnKeyDown: func(key walk.Key) {
									if key == walk.KeyEscape {
										a.batalNickEdit()
									}
								},
							},
							LineEdit{
								AssignTo:    &a.leStatus,
								Text:        a.cfg.StatusMsg,
								MaxSize:     Size{Width: midMax, Height: 0},
								Background:  SolidColorBrush{Color: walk.RGB(0x1F, 0x4E, 0x79)},
								TextColor:   walk.RGB(0xA8, 0xC8, 0xE3),
								Font:        Font{Family: "Segoe UI", PointSize: 8, Italic: true},
								CueBanner:   "ketuk untuk membuat status",
								MaxLength:   proto.MaxStatMsg,
								ToolTipText: T("Pesan status untuk rekan lain; ketik lalu tekan Enter"),
								OnEditingFinished: func() {
									a.simpanStatusEdit()
								},
								OnKeyDown: func(key walk.Key) {
									if key == walk.KeyEscape {
										a.batalStatusEdit()
									}
								},
							},
						},
					},
					HSpacer{},
					Composite{
						MaxSize: Size{Width: statusBoxWidth, Height: 0},
						Layout:  VBox{MarginsZero: true, SpacingZero: true, Alignment: AlignHCenterVNear},
						Children: []Widget{
							ImageView{
								AssignTo:    &a.ivStatus,
								MinSize:     Size{Width: 20, Height: 20},
								MaxSize:     Size{Width: 20, Height: 20},
								Mode:        ImageViewModeZoom,
								ToolTipText: T("Klik untuk mengganti status"),
								OnMouseDown: func(x, y int, btn walk.MouseButton) {
									if btn == walk.LeftButton {
										a.showStatusMenu()
									}
								},
								ContextMenuItems: statusMenuItems(a),
							},
							Composite{
								Layout: HBox{MarginsZero: true, SpacingZero: true},
								Children: []Widget{
									TextLabel{
										AssignTo:  &a.lblStat,
										MaxSize:   Size{Width: statusBoxWidth - 12, Height: 0},
										Font:      Font{Family: "Segoe UI", PointSize: 8},
										TextColor: walk.RGB(0xCF, 0xE2, 0xF3),
										OnMouseDown: func(x, y int, btn walk.MouseButton) {
											if btn == walk.LeftButton {
												a.showStatusMenu()
											}
										},
									},
									TextLabel{
										Text:      " \u25be",
										Font:      Font{Family: "Segoe UI", PointSize: 8},
										TextColor: walk.RGB(0xCF, 0xE2, 0xF3),
										OnMouseDown: func(x, y int, btn walk.MouseButton) {
											if btn == walk.LeftButton {
												a.showStatusMenu()
											}
										},
									},
								},
							},
						},
					},
				},
			},
			TableView{
				AssignTo:      &a.tv,
				StretchFactor: 10,
				HeaderHidden:  true,
				Columns: []TableViewColumn{
					{Name: "foto", Title: " ", Width: 28},
					{Name: "nick", Title: T("Pengguna"), Width: 110},
					{Name: "status", Title: T("Status"), Width: 60},
				},
				CustomRowHeight: 30,
				Model:           a.model,
				StyleCell:       a.styleCell,
				OnMouseDown:     a.onListMouseDown,
				OnItemActivated: a.openSelected,
				ToolTipText:     T("Klik judul bagian untuk membuka/menutup daftarnya"),
				ContextMenuItems: []MenuItem{
					Action{Text: T("Buka Percakapan"), OnTriggered: a.openSelected},
					Action{Text: T("Kirim File..."), OnTriggered: a.sendFileToSelected},
					Separator{},
					Action{Text: T("Info"), OnTriggered: a.showUserInfo},
					Action{Text: T("Kunci Percakapan"), OnTriggered: a.lockSelectedConversation},
					Action{Text: T("Buka Kunci Percakapan"), OnTriggered: a.unlockSelectedConversation},
					Action{Text: T("Ubah Tampilan Nama..."), OnTriggered: a.renameSelected},
					Separator{},
					Action{Text: T("Ubah Nama Grup"), OnTriggered: a.renameSelectedGroup},
					Action{Text: T("Tambah Anggota Grup"), OnTriggered: a.addMembersSelectedGroup},
					Action{Text: T("Hapus Grup"), OnTriggered: a.deleteSelectedGroup},
					Separator{},
					Action{Text: T("Bersihkan obrolan"), OnTriggered: a.clearSelectedChat},
					Action{Text: T("Hapus Riwayat Percakapan"), OnTriggered: a.clearSelectedHistory},
					Separator{},
					Action{Text: T("Kirim ke Semua (Broadcast)"), OnTriggered: a.showBroadcast},
				},
			},
			Composite{
				Background: SolidColorBrush{Color: walk.RGB(0xE4, 0xEC, 0xF5)},
				MaxSize:    Size{Height: 26},
				Layout:     HBox{Margins: Margins{Left: 6, Top: 4, Right: 6, Bottom: 4}, Spacing: 4},
				Children: []Widget{
					TextLabel{AssignTo: &a.lblNet, Text: T("Menyiapkan jaringan...")},
				},
			},
		},
	}).Create()
	if err != nil {
		return fmt.Errorf("gagal membuat jendela: %w", err)
	}
	// LineEdit Win32 membawa border 3D putih dari theme sistem. Hilangkan
	// style tersebut agar kedua field menyatu dengan biru background header.
	for _, edit := range []*walk.LineEdit{a.leNick, a.leStatus} {
		if edit == nil {
			continue
		}
		style := win.GetWindowLong(edit.Handle(), win.GWL_STYLE)
		style &^= int32(win.WS_BORDER)
		win.SetWindowLong(edit.Handle(), win.GWL_STYLE, style)
		exStyle := win.GetWindowLong(edit.Handle(), win.GWL_EXSTYLE)
		exStyle &^= int32(win.WS_EX_CLIENTEDGE)
		win.SetWindowLong(edit.Handle(), win.GWL_EXSTYLE, exStyle)
		win.SetWindowPos(edit.Handle(), 0, 0, 0, 0, 0,
			win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
	}

	// ikon: jendela/taskbar + tray, semuanya dari PNG ter-embed
	if a.iconApp, err = walk.NewIconFromImage(decodePNG(pngAppIcon)); err != nil {
		return fmt.Errorf("gagal memuat ikon aplikasi: %w", err)
	}
	if a.iconNormal, err = walk.NewIconFromImage(decodePNG(pngNormal)); err != nil {
		return fmt.Errorf("gagal memuat ikon: %w", err)
	}
	if a.iconAlert, err = walk.NewIconFromImage(decodePNG(pngAlert)); err != nil {
		a.iconAlert = a.iconNormal
	}
	_ = a.mw.SetIcon(a.iconApp)

	if a.ni, err = walk.NewNotifyIcon(a.mw); err != nil {
		return fmt.Errorf("gagal membuat ikon tray: %w", err)
	}
	_ = a.ni.SetIcon(a.iconNormal)
	_ = a.ni.SetToolTip("Belgindo Office Messenger")
	_ = a.ni.SetVisible(true)
	a.ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.toggleMain()
		}
	})
	a.ni.MessageClicked().Attach(func() {
		if a.lastMsgUID != "" {
			a.openConvo(a.lastMsgUID)
		} else {
			a.toggleMain()
		}
	})
	a.balloonWin = newTrayBalloon(a)
	a.buildTrayMenu()

	// tombol X = minimize ke tray, aplikasi tetap berjalan
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if a.isQuitting() {
			return
		}
		*canceled = true
		a.mw.Hide()
		if !a.toldTray {
			a.toldTray = true
			a.balloon("Belgindo Office Messenger",
				T("Aplikasi tetap berjalan di tray. Klik ikon tray untuk membuka lagi."))
		}
	})

	a.placeBottomRight()
	a.layoutMain()
	a.refreshHeader()
	if a.tv != nil {
		_ = a.tv.SetFocus()
	}

	// peristiwa jaringan -> thread UI
	hub.OnEvent(func(e p2p.Event) {
		a.mw.Synchronize(func() { a.onHubEvent(e) })
	})
	hub.OnSavePeers(func(ps []p2p.Peer) {
		_ = store.SavePeers(ps)
	})

	// jalankan jaringan
	a.localIPs = strings.Join(netutil.LocalIPStrings(), ", ")
	if err := hub.Start(); err != nil {
		return err
	}
	a.refreshList()
	go a.runIdleMonitor()

	if startInTray || a.cfg.StartInTray {
		a.mw.Hide()
		a.balloon("Belgindo Office Messenger", T("Berjalan di tray, siap menerima pesan."))
	} else {
		a.mw.Show()
	}
	a.mw.Run()
	return nil
}

func (a *App) pcName() string {
	if a.pc != "" {
		return a.pc
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "PC"
}

func (a *App) isQuitting() bool { return a.quitting }

// ---------- jendela: posisi & tata letak responsif ----------

// placeBottomRight menempelkan jendela ke pojok kanan bawah, di atas tray.
func (a *App) placeBottomRight() {
	var r win.RECT
	if !win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&r), 0) {
		return
	}
	sz := a.mw.SizePixels()
	x := int(r.Right) - sz.Width - 10
	y := int(r.Bottom) - sz.Height - 6
	if x < int(r.Left) {
		x = int(r.Left)
	}
	if y < int(r.Top) {
		y = int(r.Top)
	}
	_ = a.mw.SetBoundsPixels(walk.Rectangle{X: x, Y: y, Width: sz.Width, Height: sz.Height})
}

// chatWidth memberi jendela chat/grup lebar yang sama dengan jendela utama,
// dengan batas bawah selebar jendela utama saat baru dibuka.
func (a *App) chatWidth() int {
	if a.mw == nil {
		return wantWidth
	}
	if w := a.mw.Bounds().Width; w > wantWidth {
		return w
	}
	return wantWidth
}

// layoutMain menyesuaikan isi jendela setiap kali ukurannya berubah.
func (a *App) layoutMain() {
	if a.mw == nil || a.tv == nil {
		return
	}
	w := a.mw.ClientBounds().Width

	// Header: avatar 34 di kiri + blok nama/status + pegas + kolom status 66 di kanan.
	// Nama ikut menyusut agar kolom status tidak terdorong keluar di jendela sempit.
	nickMax := w - 16 - 34 - statusBoxWidth - 12
	if nickMax < 40 {
		nickMax = 40
	}
	if a.hdrMid != nil {
		_ = a.hdrMid.SetMinMaxSize(
			walk.Size{Width: 40, Height: 0},
			walk.Size{Width: nickMax, Height: 0})
	}
	for _, le := range []*walk.LineEdit{a.leNick, a.leStatus} {
		if le != nil {
			_ = le.SetMinMaxSize(
				walk.Size{Width: 40, Height: 0},
				walk.Size{Width: nickMax, Height: 0})
		}
	}

	// kolom foto: tampil bila muat, sisanya untuk nama
	fotoW := 0
	if w >= 170 {
		fotoW = 28
	}
	if col := a.tv.Columns().ByName("foto"); col != nil {
		_ = col.SetVisible(fotoW > 0)
		if fotoW > 0 {
			_ = col.SetWidth(fotoW)
		}
	}

	// kolom status hanya bila lebar cukup
	statusW := 0
	switch {
	case w >= 260:
		statusW = 62
	case w >= narrowWidth:
		statusW = 52
	}
	if col := a.tv.Columns().ByName("status"); col != nil {
		_ = col.SetVisible(statusW > 0)
		if statusW > 0 {
			_ = col.SetWidth(statusW)
		}
	}
	if col := a.tv.Columns().ByName("nick"); col != nil {
		avail := w - fotoW - statusW - 26
		if avail < 70 {
			avail = 70
		}
		_ = col.SetWidth(avail)
	}
	a.updateNetLabel()
}

// refreshAll = menu Chat > Refresh.
func (a *App) refreshAll() {
	a.hub.AnnounceNow()
	a.refreshList()
}

func (a *App) inboxDir() string {
	if a.cfg != nil && strings.TrimSpace(a.cfg.SaveDir) != "" {
		return a.cfg.SaveDir
	}
	return store.DefaultInboxDir()
}

// simpanStatusEdit menyimpan pesan status saat Enter atau fokus berpindah.
func (a *App) simpanStatusEdit() {
	if a.leStatus == nil {
		return
	}
	msg := truncateRunes(strings.TrimSpace(a.leStatus.Text()), proto.MaxStatMsg)
	if msg == a.cfg.StatusMsg {
		return
	}
	a.cfg.StatusMsg = msg
	if err := store.SaveConfig(a.cfg); err != nil {
		walk.MsgBox(a.mw, T("Pesan Status"), T("Gagal menyimpan: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
	// siarkan ke rekan lain seketika
	a.hub.SetStatMsg(msg)
}

// batalStatusEdit mengembalikan tulisan ke nilai tersimpan (Esc).
func (a *App) batalStatusEdit() {
	if a.leStatus != nil {
		_ = a.leStatus.SetText(a.cfg.StatusMsg)
	}
	a.kembaliKeDaftar()
}

// simpanNickEdit menyimpan nama tampilan saat Enter atau fokus berpindah.
func (a *App) simpanNickEdit() {
	if a.leNick == nil {
		return
	}
	n := truncateRunes(strings.TrimSpace(a.leNick.Text()), maxNickLen)
	if n == "" || n == a.hub.Nick() {
		a.batalNickEdit()
		return
	}
	a.hub.SetNick(n)
	a.cfg.Nick = n
	if err := store.SaveConfig(a.cfg); err != nil {
		walk.MsgBox(a.mw, T("Nama Tampilan"), T("Gagal menyimpan: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
	a.refreshHeader()
}

// batalNickEdit mengembalikan tulisan ke nama tersimpan (Esc).
func (a *App) batalNickEdit() {
	if a.leNick != nil {
		_ = a.leNick.SetText(a.hub.Nick())
	}
	a.kembaliKeDaftar()
}

// kembaliKeDaftar mengembalikan fokus ke daftar rekan.
func (a *App) kembaliKeDaftar() {
	if a.tv != nil {
		_ = a.tv.SetFocus()
	}
}

// flushSeenReports mengurus jendela chat yang sedang aktif setiap beberapa
// detik: laporan baca yang tertunda dikirim dan penanda belum dibaca di daftar
// rekan dibersihkan (pesan dianggap terbaca begitu terlihat di layarnya).
func (a *App) flushSeenReports() {
	a.mu.Lock()
	convos := make([]*Convo, 0, len(a.convos))
	for _, c := range a.convos {
		convos = append(convos, c)
	}
	a.mu.Unlock()
	if len(convos) == 0 {
		return
	}
	a.mw.Synchronize(func() {
		for _, c := range convos {
			if a.windowActive(c.win) {
				c.flushRead()
				a.clearUnread(c.uid)
			}
		}
	})
}

func (a *App) runIdleMonitor() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var sweep time.Time
	for range ticker.C {
		if a.isQuitting() {
			return
		}
		a.checkIdleStatus()
		a.flushSeenReports()
		// Tanggal berganti = semua ruang chat yang masih terbuka dibersihkan.
		a.rollViewsIfNewDay()
		// EvPeerOnline bisa terlewat (rekan sudah online saat pesan gagal),
		// jadi outbox disapu berkala selama jendela obrolannya terbuka.
		if time.Since(sweep) >= time.Minute {
			sweep = time.Now()
			a.sweepPendingOut()
		}
	}
}

// sweepPendingOut mengirim ulang pesan pending milik obrolan yang sedang buka
// dan rekannya sedang online.
func (a *App) sweepPendingOut() {
	if !a.outboxBusy.CompareAndSwap(false, true) {
		return
	}
	a.mu.Lock()
	var uids []string
	for uid, c := range a.convos {
		if c != nil && a.hub.PeerOnline(uid) {
			uids = append(uids, uid)
		}
	}
	a.mu.Unlock()
	go func() {
		defer a.outboxBusy.Store(false)
		for _, uid := range uids {
			a.retryPendingOut(uid)
		}
	}()
}

func (a *App) checkIdleStatus() {
	if a.cfg == nil || !a.cfg.AutoAway {
		return
	}
	mins := a.cfg.AutoAwayMins
	if mins <= 0 {
		mins = 5
	}
	threshold := time.Duration(mins) * time.Minute
	idle := getIdleDuration()

	a.mu.Lock()
	isAuto := a.isAutoAway
	prev := a.prevStatBeforeAway
	curStat := a.hub.Status()
	a.mu.Unlock()

	if idle >= threshold {
		if !isAuto && curStat != proto.StatAway && curStat != proto.StatInvis {
			a.mu.Lock()
			a.prevStatBeforeAway = curStat
			a.isAutoAway = true
			a.mu.Unlock()

			if a.mw != nil {
				a.mw.Synchronize(func() {
					a.setOwnStatus(proto.StatAway, false)
				})
			}
		}
	} else if idle < 3*time.Second {
		if isAuto {
			a.mu.Lock()
			a.isAutoAway = false
			target := prev
			if target < 0 || target >= proto.StatCount || target == proto.StatAway {
				target = proto.StatOnline
			}
			a.mu.Unlock()

			if a.mw != nil {
				a.mw.Synchronize(func() {
					a.setOwnStatus(target, false)
				})
			}
		}
	}
}

// ---------- header: status, nama, avatar ----------

// refreshHeader memperbarui ikon status, nama, teks status, dan avatar
// pada header jendela utama. Dipanggil setiap kali identitas berubah.
func (a *App) refreshHeader() {
	st := a.hub.Status()
	for i := 0; i < proto.StatCount; i++ {
		if a.actStatus[i] != nil {
			_ = a.actStatus[i].SetChecked(i == st)
		}
	}
	if a.ivStatus != nil {
		if ic := a.statusIconHeader(st, 20); ic != nil {
			_ = a.ivStatus.SetImage(ic)
		}
	}
	if a.leNick != nil && !a.leNick.Focused() {
		_ = a.leNick.SetText(a.hub.Nick())
	}
	if a.lblStat != nil {
		_ = a.lblStat.SetText(statusCaption(st))
	}
	if a.leStatus != nil && !a.leStatus.Focused() {
		_ = a.leStatus.SetText(a.cfg.StatusMsg)
	}
	if a.ivAvatar != nil {
		if ic := a.ownAvatarIcon(34); ic != nil {
			_ = a.ivAvatar.SetImage(ic)
		}
	}
	if a.mw != nil {
		_ = a.mw.SetTitle("Belgindo Office Messenger — " + a.hub.Nick())
	}
}

// setOwnStatus mengubah status kehadiran. persist hanya true bila pilihan
// pengguna: status hasil auto-away tidak boleh ikut tersimpan, kalau tidak
// aplikasi berikutnya menyala dalam keadaan Away.
func (a *App) setOwnStatus(st int, persist bool) {
	if st < 0 || st >= proto.StatCount {
		return
	}
	a.mu.Lock()
	if st != proto.StatAway {
		a.isAutoAway = false
	}
	a.mu.Unlock()
	a.hub.SetStatus(st)
	if persist {
		a.cfg.Status = st
		_ = store.SaveConfig(a.cfg)
	}
	a.refreshHeader()
}

// showStatusMenu menampilkan menu dropdown pilihan status
// (klik kiri pada ikon status = sama seperti klik kanan).
func (a *App) showStatusMenu() {
	if a.mw == nil || a.ivStatus == nil {
		return
	}
	var pt win.POINT
	if !win.GetCursorPos(&pt) {
		return
	}
	hwnd := a.ivStatus.Handle()
	lp := uintptr(uint16(pt.X)) | uintptr(uint16(pt.Y))<<16
	win.SendMessage(hwnd, win.WM_CONTEXTMENU, uintptr(hwnd), lp)
}

// statusMenuItems menyusun isi menu dropdown status.
func statusMenuItems(a *App) []MenuItem {
	items := make([]MenuItem, 0, proto.StatCount+2)
	for i := 0; i < proto.StatCount; i++ {
		st := i
		items = append(items, Action{
			AssignTo:  &a.actStatus[i],
			Text:      statusLabel(i),
			Image:     a.statusIcon(i, 16),
			Checkable: true,
			OnTriggered: func() {
				a.setOwnStatus(st, true)
			},
		})
	}
	items = append(items, Separator{})
	items = append(items, Action{
		Text:        T("Pengaturan Profil..."),
		OnTriggered: a.showSettings,
	})
	return items
}

// ---------- tray ----------

func (a *App) buildTrayMenu() {
	menu := a.ni.ContextMenu()
	add := func(text string, fn func()) {
		act := walk.NewAction()
		_ = act.SetText(text)
		act.Triggered().Attach(fn)
		_ = menu.Actions().Add(act)
	}
	add(T("Buka Belgindo Office Messenger"), func() { a.showMain() })
	add(T("Ubah Nama & Profil..."), a.showSettings)
	add(T("Ganti Status..."), a.showStatusMenu)
	add(T("Refresh Jaringan"), func() { a.hub.AnnounceNow() })
	sep := walk.NewSeparatorAction()
	_ = menu.Actions().Add(sep)
	add(T("Keluar"), a.quitApp)
}

func (a *App) showMain() {
	a.mw.Show()
	a.mw.Activate()
	a.clearAllUnreadVisual()
}

func (a *App) toggleMain() {
	a.mu.Lock()
	vis := a.mw.Visible()
	a.mu.Unlock()
	if vis {
		a.mw.Hide()
	} else {
		a.showMain()
	}
}

func (a *App) balloon(title, text string) {
	if !a.cfg.Balloon || a.ni == nil {
		return
	}
	_ = a.ni.ShowCustom(title, text, a.iconApp)
}

// soundOn memeriksa bunyi per jenis peristiwa (master tetap cfg.Sound).
func (a *App) soundOn(kind int) bool {
	if !a.cfg.Sound {
		return false
	}
	switch kind {
	case sndMsg:
		return a.cfg.SoundMsg
	case sndOnline:
		return a.cfg.SoundOnline
	case sndOffline:
		return a.cfg.SoundOffline
	}
	return true
}

func (a *App) quitApp() {
	a.mu.Lock()
	a.quitting = true
	convos := make([]*Convo, 0, len(a.convos))
	for _, c := range a.convos {
		convos = append(convos, c)
	}
	grp := make([]*GroupConv, 0, len(a.groups))
	for _, g := range a.groups {
		grp = append(grp, g)
	}
	a.mu.Unlock()
	for _, c := range convos {
		c.win.Close()
	}
	for _, g := range grp {
		g.win.Close()
	}
	if a.ni != nil {
		_ = a.ni.SetVisible(false)
		a.ni.Dispose()
	}
	walk.App().Exit(0)
}

// ---------- daftar pengguna & grup ----------

// listRow adalah satu baris daftar rekan: judul bagian (header) yang bisa
// dibuka/tutup, atau satu rekan di dalam bagian tersebut.
type listRow struct {
	header bool
	key    string   // "teman" atau ID grup (hanya untuk baris header)
	title  string   // teks baris header
	peer   p2p.Peer // rekan pada baris isi
}

// temanKey kunci bagian default yang berisi seluruh rekan.
const temanKey = "teman"

type userModel struct {
	walk.TableModelBase
	app  *App
	rows []listRow
}

func (m *userModel) RowCount() int { return len(m.rows) }

// rowAt mengembalikan salinan baris ke-i (aman dipakai di luar mutex).
func (m *userModel) rowAt(row int) (listRow, bool) {
	if row < 0 || row >= len(m.rows) {
		return listRow{}, false
	}
	return m.rows[row], true
}

func (m *userModel) Value(row, col int) interface{} {
	r, ok := m.rowAt(row)
	if !ok {
		return ""
	}
	if r.header {
		if col == 1 {
			return r.title
		}
		return ""
	}
	p := r.peer
	switch col {
	case 1:
		name := m.app.displayPeerName(p)
		if n := m.app.unreadCount(p.UID); n > 0 {
			name += fmt.Sprintf(" [%d]", n)
		}
		if s := strings.TrimSpace(p.StatMsg); s != "" {
			name += " (" + s + ")"
		}
		return name
	case 2:
		return peerStatusText(p)
	}
	return ""
}

// peerDisplayName nama rekan, fallback ke nama PC bila nickname kosong.
func peerDisplayName(p p2p.Peer) string {
	if n := strings.TrimSpace(p.Nick); n != "" {
		return n
	}
	if pc := strings.TrimSpace(p.PC); pc != "" {
		return pc
	}
	return p.UID
}

// displayPeerName mengutamakan nama lokal bila pengguna pernah diberi alias.
// Alias ini hanya tersimpan di komputer kita dan tidak mengubah nama jaringan.
func (a *App) displayPeerName(p p2p.Peer) string {
	if a != nil && a.cfg != nil {
		if n := strings.TrimSpace(a.cfg.Nicknames[p.UID]); n != "" {
			return n
		}
	}
	return peerDisplayName(p)
}

func (a *App) displayNameForUID(uid string) string {
	if p, ok := findPeer(a.hub.SnapshotPeers(), uid); ok {
		return a.displayPeerName(p)
	}
	nick, pc := a.hub.DisplayName(uid)
	return a.displayPeerName(p2p.Peer{UID: uid, Nick: nick, PC: pc})
}

func findPeer(peers []p2p.Peer, uid string) (p2p.Peer, bool) {
	for _, p := range peers {
		if p.UID == uid {
			return p, true
		}
	}
	return p2p.Peer{}, false
}

// peerStatusText teks kolom status untuk rekan.
// "Tampil Offline" tetap diperlihatkan sebagai Offline (itulah maksudnya).
func peerStatusText(p p2p.Peer) string {
	if !p.Online || p.Stat == proto.StatInvis {
		return "Offline"
	}
	return statusLabel(p.Stat)
}

func (a *App) unreadCount(uid string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.unread[uid]
}

// clearUnread menghapus penanda belum dibaca satu rekan: jendela chatnya
// sedang dibuka atau sedang aktif, jadi pesannya sudah terlihat.
func (a *App) clearUnread(uid string) {
	a.mu.Lock()
	if a.unread[uid] == 0 {
		a.mu.Unlock()
		return
	}
	delete(a.unread, uid)
	total := 0
	for _, v := range a.unread {
		total += v
	}
	a.mu.Unlock()
	a.refreshList()
	a.syncTrayUnread(total)
}

// syncTrayUnread menyesuaikan ikon dan keterangan tray dengan jumlah pesan
// yang belum dibuka.
func (a *App) syncTrayUnread(total int) {
	if a.ni == nil {
		return
	}
	if total <= 0 {
		_ = a.ni.SetIcon(a.iconNormal)
		_ = a.ni.SetToolTip("Belgindo Office Messenger")
		return
	}
	_ = a.ni.SetIcon(a.iconAlert)
	_ = a.ni.SetToolTip(fmt.Sprintf("Belgindo — %d pesan belum dibaca", total))
}

// buildRows menyusun daftar bertingkat: bagian "Teman" berisi semua rekan,
// diikuti satu bagian per grup berisi anggotanya. Bagian yang ditutup
// hanya menampilkan judulnya. Dipanggil dengan a.mu sudah terkunci.
func (a *App) buildRows(peers []p2p.Peer) []listRow {
	byUID := make(map[string]p2p.Peer, len(peers))
	for _, p := range peers {
		byUID[p.UID] = p
	}
	rows := make([]listRow, 0, len(peers)+len(a.groupDefs))

	section := func(key, title string, members []p2p.Peer) {
		arrow := "▸ "
		if !a.collapsed[key] {
			arrow = "▾ "
		}
		rows = append(rows, listRow{header: true, key: key, title: arrow + title})
		if a.collapsed[key] {
			return
		}
		for _, p := range members {
			rows = append(rows, listRow{peer: p})
		}
	}

	section(temanKey, fmt.Sprintf("Teman \u2014 %d", len(peers)), peers)

	for _, g := range a.groupDefs {
		ms := make([]p2p.Peer, 0, len(g.Members))
		for _, uid := range g.Members {
			if p, ok := byUID[uid]; ok {
				ms = append(ms, p)
				continue
			}
			nick, pc := a.hub.DisplayName(uid)
			ms = append(ms, p2p.Peer{UID: uid, Nick: nick, PC: pc})
		}
		section(g.ID, fmt.Sprintf("%s (%d)", g.Name, len(g.Members)), ms)
	}
	return rows
}

// toggleSection membuka/menutup bagian daftar dan menyimpan state-nya.
func (a *App) toggleSection(key string) {
	a.mu.Lock()
	if a.collapsed == nil {
		a.collapsed = map[string]bool{}
	}
	a.collapsed[key] = !a.collapsed[key]
	if !a.collapsed[key] {
		delete(a.collapsed, key)
	}
	list := make([]string, 0, len(a.collapsed))
	for k, v := range a.collapsed {
		if v {
			list = append(list, k)
		}
	}
	sort.Strings(list)
	a.cfg.Collapsed = list
	a.mu.Unlock()
	_ = store.SaveConfig(a.cfg)
	a.refreshList()
}

// onListMouseDown membuka/menutup bagian daftar saat judulnya diklik.
func (a *App) onListMouseDown(x, y int, btn walk.MouseButton) {
	if a.tv == nil {
		return
	}
	idx := a.tv.IndexAt(x, y)
	if idx < 0 {
		return
	}
	// Klik kanan harus memindahkan pilihan ke baris yang diklik sebelum
	// menu konteks dibuka agar aksi berlaku pada user yang benar.
	if btn == walk.RightButton {
		_ = a.tv.SetCurrentIndex(idx)
		return
	}
	if btn != walk.LeftButton {
		return
	}
	a.mu.Lock()
	r, ok := a.model.rowAt(idx)
	a.mu.Unlock()
	if !ok || !r.header {
		return
	}
	a.toggleSection(r.key)
}

func (a *App) styleCell(style *walk.CellStyle) {
	row := style.Row()
	col := style.Col()
	a.mu.Lock()
	m := a.model
	a.mu.Unlock()

	r, ok := m.rowAt(row)
	if !ok {
		return
	}
	if r.header {
		if col == 0 && r.key != temanKey {
			a.drawAvatarCell(style, a.groupIcon(22))
			return
		}
		// judul bagian: latar pudar + teks tebal agar tampil sebagai kepala grup
		style.BackgroundColor = walk.RGB(0xE4, 0xEC, 0xF5)
		style.TextColor = walk.RGB(0x1F, 0x4E, 0x79)
		style.Font = a.fontBold
		return
	}
	p := r.peer

	switch col {
	case 0:
		a.drawAvatarCell(style, a.peerAvatarIcon(p.UID, p.AvatarHash, p.AvatarID, 22))
	case 1:
		switch {
		case a.unreadCount(p.UID) > 0:
			style.TextColor = walk.RGB(0xC0, 0x50, 0x00)
		case p.Online:
			style.TextColor = walk.RGB(0x0B, 0x6E, 0x3A)
		default:
			style.TextColor = walk.RGB(0x8A, 0x8A, 0x8A)
		}
	case 2:
		switch {
		case !p.Online, p.Stat == proto.StatInvis:
			style.TextColor = walk.RGB(0x8A, 0x8A, 0x8A)
		default:
			style.TextColor = statusColorWalk(p.Stat)
		}
	}
}

// drawAvatarCell menggambar avatar di kolom foto menggunakan canvas sel
// (menggantikan gambar bawaan listview agar bisa lebih besar dan tajam).
func (a *App) drawAvatarCell(style *walk.CellStyle, icon *walk.Icon) {
	if icon == nil {
		return
	}
	c := style.Canvas()
	if c == nil {
		return
	}
	b := style.Bounds()
	if brush, err := walk.NewSolidColorBrush(style.BackgroundColor); err == nil {
		defer brush.Dispose()
		c.FillRectangle(brush, b)
	}
	size := 22
	x := b.X + (b.Width-size)/2
	if x < b.X {
		x = b.X
	}
	y := b.Y + (b.Height-size)/2
	_ = c.DrawImage(icon, walk.Point{X: x, Y: y})
}

func (a *App) refreshList() {
	sel := -1
	if a.tv != nil {
		sel = a.tv.CurrentIndex()
	}
	var selUID, selKey string
	a.mu.Lock()
	if r, ok := a.model.rowAt(sel); ok {
		if r.header {
			selKey = r.key
		} else {
			selUID = r.peer.UID
		}
	}
	a.model.rows = a.buildRows(a.hub.SnapshotPeers())
	a.mu.Unlock()

	a.tv.SetModel(a.model)

	// pulihkan pilihan (di luar mutex: repaint bisa mengunci ulang)
	idx := -1
	a.mu.Lock()
	for i, r := range a.model.rows {
		if r.header {
			if r.key == selKey {
				idx = i
				break
			}
			continue
		}
		if r.peer.UID == selUID {
			idx = i
			break
		}
	}
	a.mu.Unlock()
	if idx >= 0 {
		_ = a.tv.SetCurrentIndex(idx)
	}
	a.updateNetLabel()
}

func (a *App) updateNetLabel() {
	if a.lblNet == nil {
		return
	}
	online, offline := 0, 0
	for _, p := range a.hub.SnapshotPeers() {
		if p.Online {
			online++
		} else {
			offline++
		}
	}
	w := 0
	if a.mw != nil {
		w = a.mw.ClientBounds().Width
	}
	base := fmt.Sprintf("%d online • %d offline", online, offline)
	variants := []string{
		base,
		base + " • IP " + firstIP(a.localIPs),
		base + " • IP " + a.localIPs,
	}
	// Hanya pakai teks yang muat supaya lebar teks tidak memaksa
	// batas minimum ukuran jendela (walk menghitung min-size dari isi).
	full := variants[0]
	for _, v := range variants {
		if utf8.RuneCountInString(v)*31 <= (w-24)*5 {
			full = v
		}
	}
	_ = a.lblNet.SetText(full)
	_ = a.lblNet.SetToolTipText(fmt.Sprintf("IP lokal: %s", a.localIPs))
	if a.mw != nil {
		_ = a.mw.SetTitle("Belgindo Office Messenger — " + a.hub.Nick())
	}
}

// firstIP mengambil alamat pertama dari daftar "a, b, c".
func firstIP(list string) string {
	if i := strings.Index(list, ","); i > 0 {
		return list[:i]
	}
	return list
}

// rowIDs mengembalikan UID rekan atau ID grup yang diwakili satu baris daftar.
// Baris judul bagian mengembalikan ID grupnya (kecuali bagian Teman).
func rowIDs(r listRow) (uid, gid string) {
	if r.header {
		if r.key == temanKey {
			return "", ""
		}
		return "", r.key
	}
	return r.peer.UID, ""
}

// selected mengembalikan UID rekan atau ID grup yang sedang dipilih.
func (a *App) selected() (uid, gid string) {
	a.mu.Lock()
	r, ok := a.model.rowAt(a.tv.CurrentIndex())
	a.mu.Unlock()
	if !ok {
		return "", ""
	}
	return rowIDs(r)
}

func (a *App) openSelected() {
	a.mu.Lock()
	r, ok := a.model.rowAt(a.tv.CurrentIndex())
	a.mu.Unlock()
	if !ok {
		return
	}
	if r.header {
		// satu klik pada judul sudah membuka/menutup bagiannya;
		// klik ganda pada nama grup menampilkan jendela grupnya.
		if r.key != temanKey {
			a.openGroup(r.key)
		}
		return
	}
	a.openConvo(r.peer.UID)
}

func (a *App) sendFileToSelected() {
	uid, _ := a.selected()
	if uid == "" {
		return
	}
	a.openConvo(uid)
	a.mu.Lock()
	c := a.convos[uid]
	a.mu.Unlock()
	if c != nil {
		c.pickFiles()
	}
}

func (a *App) showUserInfo() {
	uid, gid := a.selected()
	if gid != "" {
		g, ok := a.groupSnap(gid)
		if !ok {
			return
		}
		var sb strings.Builder
		sb.WriteString(T("Grup") + ": " + g.Name + "\n" + T("Anggota") + ":\n")
		for _, m := range g.Members {
			nick, pc := a.hub.DisplayName(m)
			if p, ok := findPeer(a.hub.SnapshotPeers(), m); ok {
				nick, pc = a.displayPeerName(p), p.PC
			}
			sb.WriteString("  • " + nick + " (" + pc + ")\n")
		}
		if a.chatLocked(store.SafeKey(gid)) {
			sb.WriteString("\n" + T("Riwayat grup ini dikunci dengan password.") + "\n")
		}
		walk.MsgBox(a.mw, T("Info Grup"), sb.String(), walk.MsgBoxOK|walk.MsgBoxIconInformation)
		return
	}
	if uid == "" {
		return
	}
	for _, p := range a.hub.SnapshotPeers() {
		if p.UID != uid {
			continue
		}
		status := T("Online")
		if !p.Online {
			status = T("Offline")
		}
		text := fmt.Sprintf(T("Nama tampilan: %s\nNama jaringan: %s\nKomputer: %s\nAlamat IP: %s\nStatus: %s"),
			a.displayPeerName(p), p.Nick, p.PC, p.IP, status)
		// Update.txt: riwayat tidak boleh terbaca lewat menu info pengguna.
		if a.chatLocked(store.SafeKey(uid)) {
			text += "\n\n" + T("Percakapan ini dikunci. Buka ruang chatnya untuk membaca riwayat setelah autentikasi.")
		}
		walk.MsgBox(a.mw, T("Info Pengguna"), text, walk.MsgBoxOK|walk.MsgBoxIconInformation)
		return
	}
}

// lockSelectedConversation membuat password untuk percakapan pengguna terpilih.
func (a *App) lockSelectedConversation() {
	uid, gid := a.selected()
	if uid == "" || gid != "" {
		return
	}
	a.lockConversation(store.SafeKey(uid))
}

// unlockSelectedConversation menghapus password percakapan terpilih.
func (a *App) unlockSelectedConversation() {
	uid, gid := a.selected()
	if uid == "" || gid != "" {
		return
	}
	a.unlockConversation(store.SafeKey(uid))
}

func (a *App) promptChatPassword(title, prompt string) (string, bool) {
	return a.promptLine(title, prompt, "", true)
}

// promptText menanyakan satu baris teks biasa (nama grup, dll). initial
// dipakai sebagai isi awal sekaligus cadangan bila pengguna Kosongkan.
func (a *App) promptText(title, prompt, initial string, out *string) bool {
	s, ok := a.promptLine(title, prompt, initial, false)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = initial
	}
	*out = s
	return true
}

// promptLine menampilkan dialog satu baris; secret=true memakai topeng.
func (a *App) promptLine(title, prompt, initial string, secret bool) (string, bool) {
	var dlg *walk.Dialog
	var le *walk.LineEdit
	var db, cb *walk.PushButton
	if err := (Dialog{
		AssignTo: &dlg, Title: title,
		MinSize: Size{Width: 300, Height: 145}, Size: Size{Width: 300, Height: 145},
		Font:          Font{Family: "Segoe UI", PointSize: 9},
		Layout:        VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 8},
		DefaultButton: &db, CancelButton: &cb,
		Children: []Widget{
			Label{Text: prompt},
			LineEdit{AssignTo: &le, Text: initial, PasswordMode: secret, MaxLength: 128},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &db, Text: T("Lanjut"), OnClicked: func() { dlg.Accept() }},
				PushButton{AssignTo: &cb, Text: T("Batal"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}).Create(a.mw); err != nil {
		return "", false
	}
	if le == nil {
		dlg.Dispose()
		return "", false
	}
	// Set fokus saat dialog benar-benar tampil; fokus sebelum Run dapat
	// ditimpa oleh default button milik dialog Windows.
	dlg.VisibleChanged().Attach(func() {
		if dlg.Visible() {
			_ = le.SetFocus()
		}
	})
	result := dlg.Run()
	value := le.Text()
	dlg.Dispose()
	return value, result == walk.DlgCmdOK
}

// renameSelected memberi alias lokal pada user terpilih tanpa mengubah nama
// yang disiarkan oleh user tersebut ke jaringan.
func (a *App) renameSelected() {
	uid, gid := a.selected()
	if uid == "" || gid != "" {
		return
	}
	p := p2p.Peer{UID: uid}
	for _, candidate := range a.hub.SnapshotPeers() {
		if candidate.UID == uid {
			p = candidate
			break
		}
	}
	current := a.displayPeerName(p)
	var dlg *walk.Dialog
	var le *walk.LineEdit
	var db, cb *walk.PushButton
	if err := (Dialog{
		AssignTo:      &dlg,
		Title:         T("Ubah Tampilan Nama"),
		MinSize:       Size{Width: 270, Height: 140},
		Size:          Size{Width: 270, Height: 140},
		Font:          Font{Family: "Segoe UI", PointSize: 9},
		Layout:        VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 8},
		DefaultButton: &db,
		CancelButton:  &cb,
		Children: []Widget{
			Label{Text: T("Nama tampilan lokal:")},
			LineEdit{AssignTo: &le, Text: current, MaxLength: maxNickLen},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &db, Text: T("Simpan"), OnClicked: func() { dlg.Accept() }},
				PushButton{AssignTo: &cb, Text: T("Batal"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}).Create(a.mw); err != nil {
		return
	}
	if dlg.Run() != walk.DlgCmdOK {
		return
	}
	name := truncateRunes(strings.TrimSpace(le.Text()), maxNickLen)
	if a.cfg.Nicknames == nil {
		a.cfg.Nicknames = make(map[string]string)
	}
	if name == "" || name == peerDisplayName(p) {
		delete(a.cfg.Nicknames, uid)
	} else {
		a.cfg.Nicknames[uid] = name
	}
	if err := store.SaveConfig(a.cfg); err != nil {
		walk.MsgBox(a.mw, T("Ubah Tampilan Nama"), T("Gagal menyimpan: ")+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	a.refreshList()
}

// ---------- peristiwa jaringan ----------

func (a *App) onHubEvent(e p2p.Event) {
	switch e.Kind {
	case p2p.EvAvatarUpd:
		// foto profil rekan baru diterima -> perbarui kolom foto.
		a.invalidatePeerAvatarIcons(e.UID)
		a.refreshList()
	case p2p.EvPeerOnline:
		a.refreshList()
		// rekan baru muncul -> kirim ulang pesan yang dulu menggantung
		go a.retryPendingOut(e.UID)
		if a.notifyPresence() && !e.Quiet {
			a.balloon(fmt.Sprintf(T("%s online"), e.Nick),
				fmt.Sprintf(T("%s (%s) kembali terhubung."), e.Nick, e.PC))
			if a.soundOn(sndOnline) {
				playWAV(wavOnline)
			}
		}
	case p2p.EvPeerOffline:
		a.refreshList()
		// Quiet = peristiwa saat aplikasi baru menyala; tanpa pemeriksaan ini
		// semua rekan yang sudah lama hilang memunculkan balon sekaligus.
		if a.notifyPresence() && !e.Quiet {
			a.balloon(fmt.Sprintf(T("%s offline"), e.Nick),
				fmt.Sprintf(T("%s (%s) keluar / tidak terhubung."), e.Nick, e.PC))
			if a.soundOn(sndOffline) {
				playWAV(wavOffline)
			}
		}
	case p2p.EvGroupInfo:
		a.onGroupInfo(e)
	case p2p.EvPeerRenamed:
		a.refreshList()
		a.mu.Lock()
		c := a.convos[e.UID]
		a.mu.Unlock()
		if c != nil {
			name, pc := e.Nick, e.PC
			if p, ok := findPeer(a.hub.SnapshotPeers(), e.UID); ok {
				name, pc = a.displayPeerName(p), p.PC
			}
			c.updateTitle(name, pc)
		}
	case p2p.EvMsgIn:
		a.onMsgIn(e)
	case p2p.EvMsgRead:
		a.onMsgRead(e)
	case p2p.EvTypingIn:
		if g := a.groupConvIfOpen(e.Group); g != nil {
			g.bridge.pushCmd(pageCmd{C: "typing", On: true, N: e.Nick})
			return
		}
		c := a.convoIfOpen(e.UID)
		if c != nil {
			c.showTyping()
		}
	case p2p.EvFileOfferIn:
		a.onFileOffer(e)
	case p2p.EvFileProgress:
		a.txProgress(e)
		if g := a.groupConvIfOpen(e.Group); g != nil {
			g.setFileProgressText(fmt.Sprintf(T("Menerima %s %d%%"), e.FileName, int(e.Percent)))
			return
		}
		if c := a.convoIfOpen(e.UID); c != nil {
			c.setFileProgress(e)
		}
	case p2p.EvFileDone:
		a.onFileDone(e)
	case p2p.EvFileRefused:
		a.txFinish(e, "ditolak")
		msg := fmt.Sprintf(T("%s menolak file %s."), e.Nick, e.FileName)
		a.talkSys(e, msg, true)
		a.notifyTx(msg)
	case p2p.EvFileFailed:
		a.txFinish(e, "gagal")
		msg := fmt.Sprintf(T("Transfer file %s gagal: %s"), e.FileName, e.Err)
		a.talkSys(e, msg, false)
		a.notifyTx(msg)
	}
}

// targetConv mengembalikan jendela percakapan yang cocok untuk peristiwa e.
// Peristiwa bergroup hanya pernah routed ke ruang grup (bukan jendela pribadi
// dengan pengirim yang sama), supaya berkas grup tidak menumpah ke chat 1:1.
func (a *App) targetConv(e p2p.Event) (*Convo, *GroupConv) {
	if e.Group != "" {
		return nil, a.groupConvIfOpen(e.Group)
	}
	return a.convoIfOpen(e.UID), nil
}

// talkSys menulis satu pesan sistem ke jendela tujuan (bila terbuka).
// clearProgress dipakai untuk menghentikan bilah transfer yang macet.
func (a *App) talkSys(e p2p.Event, text string, clearProgress bool) {
	c, g := a.targetConv(e)
	switch {
	case c != nil:
		c.appendSys(text)
		if clearProgress {
			c.clearFileProgress()
		}
	case g != nil:
		g.appendSys(text)
		if clearProgress {
			g.clearFileProgress()
		}
	}
}

// notifyTx memberi tahu hasil transfer lewat satu balon berisi judul + pesan.
func (a *App) notifyTx(text string) {
	if a.doNotDisturb() || !a.cfg.Balloon {
		return
	}
	a.balloon(T("Kirim file"), text)
}

// notifyPresence true bila notifikasi kehadiran boleh ditampilkan: status
// Busy/DND milik pengguna sendiri membisukan balon dan suara.
func (a *App) notifyPresence() bool {
	return !a.doNotDisturb() && a.cfg.Balloon
}

// doNotDisturb true bila status lokal Busy atau Jangan Ganggu sedang aktif.
func (a *App) doNotDisturb() bool {
	return a.cfg.Status == proto.StatBusy || a.cfg.Status == proto.StatDND
}

// onGroupInfo menerima definisi grup dari jaringan lalu menyelaraskan jendela
// yang sudah terbuka (nama baru, anggota baru, atau grup yang dihapus).
func (a *App) onGroupInfo(e p2p.Event) {
	gc := e.GroupInfo
	if gc.ID == "" {
		gc = proto.GroupChange{ID: e.Group, Name: e.GroupName, Owner: e.GroupOwner, Version: 1}
	}
	if gc.Version < 1 {
		gc.Version = 1
	}
	if !a.applyGroupChange(gc) {
		return
	}
	a.mu.Lock()
	g := a.groups[gc.ID]
	a.mu.Unlock()
	if g == nil {
		return
	}
	if gc.Deleted {
		_ = g.win.Close()
		return
	}
	g.mu.Lock()
	g.members = append([]string(nil), gc.Members...)
	g.mu.Unlock()
	g.setName(gc.Name, gc.Version)
}

func (a *App) groupConvIfOpen(gid string) *GroupConv {
	if gid == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.groups[gid]
}

// onMsgIn menangani pesan masuk dari percakapan perorangan.
func (a *App) onMsgIn(e p2p.Event) {
	if e.Group != "" {
		a.onGroupMsgIn(e)
		return
	}
	key := store.SafeKey(e.UID)
	_ = store.AppendHistory(e.UID, store.Entry{TS: e.TS, Dir: "in", Kind: "msg", Text: e.Text, ID: e.ID})
	a.mu.Lock()
	c := a.convos[e.UID]
	a.lastMsgUID = e.UID
	a.mu.Unlock()

	if c != nil {
		c.appendIn(e.Text, e.TS, e.ID)
	}
	// jendela perorangan sedang tampil dan fokus -> tidak perlu notifikasi
	if c != nil && a.windowActive(c.win) {
		a.clearUnread(e.UID)
		return
	}
	if a.soundOn(sndMsg) && !a.doNotDisturb() {
		playWAV(wavMsg)
	}
	a.countUnread(e.UID)
	a.notifyIn(e.UID, e.Nick, a.lockedPreview(key, e.Text))
}

// onGroupMsgIn menangani pesan masuk ke ruang grup. Riwayat grup terpisah dari
// riwayat perorangan, jadi penulisannya memakai AppendGroupHistory.
func (a *App) onGroupMsgIn(e p2p.Event) {
	key := store.SafeKey(e.Group)
	_ = store.AppendGroupHistory(e.Group, store.Entry{TS: e.TS, Dir: "in", Kind: "msg",
		Text: e.Text, ID: e.ID, Who: e.Nick})
	g := a.groupConvIfOpen(e.Group)
	if g != nil {
		g.appendIn(a.displayNameForUID(e.UID), e.Text, e.TS, e.ID)
		if a.windowActive(g.win) {
			a.clearUnread(e.Group)
			return
		}
	}
	if a.soundOn(sndMsg) && !a.doNotDisturb() {
		playWAV(wavMsg)
	}
	a.countUnread(e.Group)
	text := e.Nick + ": " + e.Text
	a.notifyIn(e.Group, T("Grup")+" "+a.groupNameFor(e.Group), a.lockedPreview(key, text))
}

// countUnread menaikkan penghitung belum dibaca lalu menyegarkan badge daftar
// dan ikon tray. Kunci peta unread memakai UID perorangan atau ID grup, jadi
// grup pun ikut menghitung badge.
func (a *App) countUnread(key string) {
	a.mu.Lock()
	a.unread[key]++
	total := 0
	for _, v := range a.unread {
		total += v
	}
	a.mu.Unlock()
	a.refreshList()
	a.syncTrayUnread(total)
}

// notifyIn menampilkan SATU notifikasi berisi isi pesan (bukan dua balon
// sekaligus). Balon tray dipakai hanya bila kartu mengambang tidak tersedia.
func (a *App) notifyIn(key, who, text string) {
	if a.doNotDisturb() || !a.cfg.Balloon {
		return
	}
	title := fmt.Sprintf(T("%s mengirim pesan"), who)
	if a.balloonWin != nil {
		a.balloonWin.Show(key, title, truncateRunes(text, 120))
		return
	}
	a.balloon(title, truncateRunes(text, 120))
}

// groupNameFor mengambil nama grup untuk judul notifikasi.
func (a *App) groupNameFor(gid string) string {
	if d := a.groupDef(gid); d != nil {
		return d.Name
	}
	return gid
}

// onMsgRead menangani laporan bahwa rekan sudah membaca pesan-pesan kita:
// centang pada balon keluar berubah menjadi biru.
func (a *App) onMsgRead(e p2p.Event) {
	a.mu.Lock()
	c := a.convos[e.UID]
	a.mu.Unlock()
	if c != nil {
		c.setState(e.IDs, "read")
		return
	}
	// jendela belum terbuka -> cukup perbarui riwayat, centang biru muncul
	// saat jendela dibuka lagi.
	_ = store.SetHistoryState(e.UID, e.IDs, "read")
}

// windowActive melaporkan apakah sebuah jendela sedang terlihat dan fokus.
func (a *App) windowActive(w *walk.MainWindow) bool {
	if w == nil || !w.Visible() {
		return false
	}
	return walk.App().ActiveForm() == w
}

func (a *App) clearAllUnreadVisual() {
	a.mu.Lock()
	if len(a.unread) == 0 {
		a.mu.Unlock()
		return
	}
	a.unread = make(map[string]int)
	a.mu.Unlock()
	a.refreshList()
	if a.ni != nil {
		_ = a.ni.SetIcon(a.iconNormal)
		_ = a.ni.SetToolTip("Belgindo Office Messenger")
	}
}

func (a *App) onFileOffer(e p2p.Event) {
	c, g := a.targetConv(e)
	var owner walk.Form = a.mw
	switch {
	case c != nil:
		owner = c.win
	case g != nil:
		owner = g.win
	}
	msg := fmt.Sprintf(T("%s mengirim file untuk Anda:\n\n%s (%s)\n\nTerima file ini?"),
		e.Nick, e.FileName, humanSize(e.Size))
	if warn := p2p.WarningRiskyFile(e.FileName); warn != "" {
		msg += "\n\n" + warn
	}
	if walk.MsgBox(owner, T("Terima File — Belgindo Office Messenger"), msg,
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		a.hub.RejectFile(e.FID)
		return
	}
	saveDir := a.inboxDir()
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		a.balloon(T("Gagal menerima file"), err.Error())
		a.hub.RejectFile(e.FID)
		return
	}
	dest := p2p.UniquePath(saveDir, e.FileName)
	if err := a.hub.AcceptFile(e.FID, dest); err != nil {
		a.balloon(T("Gagal menerima file"), err.Error())
		return
	}
	a.txStart(e.FID, "in", e.Nick, e.FileName, e.Size)
	switch {
	case c != nil:
		c.setFileProgressText(fmt.Sprintf(T("Menerima %s ..."), e.FileName))
	case g != nil:
		g.setFileProgressText(fmt.Sprintf(T("Menerima %s ..."), e.FileName))
	}
}

// onFileDone mencatat hasil transfer: berkas masuk tampil sebagai LAMPIRAN di
// dalam balon (foto langsung tampil), bukan sebagai baris teks sistem.
func (a *App) onFileDone(e p2p.Event) {
	c, g := a.targetConv(e)
	a.txFinish(e, "selesai")
	switch e.Dir {
	case p2p.DirIn:
		a.noteFileHistory(e, "in")
		switch {
		case c != nil:
			c.appendAttachment("in", e.FileName, e.Size, e.Path)
			c.clearFileProgress()
		case g != nil:
			g.appendAttachment(a.displayNameForUID(e.UID), e.FileName, e.Size, e.Path)
			g.clearFileProgress()
		}
		if a.soundOn(sndMsg) && !a.doNotDisturb() {
			playWAV(wavMsg)
		}
		a.notifyFile(fmt.Sprintf(T("%s menerima file"), e.Nick),
			fmt.Sprintf(T("%s dari %s tersimpan di %s"), e.FileName, e.Nick, e.Path))
	case p2p.DirOut:
		a.noteFileHistory(e, "out")
		switch {
		case c != nil:
			c.appendAttachment("out", e.FileName, e.Size, e.Path)
			c.clearFileProgress()
		case g != nil:
			g.clearFileProgress()
		}
		a.notifyFile(T("File terkirim"),
			fmt.Sprintf(T("%s terkirim ke %s."), e.FileName, e.Nick))
	}
}

// notifyFile menampilkan satu balon hasil transfer tanpa menggangu saat DND.
func (a *App) notifyFile(title, text string) {
	if a.doNotDisturb() || !a.cfg.Balloon {
		return
	}
	a.balloon(title, text)
}

// noteFileHistory menyimpan berkas ke riwayat percakapan atau riwayat grup,
// sesuai tujuan transfer.
func (a *App) noteFileHistory(e p2p.Event, dir string) {
	line := fmt.Sprintf("%s|%d|%s", e.FileName, e.Size, e.Path)
	if e.Group != "" {
		_ = store.AppendGroupHistory(e.Group, store.Entry{TS: time.Now().Unix(), Dir: dir,
			Kind: "file", Text: line, Who: e.Nick})
		return
	}
	_ = store.AppendHistory(e.UID, store.Entry{TS: time.Now().Unix(), Dir: dir, Kind: "file", Text: line})
}

// ---------- util ----------

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func humanSize(n int64) string {
	const kb, mb = 1024, 1024 * 1024
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
