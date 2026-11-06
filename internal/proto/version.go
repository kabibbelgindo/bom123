package proto

// Versi yang dilihat pengguna di menu Bantuan > Tentang Belgindo.
//
// Rev adalah nomor revisi build dan WAJIB dinaikkan setiap kali ada perubahan
// yang dikirim ke pengguna. Alasannya: beberapa PC memegang installer berbeda
// dan hanya layar Tentang yang bisa dipakai untuk memastikan build mana yang
// sedang berjalan. Release tetap mengikuti MyAppVersion pada installer.iss.
const (
	Release = "1.0.0"
	Rev     = "1.2.3"
)

// VersionLabel mengembalikan label versi satu baris, mis. "v1.0.0 (rev 1.2.1)".
func VersionLabel() string { return "v" + Release + " (rev " + Rev + ")" }
