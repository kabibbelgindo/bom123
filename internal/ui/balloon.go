//go:build windows

package ui

import (
	"sync"
	"time"
	"unsafe"

	"belgindo/internal/proto"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// TrayBalloon adalah jendela balon notifikasi mengambang di atas system tray.
// Muncul saat ada pesan masuk, memiliki tombol X untuk menutup,
// dan jika diklik akan membuka ruang percakapan.
type TrayBalloon struct {
	app        *App
	win        *walk.MainWindow
	lblTitle   *walk.TextLabel
	lblBody    *walk.TextLabel
	lblHint    *walk.TextLabel
	ivIcon     *walk.ImageView
	btnClose   *walk.PushButton
	currentUID string
	mu         sync.Mutex
	timer      *time.Timer
}

func newTrayBalloon(a *App) *TrayBalloon {
	return &TrayBalloon{app: a}
}

func (tb *TrayBalloon) init() error {
	if tb.win != nil {
		return nil
	}

	err := (MainWindow{
		AssignTo:   &tb.win,
		Title:      T("Belgindo Notifikasi"),
		MinSize:    Size{Width: 310, Height: 90},
		Size:       Size{Width: 320, Height: 96},
		Font:       Font{Family: "Segoe UI", PointSize: 9},
		Background: SolidColorBrush{Color: walk.RGB(0xFA, 0xFC, 0xFF)},
		Layout:     VBox{Margins: Margins{Left: 10, Top: 8, Right: 10, Bottom: 8}, Spacing: 3},
		Children: []Widget{
			// Baris Header: Ikon + Nama Pengirim + Tombol Tutup (X)
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					ImageView{
						AssignTo: &tb.ivIcon,
						MinSize:  Size{Width: 18, Height: 18},
						MaxSize:  Size{Width: 18, Height: 18},
						Mode:     ImageViewModeZoom,
					},
					TextLabel{
						AssignTo:  &tb.lblTitle,
						Text:      T("Pesan Baru"),
						Font:      Font{Family: "Segoe UI", PointSize: 9, Bold: true},
						TextColor: walk.RGB(0x1F, 0x4E, 0x79),
						OnMouseDown: func(x, y int, btn walk.MouseButton) {
							if btn == walk.LeftButton {
								tb.handleClick()
							}
						},
					},
					HSpacer{},
					PushButton{
						AssignTo:    &tb.btnClose,
						Text:        "✕",
						Font:        Font{Family: "Segoe UI", PointSize: 8, Bold: true},
						MinSize:     Size{Width: 22, Height: 20},
						MaxSize:     Size{Width: 22, Height: 20},
						ToolTipText: T("Tutup notifikasi"),
						OnClicked: func() {
							tb.hide()
						},
					},
				},
			},
			// Baris Pesan: Cuplikan isi chat
			TextLabel{
				AssignTo:  &tb.lblBody,
				Text:      T("Isi pesan..."),
				Font:      Font{Family: "Segoe UI Emoji", PointSize: 9},
				TextColor: walk.RGB(0x22, 0x2A, 0x35),
				OnMouseDown: func(x, y int, btn walk.MouseButton) {
					if btn == walk.LeftButton {
						tb.handleClick()
					}
				},
			},
			// Baris Bawah: Petunjuk klik
			TextLabel{
				AssignTo:  &tb.lblHint,
				Text:      T("Klik untuk membalas percakapan"),
				Font:      Font{Family: "Segoe UI", PointSize: 8, Italic: true},
				TextColor: walk.RGB(0x00, 0x78, 0xD4),
				OnMouseDown: func(x, y int, btn walk.MouseButton) {
					if btn == walk.LeftButton {
						tb.handleClick()
					}
				},
			},
		},
	}).Create()
	if err != nil {
		return err
	}

	hwnd := tb.win.Handle()

	// Ubah style jendela menjadi toolwindow popup mengambang tanpa tombol taskbar
	exStyle := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	exStyle |= win.WS_EX_TOOLWINDOW | win.WS_EX_TOPMOST
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, exStyle)

	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style &^= (win.WS_CAPTION | win.WS_THICKFRAME | win.WS_MINIMIZEBOX | win.WS_MAXIMIZEBOX | win.WS_SYSMENU)
	style |= uint32(win.WS_POPUP | win.WS_BORDER)
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style))

	if tb.app != nil && tb.app.iconApp != nil {
		_ = tb.ivIcon.SetImage(tb.app.iconApp)
	}

	return nil
}

// Show menampilkan balon notifikasi di pojok kanan bawah (di atas tray).
// key adalah UID percakapan perorangan atau ID grup; judul dan isi sudah
// disusun pemanggil sehingga hanya SATU notifikasi yang muncul per pesan.
func (tb *TrayBalloon) Show(key, title, text string) {
	if tb.app == nil || tb.app.mw == nil {
		return
	}

	tb.app.mw.Synchronize(func() {
		if err := tb.init(); err != nil {
			return
		}

		tb.mu.Lock()
		tb.currentUID = key
		if tb.timer != nil {
			tb.timer.Stop()
		}
		tb.mu.Unlock()

		tb.lblTitle.SetText(truncateRunes(title, 26))
		tb.lblBody.SetText(truncateRunes(text, 100))

		// Hitung posisi di pojok kanan bawah, tepat di atas tray / taskbar.
		// Ukuran dihitung dalam PIXEL FISIK mengikuti DPI jendela: manifest
		// PerMonitorV2 membuat SetWindowPos memakai piksel, jadi 320x96 DIP
		// saja akan terpotong pada 125-150%.
		var r win.RECT
		if win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&r), 0) {
			dpi := dpiOfJendela(tb.win.Handle())
			w := dip2px(320, dpi)
			h := dip2px(96, dpi)
			x := int(r.Right) - w - 12
			y := int(r.Bottom) - h - 12
			if x < int(r.Left) {
				x = int(r.Left)
			}
			if y < int(r.Top) {
				y = int(r.Top)
			}
			win.SetWindowPos(tb.win.Handle(), win.HWND_TOPMOST, int32(x), int32(y), int32(w), int32(h),
				win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW|win.SWP_FRAMECHANGED)
		} else {
			tb.win.Show()
		}

		// Otomatis sembunyikan setelah 8 detik jika tidak diklik
		tb.mu.Lock()
		tb.timer = time.AfterFunc(8*time.Second, func() {
			if tb.app != nil && tb.app.mw != nil {
				tb.app.mw.Synchronize(func() {
					tb.hide()
				})
			}
		})
		tb.mu.Unlock()
	})
}

func (tb *TrayBalloon) hide() {
	tb.mu.Lock()
	if tb.timer != nil {
		tb.timer.Stop()
	}
	tb.mu.Unlock()
	if tb.win != nil && tb.win.Visible() {
		tb.win.Hide()
	}
}

// openConversation membuka ruang yang sesuai: kunci yang berbentuk ID grup
// membuka ruang grup, selain itu percakapan perorangan.
func (a *App) openConversation(key string) {
	if proto.ValidGroupID(key) {
		a.openGroup(key)
		return
	}
	a.openConvo(key)
}

func (tb *TrayBalloon) handleClick() {
	tb.mu.Lock()
	key := tb.currentUID
	tb.mu.Unlock()

	tb.hide()

	if key != "" && tb.app != nil {
		tb.app.openConversation(key)
	}
}
