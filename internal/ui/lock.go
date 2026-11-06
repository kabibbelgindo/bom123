//go:build windows

package ui

import (
	"belgindo/internal/store"

	"github.com/lxn/walk"
)

// Kunci percakapan (Update.txt): percakapan yang diberi password tidak bisa
// dibuka, dibaca lewat penampil riwayat, dilihat isinya di dialog info, atau
// dibaca pratinjau-nya di notifikasi — sampai password yang benar dimasukkan.
// Kunci tersimpan di config.json sehingga tetap berlaku setelah jendela
// ditutup dan aplikasi dibuka kembali.

// chatLocked true bila percakapan dengan kunci ini punya password.
func (a *App) chatLocked(key string) bool {
	return store.IsChatLocked(a.cfg, key)
}

// unlockGate meminta password bila percakapan terkunci.
// true = boleh melanjutkan; false = pengguna membatalkan atau password salah.
func (a *App) unlockGate(key string) bool {
	if key == "" || !a.chatLocked(key) {
		return true
	}
	password, ok := a.promptChatPassword(
		T("Percakapan Terkunci"),
		T("Masukkan password untuk membuka percakapan:"))
	if !ok {
		return false
	}
	if !store.VerifyChatPassword(a.cfg, key, password) {
		walk.MsgBox(a.mw, T("Password Salah"),
			T("Password percakapan tidak benar."),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return false
	}
	return true
}

// lockedPreview mengganti pratinjau isi pesan bila percakapannya terkunci,
// supaya notifikasi dan daftar tidak membocorkan isi.
func (a *App) lockedPreview(key, text string) string {
	if a.chatLocked(key) {
		return T("Pesan dilindungi password")
	}
	return text
}

// lockConversation membuat password baru untuk satu percakapan.
func (a *App) lockConversation(key string) {
	if key == "" {
		return
	}
	if a.chatLocked(key) {
		walk.MsgBox(a.mw, T("Kunci Percakapan"),
			T("Percakapan ini sudah memiliki password."),
			walk.MsgBoxOK|walk.MsgBoxIconInformation)
		return
	}
	password, ok := a.promptChatPassword(T("Buat Password Percakapan"),
		T("Password baru (minimal 3 karakter):"))
	if !ok {
		return
	}
	if len([]rune(password)) < 3 {
		walk.MsgBox(a.mw, T("Password Tidak Valid"),
			T("Password harus minimal 3 karakter."),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	confirm, ok := a.promptChatPassword(T("Konfirmasi Password"),
		T("Masukkan kembali password:"))
	if !ok {
		return
	}
	if password != confirm {
		walk.MsgBox(a.mw, T("Password Tidak Cocok"),
			T("Konfirmasi password tidak sama."),
			walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	if err := store.SetChatPassword(a.cfg, key, password); err != nil {
		walk.MsgBox(a.mw, T("Kunci Percakapan"),
			T("Gagal menyimpan password: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	// Jangan biarkan isi chat tetap terlihat setelah kunci dibuat.
	a.closeConversationWindows(key)
	walk.MsgBox(a.mw, T("Kunci Percakapan"),
		T("Password berhasil dibuat. Percakapan akan meminta password setiap kali dibuka."),
		walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

// unlockConversation menghapus password percakapan setelah password benar.
func (a *App) unlockConversation(key string) {
	if key == "" || !a.chatLocked(key) {
		return
	}
	if !a.unlockGate(key) {
		return
	}
	if err := store.RemoveChatPassword(a.cfg, key); err != nil {
		walk.MsgBox(a.mw, T("Buka Kunci Percakapan"),
			T("Gagal menghapus password: ")+err.Error(),
			walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	walk.MsgBox(a.mw, T("Buka Kunci Percakapan"),
		T("Password percakapan sudah dihapus."),
		walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

// closeConversationWindows menutup jendela yang sedang menampilkan
// percakapan ber-kunci key (perorangan maupun grup). Peta jendela memakai UID
// mentah sebagai kunci, sedangkan config.json memakai SafeKey, jadi keduanya
// dibandingkan.
func (a *App) closeConversationWindows(key string) {
	type closer interface{ Close() error }
	var wins []closer
	a.mu.Lock()
	for uid, c := range a.convos {
		if uid == key || store.SafeKey(uid) == key {
			if c.win != nil {
				wins = append(wins, c.win)
			}
		}
	}
	for gid, g := range a.groups {
		if gid == key || store.SafeKey(gid) == key {
			if g.win != nil {
				wins = append(wins, g.win)
			}
		}
	}
	a.mu.Unlock()
	for _, w := range wins {
		_ = w.Close()
	}
}
