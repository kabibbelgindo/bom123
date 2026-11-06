//go:build !windows

package p2p

import "errors"

// diskFreeBytes tidak dilaporkan di luar Windows; pemeriksaan ruang disk
// dilewati (nol = tidak pasti, bukan menolak).
func diskFreeBytes(string) (int64, error) { return 0, errors.New("tidak tersedia") }
