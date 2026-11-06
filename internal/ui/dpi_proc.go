//go:build windows

package ui

// dpi_proc.go menyediakan DPI efektif sebuah jendela. Manifest aplikasi
// memakai PerMonitorV2, sehingga SetWindowPos menuntut PIXEL FISIK; memakai
// konstanta DIP mentah (mis. 320x96) membuat balon terpotong pada 125-150%.

import (
	"github.com/lxn/win"
)

// dpiOfJendela mengembalikan DPI jendela, 96 (100%) bila sistem tidak
// menyediannya.
func dpiOfJendela(hwnd win.HWND) int {
	if dpi := win.GetDpiForWindow(hwnd); dpi >= 96 {
		return int(dpi)
	}
	hdc := win.GetDC(hwnd)
	if hdc == 0 {
		return 96
	}
	defer win.ReleaseDC(hwnd, hdc)
	if x := int(win.GetDeviceCaps(hdc, win.LOGPIXELSX)); x >= 96 {
		return x
	}
	return 96
}

// dip2px mengonversi ukuran DIP menjadi piksel pada DPI jendela tertentu.
func dip2px(dip, dpi int) int {
	return dip * dpi / 96
}
