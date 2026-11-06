//go:build windows

// Belgindo Office Messenger — chat internal kantor via LAN, tanpa internet,
// tanpa server, tanpa login. Cukup dijalankan di setiap PC.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"belgindo/internal/p2p"
	"belgindo/internal/store"
	"belgindo/internal/ui"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW  = user32.NewProc("MessageBoxW")
	procEnumWindows  = user32.NewProc("EnumWindows")
	procShowWindow   = user32.NewProc("ShowWindow")
	procSetForegrund = user32.NewProc("SetForegroundWindow")
	procTextLen      = user32.NewProc("GetWindowTextLengthW")
	procGetText      = user32.NewProc("GetWindowTextW")
)

func msgBoxWarn(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x30 /*MB_ICONWARNING*/)
}

// jendelaTemuan menyimpan HWND jendela utama instance yang sudah berjalan.
var jendelaTemuan uintptr

// enumCariJendela adalah callback EnumWindows; dipanggil satu kali sehingga
// NewCallback aman (tidak melebihi batas jumlah callback).
func enumCariJendela(hwnd, lparam uintptr) uintptr {
	if jendelaTemuan != 0 {
		return 0
	}
	n, _, _ := procTextLen.Call(hwnd)
	if n == 0 {
		return 1
	}
	buf := make([]uint16, n+2)
	procGetText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if !strings.HasPrefix(windows.UTF16ToString(buf), appWindowTitle) {
		return 1
	}
	jendelaTemuan = hwnd
	return 0
}

const appWindowTitle = "Belgindo Office Messenger"

// aktifkanJendelaLama menampilkan dan menaikkan jendela instance yang sudah
// jalan. Sebelumnya instance kedua hanya memunculkan MessageBox, sehingga
// pengguna yang menyembunyikan aplikasi ke tray mengira aplikasi macet.
func aktifkanJendelaLama() bool {
	jendelaTemuan = 0
	procEnumWindows.Call(windows.NewCallback(enumCariJendela), 0)
	if jendelaTemuan == 0 {
		return false
	}
	procShowWindow.Call(jendelaTemuan, 9 /*SW_RESTORE*/)
	procSetForegrund.Call(jendelaTemuan)
	return true
}

// pcName mengembalikan nama komputer (default nickname).
func pcName() string {
	if n := os.Getenv("COMPUTERNAME"); n != "" {
		return n
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "PC"
}

// mutexInstance dipegang selama proses hidup; menutupnya akan menghapus mutex
// sehingga instance kedua lolos tanpa terdeteksi.
var mutexInstance windows.Handle

// sudahJalan mencegah dua instance di satu PC.
func sudahJalan() bool {
	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr("Local\\BelgindoMessengerSingle"))
	if h != 0 {
		mutexInstance = h
	}
	return err != nil
}

func main() {
	tray := flag.Bool("tray", false, "mulai tersembunyi di tray (untuk autostart)")
	flag.Parse()

	if sudahJalan() {
		// Instance kedua: naikkan jendela yang sudah ada, bukan sekadar
		// MessageBox (aplikasi sering sedang sembunyi di tray).
		if !aktifkanJendelaLama() {
			msgBoxWarn(appWindowTitle,
				ui.T("Belgindo Office Messenger sudah berjalan di PC ini.\nPeriksa ikon tray di pojok kanan bawah."))
		}
		os.Exit(0)
	}

	ui.EnsureBrowserEmulation()

	cfg := store.LoadConfig()
	if cfg.Nick == "" {
		cfg.Nick = pcName() // nickname awal = nama PC, bisa diubah kapan saja
	}
	if !cfg.PrefsV2 {
		// pemasangan pertama: balon dan auto-away aktif default. Autostart
		// TIDAK diaktifkan diam-diam — pengguna yang menginginkannya memasang
		// sendiri lewat Pengaturan > Umum (perbaikan antisipasi.txt).
		cfg.Balloon = true
		cfg.AutoAway = true
		cfg.PrefsV2 = true
		_ = store.SaveConfig(cfg)
	}

	// Bahasa dipasang sekali di awal: seluruh teks dibuat setelah baris ini,
	// jadi tidak ada jendela yang tertinggal dalam bahasa lama.
	ui.SetLanguage(cfg.Language)

	hub, err := p2p.NewHub(p2p.Options{
		UID:            cfg.UID,
		Nick:           cfg.Nick,
		PC:             pcName(),
		Inbox:          cfg.InboxDir(),
		SeedPeers:      store.LoadPeers(),
		Stat:           cfg.Status,
		StatMsg:        cfg.StatusMsg,
		AvatarID:       cfg.AvatarID,
		AvatarPath:     store.OwnAvatarPath(),
		AvatarCacheDir: store.AvatarDir(),
	})
	if err != nil {
		msgBoxWarn("Belgindo Office Messenger", "Gagal menyiapkan jaringan: "+err.Error())
		os.Exit(1)
	}

	store.PruneHistory(cfg.HistoryDays)

	if err := ui.Run(cfg, hub, pcName(), *tray); err != nil {
		hub.Stop()
		msgBoxWarn("Belgindo Office Messenger",
			fmt.Sprintf("Gagal menjalankan aplikasi:\n%v\n\nPastikan port TCP 51515 dan UDP 51516 tidak dipakai aplikasi lain.", err))
		os.Exit(1)
	}

	hub.Stop()
	_ = store.SaveConfig(cfg)

	// Menu "Pilih Bahasa" menyimpan pilihan lalu berhenti di sini: teks menu,
	// judul jendela dan tooltip dibuat sekali saat widget dibangun, jadi hanya
	// proses baru yang bisa memakainya secara menyeluruh.
	if ui.RestartRequested() {
		if mutexInstance != 0 {
			windows.CloseHandle(mutexInstance)
			mutexInstance = 0
		}
		if err := ui.Relaunch(os.Args[1:]...); err != nil {
			msgBoxWarn("Belgindo Office Messenger", "Gagal membuka ulang aplikasi: "+err.Error())
		}
	}
}
