//go:build windows

// avatar.go — cache ikon walk untuk foto profil, avatar bawaan, dan
// ikon status. Gambar mentahnya dibuat oleh imggen.go (murni Go).
package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"sync"

	"belgindo/internal/proto"
	"belgindo/internal/store"

	"github.com/lxn/walk"
)

// statusLabels teks status kehadiran (urutan = proto.Stat*).
var statusLabels = [proto.StatCount]string{
	"Online",
	"Sibuk",
	"Jangan Ganggu",
	"Segera Kembali",
	"Pergi",
	"Tampil Offline",
}

// statusColors warna titik status (urutan = proto.Stat*).
var statusColors = [proto.StatCount]rgb{
	{0x2E, 0xB3, 0x4E}, // online  - hijau
	{0xF2, 0x8B, 0x1B}, // sibuk   - oranye
	{0xE3, 0x3A, 0x2E}, // DND     - merah
	{0x1F, 0x9E, 0xD6}, // segera kembali - biru muda
	{0xF5, 0xC2, 0x1E}, // pergi   - kuning
	{0x9B, 0xA6, 0xB1}, // tampil offline - abu-abu
}

func statusLabel(st int) string {
	if st < 0 || st >= proto.StatCount {
		return statusLabels[proto.StatOnline]
	}
	return T(statusLabels[st])
}

// statusCaptions adalah keterangan singkat di bawah ikon status header.
var statusCaptions = [proto.StatCount]string{
	"Available",
	"Busy",
	"DND",
	"BRB",
	"Away",
	"Offline",
}

func statusCaption(st int) string {
	if st < 0 || st >= proto.StatCount {
		return statusCaptions[proto.StatOnline]
	}
	return T(statusCaptions[st])
}

func statusColorRGB(st int) rgb {
	if st < 0 || st >= proto.StatCount {
		return statusColors[proto.StatOnline]
	}
	return statusColors[st]
}

// statusColorWarna warna status dalam tipe walk.Color.
func statusColorWalk(st int) walk.Color {
	c := statusColorRGB(st)
	return walk.RGB(c.r, c.g, c.b)
}

// ---------- cache ikon walk ----------

type iconCache struct {
	mu sync.Mutex
	m  map[string]*walk.Icon
}

func newIconCache() *iconCache {
	return &iconCache{m: make(map[string]*walk.Icon)}
}

// get mengembalikan ikon dari cache, atau membangunnya lewat make bila belum ada.
func (c *iconCache) get(key string, make func() (*walk.Icon, error)) *walk.Icon {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ic, ok := c.m[key]; ok {
		return ic
	}
	ic, err := make()
	if err != nil || ic == nil {
		return nil
	}
	c.m[key] = ic
	return ic
}

// invalidatePrefix menghapus semua ikon yang key-nya diawali prefix.
func (c *iconCache) invalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
}

// ---------- ikon milik App ----------

// statusIcon titik status untuk header/menu.
func (a *App) statusIcon(st int, size int) *walk.Icon {
	return a.icons.get(fmt.Sprintf("st|%d|%d", st, size), func() (*walk.Icon, error) {
		return walk.NewIconFromImage(dotImage(statusColorRGB(st), size))
	})
}

// statusIconHeader memakai warna biru header di luar lingkaran status.
func (a *App) statusIconHeader(st int, size int) *walk.Icon {
	return a.icons.get(fmt.Sprintf("sth|%d|%d", st, size), func() (*walk.Icon, error) {
		return walk.NewIconFromImage(dotImageOn(statusColorRGB(st), size, rgb{0x1F, 0x4E, 0x79}))
	})
}

// groupIcon ikon untuk baris grup pada daftar pengguna.
func (a *App) groupIcon(size int) *walk.Icon {
	return a.icons.get(fmt.Sprintf("grp|%d", size), func() (*walk.Icon, error) {
		return walk.NewIconFromImage(groupImage(size))
	})
}

// ownAvatarImage gambar avatar milik sendiri:
// foto upload > avatar bawaan > identicon dari UID.
func (a *App) ownAvatarImage(size int) image.Image {
	if im, err := decodeSquareImage(store.OwnAvatarPath()); err == nil {
		return resizeSquareCenter(im, size)
	}
	if id := a.hub.AvatarID(); id >= 0 {
		return builtinAvatar(id, size)
	}
	return identicon("auto-"+a.cfg.UID, size)
}

func (a *App) ownAvatarIcon(size int) *walk.Icon {
	key := fmt.Sprintf("own|%s|%d|%d", a.hub.OwnAvatarHash(), a.hub.AvatarID(), size)
	return a.icons.get(key, func() (*walk.Icon, error) {
		return walk.NewIconFromImage(a.ownAvatarImage(size))
	})
}

// peerAvatarImage gambar avatar untuk rekan:
// foto (cache) > avatar bawaan rekan > identicon dari UID.
func peerAvatarImage(uid, avatarHash string, avatarID, size int) image.Image {
	if avatarHash != "" {
		path := filepath.Join(store.AvatarDir(), uid+".png")
		if im, err := decodeSquareImage(path); err == nil {
			return resizeSquareCenter(im, size)
		}
	}
	if avatarID >= 0 {
		return builtinAvatar(avatarID, size)
	}
	return identicon("auto-"+uid, size)
}

// peerAvatarIcon ikon avatar rekan untuk tabel.
func (a *App) peerAvatarIcon(uid, avatarHash string, avatarID, size int) *walk.Icon {
	src := avatarHash
	if src == "" {
		if avatarID >= 0 {
			src = fmt.Sprintf("builtin%d", avatarID)
		} else {
			src = "auto"
		}
	}
	key := fmt.Sprintf("av|%s|%s|%d", uid, src, size)
	return a.icons.get(key, func() (*walk.Icon, error) {
		return walk.NewIconFromImage(peerAvatarImage(uid, avatarHash, avatarID, size))
	})
}

// invalidateOwnAvatarIcons dipanggil setelah foto/avatar sendiri berubah.
func (a *App) invalidateOwnAvatarIcons() {
	a.icons.invalidatePrefix("own|")
}

// invalidatePeerAvatarIcons dipanggil saat foto rekan baru diterima.
func (a *App) invalidatePeerAvatarIcons(uid string) {
	a.icons.invalidatePrefix("av|" + uid + "|")
}
