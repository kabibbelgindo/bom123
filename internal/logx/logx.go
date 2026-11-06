// Package logx menulis jejak aktivitas aplikasi ke berkas log dan menjaga
// aplikasi tetap hidup ketika sebuah goroutine mengalami panic.
package logx

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// maxLogBytes ukuran maksimum berkas log sebelum diputar ke app.log.1.
const maxLogBytes = 2 << 20

var (
	mu     sync.Mutex
	file   *os.File
	writer = &sizeWriter{}
)

type sizeWriter struct{}

func (sizeWriter) Write(p []byte) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return len(p), nil
	}
	n, err := file.Write(p)
	if size, serr := file.Seek(0, io.SeekCurrent); serr == nil && size > maxLogBytes {
		rotateLocked()
	}
	return n, err
}

// rotateLocked memutar app.log -> app.log.1 (panggil dengan mu terkunci).
func rotateLocked() {
	if file == nil {
		return
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Rename(name, name+".1")
	if n, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		file = n
	} else {
		file = nil
	}
}

// Init mengaktifkan penulisan log ke <dir>/app.log. Kegagalan diabaikan oleh
// pemanggil: aplikasi harus tetap jalan tanpa log.
func Init(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	mu.Lock()
	if file != nil {
		_ = file.Close()
	}
	file = f
	mu.Unlock()
	log.SetOutput(writer)
	log.SetFlags(log.LstdFlags)
	log.Printf("logx: log aktif di %s", f.Name())
	return nil
}

// Close menutup berkas log.
func Close() {
	mu.Lock()
	if file != nil {
		_ = file.Close()
		file = nil
	}
	mu.Unlock()
	log.SetOutput(io.Discard)
}

// Printf menulis satu baris log (aman walau log belum aktif).
func Printf(format string, args ...interface{}) {
	log.Printf(format, args...)
}

// Go menjalankan fn pada goroutine terpisah yang terlindungi dari panic.
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)
		fn()
	}()
}

// Do menjalankan fn di goroutine pemanggil dengan perlindungan panic.
func Do(name string, fn func()) {
	defer Recover(name)
	fn()
}

// Recover dipakai sebagai defer paling luar pada setiap goroutine.
func Recover(name string) {
	if r := recover(); r != nil {
		log.Printf("logx: panic di %s: %v\n%s", name, r, debug.Stack())
	}
}
