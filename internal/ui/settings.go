//go:build windows

package ui

import (
	"fmt"
	"strings"

	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// showSettings — menu Pengaturan > Pengaturan.
// Semua halaman dibuat rata kiri dengan tata letak grid dua kolom yang
// konsisten: kolom kiri label (tetap), kolom kanan isi (melar).
func (a *App) showSettings() {
	var dlg *walk.Dialog
	var leNick *walk.LineEdit
	var leStatMsg *walk.LineEdit
	var ivPrev *walk.ImageView
	var lblStatPrev *walk.TextLabel
	var neDays *walk.NumberEdit
	var cbBalloon, cbAuto, cbTray, cbSound, cbMsg, cbOn, cbOff, cbAutoAway *walk.CheckBox
	var neAwayMins *walk.NumberEdit
	var leSaveDir *walk.LineEdit
	var db, cb *walk.PushButton

	nick := a.hub.Nick()
	auto := AutostartEnabled()

	// updatePreview menyegarkan pratinjau foto/avatar di tab Profil Saya.
	updatePreview := func() {
		if ivPrev != nil {
			a.invalidateOwnAvatarIcons()
			if ic := a.ownAvatarIcon(64); ic != nil {
				_ = ivPrev.SetImage(ic)
			}
		}
		if lblStatPrev != nil {
			_ = lblStatPrev.SetText(statusLabel(a.hub.Status()) + T(" — ubah lewat ikon status"))
		}
	}

	// pilihFoto: unggah foto dari file, dipotong persegi & disimpan lokal.
	pilihFoto := func() {
		fd := walk.FileDialog{
			Title:  T("Pilih Foto Profil"),
			Filter: "Gambar (PNG, JPG, GIF)|*.png;*.jpg;*.jpeg;*.gif|Semua file|*.*",
		}
		ok, err := fd.ShowOpen(a.mw)
		if err != nil || !ok {
			return
		}
		if err := saveOwnPhoto(fd.FilePath); err != nil {
			walk.MsgBox(a.mw, T("Foto Profil"), T("Gagal memuat foto: ")+err.Error(),
				walk.MsgBoxOK|walk.MsgBoxIconWarning)
			return
		}
		a.hub.ReloadAvatar(store.OwnAvatarPath(), a.cfg.AvatarID)
		a.invalidateOwnAvatarIcons()
		a.refreshHeader()
		updatePreview()
	}

	// pilihAvatar: pilih salah satu avatar bawaan.
	pilihAvatar := func() {
		if id, ok := a.chooseAvatarDialog(); ok {
			removeOwnPhoto() // foto custom dimatikan, avatar bawaan yang dipakai
			a.cfg.AvatarID = id
			a.hub.ReloadAvatar("", id)
			a.invalidateOwnAvatarIcons()
			a.refreshHeader()
			updatePreview()
		}
	}

	// hapusFoto: kembali ke avatar otomatis.
	hapusFoto := func() {
		removeOwnPhoto()
		a.cfg.AvatarID = -1
		a.hub.ReloadAvatar("", -1)
		a.invalidateOwnAvatarIcons()
		a.refreshHeader()
		updatePreview()
	}

	// baris label+isi agar rata kiri dan konsisten antar halaman.
	rowLabel := func(text string) Label { return Label{Text: text} }

	page := func(title string, children ...Widget) TabPage {
		return TabPage{
			Title:    title,
			Layout:   VBox{Margins: Margins{Left: 16, Top: 14, Right: 16, Bottom: 14}, Spacing: 8, Alignment: AlignHNearVNear},
			Children: append([]Widget{}, append(children, VSpacer{})...),
		}
	}

	if err := (Dialog{
		AssignTo:      &dlg,
		Title:         T("Pengaturan — Belgindo Office Messenger"),
		MinSize:       Size{Width: 438, Height: 420},
		Size:          Size{Width: 438, Height: 460},
		Font:          Font{Family: "Segoe UI", PointSize: 9},
		Layout:        VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
		DefaultButton: &db,
		CancelButton:  &cb,
		Children: []Widget{
			TabWidget{
				StretchFactor:  1,
				ContentMargins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8},
				Pages: []TabPage{

					// ================= Profil Saya =================
					page(T("Profil Saya"),
						Composite{
							Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
							Children: []Widget{
								rowLabel("Foto profil:"),
								Composite{
									Layout: HBox{MarginsZero: true, Spacing: 8},
									Children: []Widget{
										ImageView{
											AssignTo: &ivPrev,
											MinSize:  Size{Width: 64, Height: 64},
											MaxSize:  Size{Width: 64, Height: 64},
											Mode:     ImageViewModeZoom,
										},
										Composite{
											Layout: VBox{MarginsZero: true, Spacing: 4},
											Children: []Widget{
												PushButton{Text: T("Pilih Foto..."), OnClicked: pilihFoto},
												PushButton{Text: T("Pilih Avatar..."), OnClicked: pilihAvatar},
												PushButton{Text: T("Hapus Foto"), OnClicked: hapusFoto},
												HSpacer{},
											},
										},
									},
								},

								rowLabel("Nama tampilan:"),
								LineEdit{AssignTo: &leNick, Text: nick},

								rowLabel("Pesan status:"),
								LineEdit{AssignTo: &leStatMsg, Text: a.cfg.StatusMsg},

								rowLabel("Status:"),
								TextLabel{AssignTo: &lblStatPrev, TextColor: walk.RGB(0x5B, 0x6B, 0x7A)},

								rowLabel("Nama komputer:"),
								TextLabel{Text: a.pcName()},

								rowLabel("ID perangkat ini:"),
								TextLabel{Text: a.cfg.UID},

								rowLabel("IP lokal:"),
								TextLabel{Text: a.localIPs},
							},
						},
						TextLabel{Text: T("Nama dan foto/avatar dilihat rekan Anda.\nPesan status bisa langsung diketik di bawah nama Anda."),
							TextColor: walk.RGB(0x5B, 0x6B, 0x7A)},
					),

					// ================= Umum =================
					page(T("Umum"),
						TextLabel{Text: T("Tombol X menyembunyikan aplikasi ke tray;\nkeluar penuh lewat menu Chat > Keluar."),
							TextColor: walk.RGB(0x5B, 0x6B, 0x7A)},
						CheckBox{AssignTo: &cbBalloon, Text: T("Tampilkan balon notifikasi di tray saat ada pesan"), Checked: a.cfg.Balloon},
						CheckBox{AssignTo: &cbTray, Text: T("Mulai tersembunyi di tray setiap kali aplikasi dibuka"), Checked: a.cfg.StartInTray},
						CheckBox{AssignTo: &cbAuto, Text: T("Jalankan otomatis saat Windows menyala"), Checked: auto},
						VSpacer{Size: 4},
						CheckBox{AssignTo: &cbAutoAway, Text: T("Otomatis status 'Pergi' (Away) saat komputer idle"), Checked: a.cfg.AutoAway},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								Label{Text: T("Tenggat waktu idle:")},
								NumberEdit{AssignTo: &neAwayMins, Decimals: 0, MinValue: 1, MaxValue: 120, Value: float64(a.cfg.AutoAwayMins)},
								Label{Text: T("menit")},
								HSpacer{},
							},
						},
					),

					// ================= Pesan & Bunyi =================
					page(T("Pesan & Bunyi"),
						CheckBox{AssignTo: &cbSound, Text: T("Aktifkan bunyi notifikasi"), Checked: a.cfg.Sound},
						GroupBox{
							Title:  T("Bunyi untuk"),
							Layout: VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 4},
							Children: []Widget{
								CheckBox{AssignTo: &cbMsg, Text: T("Pesan masuk"), Checked: a.cfg.SoundMsg},
								CheckBox{AssignTo: &cbOn, Text: T("Rekan masuk / online"), Checked: a.cfg.SoundOnline},
								CheckBox{AssignTo: &cbOff, Text: T("Rekan keluar / offline"), Checked: a.cfg.SoundOffline},
							},
						},
						TextLabel{Text: T("Pesan saat jendela chat tidak fokus\nmemunculkan balon di tray dan menandai\nnama pengirim dengan tanda *."),
							TextColor: walk.RGB(0x5B, 0x6B, 0x7A)},
					),

					// ================= Jaringan =================
					page(T("Jaringan"),
						TextLabel{Text: T("Chat & file (TCP): port 51515\nPenemuan rekan (UDP): port 51516")},
						TextLabel{Text: T("Tidak ada server dan tidak ada akun.\nIzinkan aplikasi ini pada Windows Firewall\nuntuk jaringan Private bila rekan tak muncul."),
							TextColor: walk.RGB(0x5B, 0x6B, 0x7A)},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								PushButton{Text: T("Perbarui daftar sekarang"), OnClicked: func() { a.hub.AnnounceNow(); a.refreshList() }},
								HSpacer{},
							},
						},
					),

					// ================= Transfer File =================
					page(T("Transfer File"),
						TextLabel{Text: T("Folder penyimpanan file yang Anda terima:")},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								LineEdit{AssignTo: &leSaveDir, Text: a.inboxDir(), ReadOnly: true},
								PushButton{Text: T("Pilih Folder..."), OnClicked: func() {
									fd := walk.FileDialog{
										Title:          T("Pilih Folder Penyimpanan File"),
										InitialDirPath: leSaveDir.Text(),
									}
									if ok, err := fd.ShowBrowseFolder(dlg); err == nil && ok && fd.FilePath != "" {
										_ = leSaveDir.SetText(fd.FilePath)
									}
								}},
								PushButton{Text: T("Default"), OnClicked: func() {
									_ = leSaveDir.SetText(store.DefaultInboxDir())
								}},
							},
						},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								PushButton{Text: T("Buka Folder File"), OnClicked: func() { openFolder(leSaveDir.Text()) }},
								PushButton{Text: T("Buka Log Transfer"), OnClicked: func() { dlg.Cancel(); a.showTransfers() }},
								HSpacer{},
							},
						},
					),

					// ================= Pencatatan =================
					page(T("Pencatatan"),
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								Label{Text: T("Simpan riwayat percakapan selama:")},
								NumberEdit{AssignTo: &neDays, Decimals: 0, MinValue: 7, MaxValue: 3650, Value: float64(a.cfg.HistoryDays)},
								Label{Text: T("hari")},
								HSpacer{},
							},
						},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								PushButton{Text: T("Buka Riwayat"), OnClicked: func() { dlg.Cancel(); a.showHistory() }},
								PushButton{Text: T("Buka Folder Riwayat"), OnClicked: func() { openFolder(store.HistoryDir()) }},
								HSpacer{},
							},
						},
					),
				},
			},
			// tombol dialog: rata kiri sesuai permintaan
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					PushButton{AssignTo: &db, Text: T("Simpan"), OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cb, Text: T("Batal"), OnClicked: func() { dlg.Cancel() }},
					HSpacer{},
				},
			},
		},
	}).Create(a.mw); err != nil {
		return
	}
	_ = dlg.SetIcon(a.iconApp)
	updatePreview()

	if dlg.Run() != walk.DlgCmdOK {
		return
	}

	if n := strings.TrimSpace(leNick.Text()); n != "" && n != a.hub.Nick() {
		a.hub.SetNick(n)
		a.cfg.Nick = n
		a.refreshHeader()
		a.refreshList()
	}
	if sm := truncateRunes(strings.TrimSpace(leStatMsg.Text()), 40); sm != a.cfg.StatusMsg {
		a.cfg.StatusMsg = sm
		a.hub.SetStatMsg(sm)
		a.refreshHeader()
	}
	a.cfg.Balloon = cbBalloon.Checked()
	a.cfg.StartInTray = cbTray.Checked()
	a.cfg.Sound = cbSound.Checked()
	a.cfg.SoundMsg = cbMsg.Checked()
	a.cfg.SoundOnline = cbOn.Checked()
	a.cfg.SoundOffline = cbOff.Checked()
	a.cfg.AutoAway = cbAutoAway.Checked()
	a.cfg.AutoAwayMins = int(neAwayMins.Value())
	if a.cfg.AutoAwayMins < 1 {
		a.cfg.AutoAwayMins = 5
	}
	if sdir := strings.TrimSpace(leSaveDir.Text()); sdir != "" {
		if sdir == store.DefaultInboxDir() {
			a.cfg.SaveDir = ""
		} else {
			a.cfg.SaveDir = sdir
		}
	}
	a.cfg.HistoryDays = int(neDays.Value())
	if a.cfg.HistoryDays < 7 {
		a.cfg.HistoryDays = 7
	}
	a.cfg.Status = a.hub.Status()
	a.cfg.AvatarID = a.hub.AvatarID()

	if err := store.SaveConfig(a.cfg); err != nil {
		walk.MsgBox(a.mw, T("Pengaturan"), T("Gagal menyimpan pengaturan: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	if err := SetAutostart(cbAuto.Checked()); err != nil {
		walk.MsgBox(a.mw, T("Pengaturan"), T("Gagal mengatur jalankan-otomatis: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
	store.PruneHistory(a.cfg.HistoryDays)
	a.balloon(T("Pengaturan tersimpan"), fmt.Sprintf(T("Pengaturan diperbarui. Riwayat disimpan %d hari."), a.cfg.HistoryDays))
}

// chooseAvatarDialog menampilkan 12 avatar bawaan untuk dipilih.
func (a *App) chooseAvatarDialog() (int, bool) {
	var dlg *walk.Dialog
	chosen := -2 // -2 = batal, 0..11 = pilihan

	ivs := make([]*walk.ImageView, builtinAvatarCount)
	children := make([]Widget, 0, builtinAvatarCount)
	for i := 0; i < builtinAvatarCount; i++ {
		id := i
		children = append(children, ImageView{
			AssignTo:    &ivs[i],
			MinSize:     Size{Width: 44, Height: 44},
			MaxSize:     Size{Width: 44, Height: 44},
			Mode:        ImageViewModeZoom,
			ToolTipText: fmt.Sprintf("Avatar %d", id+1),
			OnMouseDown: func(x, y int, btn walk.MouseButton) {
				if btn == walk.LeftButton {
					chosen = id
					dlg.Accept()
				}
			},
		})
	}

	if err := (Dialog{
		AssignTo: &dlg,
		Title:    T("Pilih Avatar Bawaan"),
		MinSize:  Size{Width: 280, Height: 230},
		Size:     Size{Width: 300, Height: 250},
		Font:     Font{Family: "Segoe UI", PointSize: 9},
		Layout:   VBox{Margins: Margins{Left: 14, Top: 14, Right: 14, Bottom: 14}, Spacing: 8},
		Children: []Widget{
			TextLabel{Text: T("Klik salah satu avatar (tampil sama di semua PC):")},
			Composite{
				Layout:   Grid{Columns: 4, Spacing: 8, MarginsZero: true},
				Children: children,
			},
		},
	}).Create(a.mw); err != nil {
		return -2, false
	}
	_ = dlg.SetIcon(a.iconApp)

	// gambar avatar diisi setelah widget dibuat
	for i, iv := range ivs {
		if iv == nil {
			continue
		}
		if ic, err := walk.NewIconFromImage(builtinAvatar(i, 44)); err == nil {
			_ = iv.SetImage(ic)
		}
	}

	dlg.Run()
	if chosen == -2 {
		return -2, false
	}
	return chosen, true
}
