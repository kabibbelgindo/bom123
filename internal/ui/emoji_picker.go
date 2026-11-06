//go:build windows

package ui

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"belgindo/internal/store"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	procKeybdEvent = windows.NewLazySystemDLL("user32.dll").NewProc("keybd_event")
)

// openWindowsEmojiPicker memicu panel emoji bawaan Windows 10/11 (Win + .).
func openWindowsEmojiPicker() {
	const (
		vkLWin      = 0x5B
		vkOemPeriod = 0xBE
		keyeventfUp = 0x0002
	)
	procKeybdEvent.Call(vkLWin, 0, 0, 0)
	procKeybdEvent.Call(vkOemPeriod, 0, 0, 0)
	procKeybdEvent.Call(vkOemPeriod, 0, keyeventfUp, 0)
	procKeybdEvent.Call(vkLWin, 0, keyeventfUp, 0)
}

type emojiCategory struct {
	ID     string
	Name   string
	Icon   string
	Emojis []string
}

var allEmojiCategories = []emojiCategory{
	{
		ID:   "wajah",
		Name: "Wajah & Senyum",
		Icon: "😀",
		Emojis: []string{
			"😀", "😃", "😄", "😁", "😆", "😅", "😂", "🤣", "🥲", "☺️",
			"😊", "😇", "🙂", "🙃", "😉", "😌", "😍", "🥰", "😘", "😗",
			"😙", "😚", "😋", "😛", "😜", "🤪", "😝", "🤑", "🤗", "🤭",
			"🤫", "🤔", "🤐", "🤨", "😐", "😑", "😶", "😏", "😒", "🙄",
			"😬", "🤥", "😔", "😪", "🤤", "😴", "😷", "🤒", "🤕", "🤢",
			"🤮", "🤧", "🥵", "🥶", "🥴", "😵", "🤯", "🤠", "🥳", "🥸",
			"😎", "🤓", "🧐", "😕", "😟", "🙁", "😮", "😯", "😲", "😳",
			"🥺", "😦", "😧", "😨", "😰", "😥", "😢", "😭", "😱", "😖",
			"😣", "😞", "😓", "😩", "😫", "🥱", "😤", "😡", "😠", "🤬",
			"😈", "👿", "💀", "💩", "🤡", "👻", "👽", "🤖", "😺", "😸",
		},
	},
	{
		ID:   "tangan",
		Name: "Tangan & Tubuh",
		Icon: "👍",
		Emojis: []string{
			"👍", "👎", "👌", "🤌", "🤏", "✌️", "🤞", "🤟", "🤘", "🤙",
			"👈", "👉", "👆", "🖕", "👇", "☝️", "👋", "🤚", "🖐️", "✋",
			"🖖", "✊", "👊", "🤛", "🤜", "👏", "🙌", "👐", "🤲", "🤝",
			"🙏", "✍️", "💅", "🤳", "💪", "🦾", "🦵", "🦿", "🦶", "👂",
			"👃", "🧠", "🫀", "🫁", "🦷", "🦴", "👀", "👁️", "👅", "👄",
			"👶", "👧", "🧒", "👦", "👩", "🧑", "👨", "👱", "🧔", "👵",
		},
	},
	{
		ID:   "hati",
		Name: "Hati & Cinta",
		Icon: "❤️",
		Emojis: []string{
			"❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "🤎", "💔",
			"❣️", "💕", "💞", "💓", "💗", "💖", "💘", "💝", "💟", "💌",
			"💋", "🫂", "😻", "🏩", "💐", "🌹", "🥀", "🌺", "🌸", "🌼",
			"🌻", "🌞", "⭐", "🌟", "✨", "💫", "🔥", "💥", "💯", "💢",
		},
	},
	{
		ID:   "kantor",
		Name: "Kantor & Kerja",
		Icon: "💼",
		Emojis: []string{
			"💼", "📁", "📂", "📄", "📃", "📑", "📊", "📈", "📉", "📜",
			"📋", "📅", "📆", "📇", "🗃️", "🗳️", "🗄️", "📌", "📍", "📎",
			"🖇️", "📏", "📐", "✂️", "🗂️", "🗑️", "🔒", "🔓", "🔏", "🔐",
			"🔑", "🗝️", "🔨", "🔧", "🪛", "🔩", "⚙️", "🖥️", "💻", "⌨️",
			"🖱️", "🖨️", "📱", "☎️", "📞", "📟", "📠", "✉️", "📧", "📨",
			"📩", "📦", "🏷️", "💰", "💵", "💳", "🧾", "📝", "📢", "📣",
		},
	},
	{
		ID:   "makanan",
		Name: "Makanan & Minum",
		Icon: "☕",
		Emojis: []string{
			"☕", "🍵", "🧃", "🥤", "🧋", "🍶", "🍺", "🍻", "🥂", "🍷",
			"🥃", "🍸", "🍹", "🍾", "🍕", "🍔", "🍟", "🌭", "🥪", "🌮",
			"🌯", "🥗", "🍜", "🍝", "🍞", "🥐", "🥖", "🍩", "🍪", "🎂",
			"🍰", "🧁", "🍫", "🍬", "🍭", "🍎", "🍌", "🍉", "🍇", "🍓",
			"🍒", "🍑", "🥭", "🍍", "🥥", "🥝", "🥑", "🍿", "🥜", "🍳",
		},
	},
	{
		ID:   "aktivitas",
		Name: "Aktivitas & Pesta",
		Icon: "🎉",
		Emojis: []string{
			"🎉", "🎊", "🎈", "🎁", "🎀", "🏆", "🥇", "🥈", "🥉", "🏅",
			"🎖️", "🎯", "🎮", "🎲", "🎨", "🎬", "🎤", "🎧", "🎼", "🎵",
			"🎶", "⚽", "🏀", "🏈", "⚾", "🎾", "🏐", "🏉", "🎱", "🏓",
			"🏸", "🥅", "🥊", "🥋", "🎳", "⛳", "🚴", "🏋️", "🧗", "🏊",
			"🎪", "🎫", "🎟️", "🔔", "🔕",
		},
	},
	{
		ID:   "simbol",
		Name: "Simbol & Tanda",
		Icon: "✅",
		Emojis: []string{
			"✅", "❌", "⭕", "🛑", "⛔", "🚫", "⚠️", "❓", "❗", "❔",
			"❕", "💡", "🟢", "🔴", "🟡", "🔵", "🟣", "🟠", "⚪", "⚫",
			"⬛", "⬜", "🏁", "🚩", "💬", "💭", "🗯️", "💤", "⚡", "🌈",
			"☔", "☀️", "⛅", "🌧️", "❄️", "🆗", "🆒", "🆙", "0️⃣", "1️⃣",
			"2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟",
		},
	},
}

func buildEmojiPickerHTML() string {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><style>" +
		"*{box-sizing:border-box;margin:0;padding:0}html,body{width:100%;height:100%;overflow:hidden;background:#FFF}" +
		"body{font-family:'Segoe UI Emoji','Segoe UI',sans-serif;user-select:none}" +
		".emoji-grid{width:100%;height:100%;overflow-y:auto;padding:7px;display:flex;flex-wrap:wrap;align-content:flex-start;gap:3px}" +
		".emoji-btn{width:31px;height:31px;display:inline-flex;align-items:center;justify-content:center;font-family:'Segoe UI Emoji','Segoe UI',sans-serif;font-size:20px;cursor:pointer;border-radius:8px;background:transparent;border:0;line-height:1}" +
		".emoji-btn:hover{background:#DCEAF6;transform:scale(1.15)}</style><script>" +
		"function pick(em){window.location.href='app://emoji/'+encodeURIComponent(em)}" +
		// Esc diurus di dalam halaman: selama fokus masih di WebView, tombol itu
		// tidak pernah sampai ke jendela walk.
		"document.onkeydown=function(e){e=e||window.event;if((e.keyCode||e.which)===27){window.location.href='app://close';}};" +
		"</script></head><body tabindex=\"-1\" oncontextmenu=\"return false\"><div class=\"emoji-grid\">")
	for _, cat := range allEmojiCategories {
		for _, em := range cat.Emojis {
			sb.WriteString(fmt.Sprintf("<div class=\"emoji-btn\" onclick=\"pick('%s')\">%s</div>\n", em, em))
		}
	}
	sb.WriteString("</div></body></html>")
	return sb.String()
}

// showEmojiPicker membuka jendela pemilih emoji berwarna penuh ala WhatsApp.
func showEmojiPicker(owner walk.Form, onSelect func(emoji string)) {
	var dlg *walk.Dialog
	var wv *walk.WebView
	var cb *walk.PushButton

	htmlContent := buildEmojiPickerHTML()
	htmlPath := filepath.Join(store.Dir(), "emoji_picker.html")
	_ = os.WriteFile(htmlPath, []byte(htmlContent), 0o644)

	if err := (Dialog{
		AssignTo:     &dlg,
		Title:        "",
		MinSize:      Size{Width: 240, Height: 240},
		Size:         Size{Width: 240, Height: 240},
		CancelButton: &cb,
		OnMouseDown: func(x, y int, btn walk.MouseButton) {
			if btn == walk.LeftButton {
				win.ReleaseCapture()
				win.SendMessage(dlg.Handle(), win.WM_NCLBUTTONDOWN, uintptr(win.HTCAPTION), 0)
			}
		},
		OnKeyDown: func(key walk.Key) {
			if key == walk.KeyEscape {
				dlg.Accept()
			}
		},
		Layout:     VBox{Margins: Margins{Left: 1, Top: 1, Right: 1, Bottom: 1}, SpacingZero: true},
		Background: SolidColorBrush{Color: walk.RGB(0x8C, 0x98, 0xA6)},
		Children: []Widget{
			WebView{
				AssignTo: &wv,
			},
			// Tombol batal tersembunyi: message loop walk menerjemahkan Esc menjadi
			// IDCANCEL, dan IDCANCEL hanya ditutup bila dialog punya CancelButton.
			PushButton{
				AssignTo:  &cb,
				Visible:   false,
				OnClicked: func() { dlg.Cancel() },
			},
		},
	}).Create(owner); err != nil {
		return
	}
	// Dialog Walk memakai caption/frame bawaan. Buang seluruh frame agar panel
	// tetap popup ringan, lalu gambar border 1 px sendiri (background dialog
	// yang muncul di tepi) karena border bawaan Windows nyaris tak terlihat.
	style := win.GetWindowLong(dlg.Handle(), win.GWL_STYLE)
	style &^= int32(win.WS_CAPTION | win.WS_SYSMENU | win.WS_THICKFRAME | win.WS_BORDER | win.WS_DLGFRAME)
	win.SetWindowLong(dlg.Handle(), win.GWL_STYLE, style)
	exStyle := win.GetWindowLong(dlg.Handle(), win.GWL_EXSTYLE)
	exStyle &^= int32(win.WS_EX_CLIENTEDGE | win.WS_EX_DLGMODALFRAME | win.WS_EX_WINDOWEDGE | win.WS_EX_STATICEDGE)
	win.SetWindowLong(dlg.Handle(), win.GWL_EXSTYLE, exStyle)
	win.SetWindowPos(dlg.Handle(), 0, 0, 0, 0, 0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)

	wv.Navigating().Attach(func(e *walk.WebViewNavigatingEventData) {
		u := appURL(e.Url())
		if strings.HasPrefix(u, "app://emoji/") {
			e.SetCanceled(true)
			raw := strings.TrimPrefix(u, "app://emoji/")
			decoded, err := url.QueryUnescape(raw)
			if err == nil && decoded != "" {
				onSelect(decoded)
				dlg.Accept()
			}
		} else if u == "app://winemoji" {
			e.SetCanceled(true)
			openWindowsEmojiPicker()
		} else if u == "app://close" {
			e.SetCanceled(true)
			dlg.Accept()
		}
	})

	_ = wv.SetURL(htmlPath)

	// Letakkan panel di tengah bawah layar, tepat di atas taskbar.
	var workArea win.RECT
	if win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&workArea), 0) {
		const panelW, panelH = 240, 240
		x := int(workArea.Left) + (int(workArea.Right-workArea.Left)-panelW)/2
		y := int(workArea.Bottom) - panelH
		win.SetWindowPos(dlg.Handle(), 0, int32(x), int32(y), panelW, panelH, win.SWP_NOZORDER)
	}
	dlg.Run()
}
