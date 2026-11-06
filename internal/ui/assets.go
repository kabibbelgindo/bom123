//go:build windows

package ui

import (
	_ "embed"
	"image"
	"image/png"
	"log"
)

//go:embed assets/tray_normal.png
var pngNormal []byte

//go:embed assets/tray_alert.png
var pngAlert []byte

//go:embed assets/app_icon_win.png
var pngAppIcon []byte

//go:embed assets/msg.wav
var wavMsg []byte

//go:embed assets/online.wav
var wavOnline []byte

//go:embed assets/offline.wav
var wavOffline []byte

func decodePNG(b []byte) image.Image {
	im, err := png.Decode(bytesReader(b))
	if err != nil {
		log.Printf("decode png: %v", err)
		return nil
	}
	return im
}
