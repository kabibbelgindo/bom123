//go:build windows

package ui

// language.go menyediakan menu Bantuan > Pilih Bahasa.
//
// Seluruh teks antarmuka diambil lewat T(), tetapi judul jendela, menu dan
// tooltip dibuat sekali saat widget dibangun. Karena itu pergantian bahasa
// baru benar-benar bersih setelah aplikasi dibangun ulang: pilihan disimpan
// dulu, lalu pengguna ditawari membuka ulang — bukan membiarkan sebagian teks
// campur bahasa seperti pada percobaan sebelumnya.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var (
	restartMu   sync.Mutex
	restartWant bool
	langActions []*walk.Action
)

// RestartRequested dibaca cmd/messenger setelah ui.Run selesai: true berarti
// pengguna memilih membuka ulang sehingga proses baru boleh dijalankan.
func RestartRequested() bool {
	restartMu.Lock()
	defer restartMu.Unlock()
	return restartWant
}

// MarkRestartRequested menandai permintaan buka ulang.
func MarkRestartRequested() {
	restartMu.Lock()
	restartWant = true
	restartMu.Unlock()
}

// Relaunch menjalankan kembali berkas exe ini. Mutex instance harus dilepas
// pemanggil lebih dulu supaya proses baru tidak menganggap aplikasi masih
// berjalan.
// Relaunch menjalankan kembali berkas exe ini dengan tambahan argumen.
// Mutex instance harus dilepas pemanggil lebih dulu supaya proses baru tidak
// menganggap aplikasi masih berjalan.
func Relaunch(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}

// languageMenuItems menyusun submenu Bahasa; entri yang aktif diberi centang.
func (a *App) languageMenuItems() []MenuItem {
	langActions = make([]*walk.Action, len(store.Languages))
	items := make([]MenuItem, 0, len(store.Languages))
	for i, l := range store.Languages {
		idx, code := i, l.Code
		items = append(items, Action{
			AssignTo:  &langActions[idx],
			Text:      l.Native,
			Checkable: true,
			OnTriggered: func() {
				a.setLanguage(code)
			},
		})
	}
	a.syncLanguageChecks()
	return items
}

// setLanguage menyimpan bahasa terpilih lalu menawarkan buka ulang.
//
// Bahasa aktif baru dipasang saat proses-start; memakai bahasa baru di
// tengah sesi membuat sebagian teks (menu dan judul yang sudah dibuat) tetap
// berbahasa lama sementara pesan baru berbahasa baru — persis campur bahasa
// yang dikeluhkan. Jadi pilihan hanya disimpan sampai aplikasi dibuka ulang.
func (a *App) setLanguage(code string) {
	if !store.IsLanguageCode(code) || code == a.cfg.Language {
		return
	}
	a.cfg.Language = code
	if err := store.SaveConfig(a.cfg); err != nil {
		walk.MsgBox(a.mw, T("Pilih Bahasa"), T("Gagal menyimpan: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	a.syncLanguageChecks()

	if walk.MsgBox(a.mw, T("Pilih Bahasa"),
		T("Bahasa dipilih. Buka ulang aplikasi sekarang agar seluruh teks ikut bahasa ini?\n\nPilih Tidak bila ingin membuka ulang sendiri nanti."),
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
		MarkRestartRequested()
		a.quitApp()
	}
}

// syncLanguageChecks memasang centang pada bahasa yang tersimpan di config.
func (a *App) syncLanguageChecks() {
	for i, l := range store.Languages {
		if i < len(langActions) && langActions[i] != nil {
			_ = langActions[i].SetChecked(l.Code == a.cfg.Language)
		}
	}
}
