//go:build windows

package ui

import (
	"fmt"
	"strings"

	"belgindo/internal/proto"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// showBroadcast — kirim pesan ke semua pengguna online.
func (a *App) showBroadcast() {
	var dlg *walk.Dialog
	var te *walk.TextEdit
	var db, cb *walk.PushButton

	if err := (Dialog{
		AssignTo:      &dlg,
		Title:         T("Kirim ke Semua (Broadcast)"),
		MinSize:       Size{Width: 380, Height: 240},
		Size:          Size{Width: 380, Height: 240},
		Font:          Font{Family: "Segoe UI", PointSize: 9},
		Layout:        VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 8},
		DefaultButton: &db,
		CancelButton:  &cb,
		Children: []Widget{
			Label{Text: T("Pesan akan dikirim ke semua pengguna yang online:")},
			TextEdit{AssignTo: &te, VScroll: true},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &db, Text: T("Kirim"), OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cb, Text: T("Batal"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}).Create(a.mw); err != nil {
		return
	}

	if dlg.Run() == walk.DlgCmdOK {
		text := strings.TrimSpace(te.Text())
		if text == "" {
			return
		}
		go func() {
			okN, failN := a.hub.SendBroadcast(text)
			a.mw.Synchronize(func() {
				walk.MsgBox(a.mw, T("Broadcast"),
					fmt.Sprintf(T("Pesan terkirim ke %d pengguna online (%d gagal)."), okN, failN),
					walk.MsgBoxOK|walk.MsgBoxIconInformation)
			})
		}()
	}
}

// showAbout — menu Bantuan > Tentang.
//
// Baris versi dicetak dari proto.Rev dan naik setiap ada perubahan, supaya
// pengguna bisa membedakan build yang terpasang di tiap PC.
func (a *App) showAbout() {
	walk.MsgBox(nil, T("Tentang Belgindo Office Messenger"),
		"Belgindo Office Messenger "+proto.VersionLabel()+"\n\n"+
			T("Chat internal kantor melalui jaringan LAN/WiFi — tanpa internet, tanpa server, tanpa login.")+"\n\n"+
			T("Ubah nama, foto profil, dan status lewat ikon status di jendela utama\n"+
				"atau menu Pengaturan > Pengaturan > Profil Saya.")+"\n\n"+
			T("Tombol X hanya menyembunyikan aplikasi ke tray; klik ikon tray untuk membuka lagi.")+"\n"+
			T("Port jaringan: TCP 51515 (chat & file), UDP 51516 (kehadiran)."),
		walk.MsgBoxOK|walk.MsgBoxIconInformation)
}
