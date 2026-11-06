//go:build windows

package ui

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const runKeyName = "BelgindoMessenger"

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// AutostartEnabled melaporkan apakah aplikasi terdaftar di autostart Windows.
func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runKeyName)
	return err == nil
}

// SetAutostart mendaftarkan / menghapus autostart Windows (berjalan di tray).
func SetAutostart(enable bool) error {
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		return k.SetStringValue(runKeyName, `"`+filepath.Clean(exe)+`" -tray`)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(runKeyName); err != nil {
		// sudah tidak ada dianggap sukses
		return nil
	}
	return nil
}

// EnsureBrowserEmulation mendaftarkan executable ke FEATURE_BROWSER_EMULATION
// dengan nilai 11001 (IE11 Edge Mode) agar WebView menggunakan DirectWrite
// dan merender emoji dalam warna penuh (full color Segoe UI Emoji).
func EnsureBrowserEmulation() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exeName := filepath.Base(exe)
	k, _, err := registry.CreateKey(registry.CURRENT_USER,
		`Software\Microsoft\Internet Explorer\Main\FeatureControl\FEATURE_BROWSER_EMULATION`,
		registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.SetDWordValue(exeName, 11001)
	_ = k.SetDWordValue("BelgindoOfficeMessenger.exe", 11001)
	_ = k.SetDWordValue("messenger.exe", 11001)
}
