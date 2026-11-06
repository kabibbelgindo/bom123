package p2p

import (
	"golang.org/x/sys/windows"
)

// diskFreeBytes melaporkan byte bebas di volume tempat dir berada.
func diskFreeBytes(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var bebas, total, bebasTotal uint64
	if err := windows.GetDiskFreeSpaceEx(p, &bebas, &total, &bebasTotal); err != nil {
		return 0, err
	}
	// bebasBytesAvailableToCaller sudah memperhitungkan pembatasan akun.
	return int64(bebas), nil
}
