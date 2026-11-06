//go:build windows

package ui

import (
	"bytes"
	"io"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// PlaySoundW dari winmm.dll untuk memainkan WAV ter-embed dari memori.
var (
	procPlaySound = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")
)

const (
	sndAsync     = 0x0001
	sndNodefault = 0x0002
	sndMemory    = 0x0004
)

// Jenis peristiwa yang bisa diberi bunyi sendiri di jendela Pengaturan.
const (
	sndMsg = iota + 1
	sndOnline
	sndOffline
)

// currentWav menjaga buffer tetap hidup selara suara diputar (SND_ASYNC).
var currentWav []byte

func playWAV(data []byte) {
	if len(data) == 0 {
		return
	}
	currentWav = data
	go func() {
		defer func() { _ = recover() }()
		procPlaySound.Call(uintptr(unsafe.Pointer(&currentWav[0])), 0, sndMemory|sndAsync|sndNodefault)
		// beri waktu playback membaca buffer sebelum GC boleh menyentuhnya
		time.Sleep(2 * time.Second)
		runtime.KeepAlive(currentWav)
	}()
}
