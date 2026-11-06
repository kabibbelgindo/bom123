//go:build windows

package ui

// chatpage.go menggambar SELURUH ruang chat (pribadi maupun grup) sebagai satu
// halaman HTML di dalam walk.WebView, bertema BIRU khas jendela utama.
//
// Aturan tampilan (Update.txt):
//   - alamat web dijadikan tautan otomatis, HANYA http(s):// atau www.;
//     tulisan seperti "Jl.Sudirman", "PT.Maju", "Rp.500", "data.zip" atau
//     "script.sh" tidak boleh tertaut, dan domain belgindoraya.co.id
//     dikecualikan karena bukan halaman web;
//   - foto tampil langsung di dalam balon (bukan tautan) dan bisa diklik
//     untuk memilih aplikasi pembuka; dokumen tampil sebagai tautan;
//   - teks pesan dapat diseleksi dengan mouse; klik kanan membuka menu
//     "Salin" / "Bersihkan Obrolan" bikinan sendiri;
//   - seluruh teks halaman ikut bahasa yang dipilih (lewat T()).
//
// Komunikasi dua arah:
//   - JS -> Go  : navigasi "app://..." (ditangkap event Navigating lalu
//     dibatalkan) untuk sinyal pendek, dan
//     document.title = "\x01<seq>|<id>|<teks terenkode>" untuk teks pesan.
//   - Go -> JS  : navigasi "javascript:" berisi window.__dispatchAll([...])
//     dengan beberapa payload base64 sekaligus, karena navigasi tidak boleh
//     bertumpuk (lihat pageBridge).
//   Seluruh script berada di AKHIR <body> agar DOM sudah tersedia.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lxn/walk"
)

// domainBukanWeb tidak pernah dijadikan tautan walau tertulis seperti alamat.
const domainBukanWeb = "belgindoraya.co.id"

// ---------- model halaman ----------

// pageAttach lampiran satu pesan: foto tampil inline, dokumen jadi tautan.
type pageAttach struct {
	K  string `json:"k"`            // "img" | "doc"
	N  string `json:"n"`            // nama berkas
	S  string `json:"s"`            // ukuran, sudah diformat
	H  string `json:"h,omitempty"`  // src thumbnail (foto)
	O  string `json:"o,omitempty"`  // URL app://open untuk klik
	BR string `json:"br,omitempty"` // catatan kecil di bawah lampiran
}

// pageMsg adalah satu pesan versi JSON untuk halaman chat.
type pageMsg struct {
	D  string      `json:"d"` // "in" | "out" | "sys"
	W  string      `json:"w,omitempty"`
	T  string      `json:"t"`
	TS string      `json:"ts"` // jam "15:04"
	ID string      `json:"id,omitempty"`
	ST string      `json:"st,omitempty"`
	A  *pageAttach `json:"a,omitempty"`
}

func newPageMsg(m chatMessage) pageMsg {
	p := pageMsg{D: m.Dir, W: m.Who, T: m.Text,
		TS: time.Unix(m.TS, 0).Format("15:04"), ID: m.ID, ST: m.State}
	if m.Att != nil {
		p.A = renderAttach(m.Att)
	}
	return p
}

// renderAttach mengubah lampiran menjadi versi siap-tampil.
func renderAttach(a *chatAttach) *pageAttach {
	pa := &pageAttach{K: "doc", N: a.Name, S: humanSize(a.Size)}
	if p := existingPath(a.Path); p != "" {
		pa.O = openAppURL(p)
	}
	if a.IsPhoto {
		if thumb := thumbnailFor(a.Path); thumb != "" {
			pa.K = "img"
			pa.H = thumb
		} else {
			pa.BR = T("Foto belum siap ditampilkan; klik untuk membuka berkasnya")
		}
	} else if riskyName(a.Name) {
		pa.BR = T("Berkas ini bisa berbahaya. Pastikan Anda mengenal pengirimnya.")
	}
	return pa
}

// pageCmd perintah Go -> halaman chat.
type pageCmd struct {
	C   string    `json:"c"` // add|ticks|status|typing|peer|insert|focus|clear|cancelbtn
	M   []pageMsg `json:"m,omitempty"`
	IDs []string  `json:"ids,omitempty"`
	ST  string    `json:"st,omitempty"`
	T   string    `json:"t,omitempty"`
	On  bool      `json:"on,omitempty"`
	N   string    `json:"n,omitempty"`
	S   string    `json:"s,omitempty"`
}

// pageBridge menghubungkan satu jendela chat dengan halaman HTML-nya.
//
// Satu-satunya saluran Go -> JS adalah navigasi "javascript:", dan navigasi
// bersifat asinkron: dua SetURL berurutan bisa saling menimpa sehingga balon
// pesan baru muncul terlambat (atau hilang sampai navigasi berikutnya).
// Karena itu perintah selalu masuk antrean dan hanya satu navigasi yang boleh
// berjalan pada satu waktu; batch berikutnya dilepas saat halaman melaporkan
// navigasi selesai (finish) atau saat watchdog habis.
type pageBridge struct {
	mu      sync.Mutex
	wv      *walk.WebView
	ready   bool
	queue   []string // payload base64 yang menunggu giliran
	busy    bool     // ada navigasi javascript: yang belum selesai
	pumpSeq uint64
}

const (
	// maxBatchItems membatasi jumlah perintah dalam satu navigasi.
	maxBatchItems = 24
	// maxBatchChars membatasi panjang URL javascript: (MSHTML punya batas URL).
	maxBatchChars = 12000
	// maxQueue menahan agar antrean tidak tumbuh tanpa batas.
	maxQueue = 400
	// pumpWatchdog adalah cadangan bila event Navigated tidak tiba.
	pumpWatchdog = 200 * time.Millisecond
)

func (b *pageBridge) attach(wv *walk.WebView) {
	b.mu.Lock()
	b.wv = wv
	b.mu.Unlock()
}

// reset menyiapkan bridge untuk halaman baru (setelah jendela dibuka kembali).
func (b *pageBridge) reset() {
	b.mu.Lock()
	b.ready = false
	b.busy = false
	b.queue = nil
	b.mu.Unlock()
}

// payloadOf mengemas satu perintah menjadi literal string JS siap-tempel.
func payloadOf(cmd pageCmd) (string, bool) {
	raw, err := json.Marshal(cmd)
	if err != nil {
		return "", false
	}
	return "'" + base64.StdEncoding.EncodeToString(raw) + "'", true
}

// pushCmd menambahkan satu perintah ke antrean lalu mencoba melepasnya.
func (b *pageBridge) pushCmd(cmd pageCmd) {
	item, ok := payloadOf(cmd)
	if !ok {
		return
	}
	b.mu.Lock()
	if len(b.queue) < maxQueue {
		b.queue = append(b.queue, item)
	}
	if !b.ready || b.busy {
		b.mu.Unlock()
		return
	}
	b.pumpLocked()
}

// pumpLocked melepas satu batch dari antrean sebagai satu navigasi. Selalu
// membuka kunci sebelum SetURL karena event Navigated bisa datang seketika.
func (b *pageBridge) pumpLocked() {
	if len(b.queue) == 0 {
		b.busy = false
		b.mu.Unlock()
		return
	}
	take, chars := 0, 0
	for take < len(b.queue) && take < maxBatchItems && chars+len(b.queue[take]) < maxBatchChars {
		chars += len(b.queue[take])
		take++
	}
	items := append([]string(nil), b.queue[:take]...)
	b.queue = b.queue[take:]
	b.busy = true
	b.pumpSeq++
	seq := b.pumpSeq
	wv := b.wv
	b.mu.Unlock()
	if wv == nil {
		b.finish()
		return
	}
	_ = wv.SetURL("javascript:void(window.__dispatchAll&&window.__dispatchAll([" + strings.Join(items, ",") + "]))")
	time.AfterFunc(pumpWatchdog, func() { b.watchdog(seq) })
}

// watchdog memaksa batch berikutnya bila navigasi tidak dilaporkan selesai.
func (b *pageBridge) watchdog(seq uint64) {
	b.mu.Lock()
	if !b.busy || b.pumpSeq != seq {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	b.finish()
}

// finish menandai navigasi terakhir selesai dan melepas batch berikutnya.
func (b *pageBridge) finish() {
	b.mu.Lock()
	b.busy = false
	if !b.ready || len(b.queue) == 0 {
		b.mu.Unlock()
		return
	}
	b.pumpLocked()
}

// markReady menandai halaman siap lalu menuang semua perintah yang tertahan.
func (b *pageBridge) markReady() {
	b.mu.Lock()
	wasReady := b.ready
	b.ready = true
	b.busy = false
	if !wasReady {
		if item, ok := payloadOf(pageCmd{C: "focus"}); ok && len(b.queue) < maxQueue {
			b.queue = append(b.queue, item)
		}
	}
	if len(b.queue) == 0 {
		b.mu.Unlock()
		return
	}
	b.pumpLocked()
}

// enqueue menambahkan satu perintah tanpa langsung memompa; dipakai ketika
// beberapa perintah harus berada di halaman dalam navigasi yang sama (mis.
// balon baru lalu centang terkirim), sehingga pengguna tidak sempat melihat
// balon tanpa status.
func (b *pageBridge) enqueue(cmd pageCmd) {
	item, ok := payloadOf(cmd)
	if !ok {
		return
	}
	b.mu.Lock()
	if len(b.queue) < maxQueue {
		b.queue = append(b.queue, item)
	}
	b.mu.Unlock()
}

// pump melepas antrean yang menunggu bila sedang tidak ada navigasi berjalan.
func (b *pageBridge) pump() {
	b.mu.Lock()
	if !b.ready || b.busy || len(b.queue) == 0 {
		b.mu.Unlock()
		return
	}
	b.pumpLocked()
}

// dropCmd adalah perintah menghapus balon yang sudah terlanjur tampil.
func dropCmd(id string) pageCmd { return pageCmd{C: "drop", IDs: []string{id}} }

// ---------- tautan ----------

// urlRe menangkap alamat http(s):// atau www. saja. Tanpa pola ini, tulisan
// seperti "Jl.Sudirman" atau "data.zip" ikut dianggap alamat.
var urlRe = regexp.MustCompile(`(?i)(https?://[a-z0-9\-._~]+(?::\d+)?[a-z0-9\-._~/?%=&+#!*,:;@()'$]*|www\.[a-z0-9\-._~]+(?::\d+)?[a-z0-9\-._~/?%=&+#!*,:;@()'$]*)`)

// trimTrailing membuang tanda baca yang menempel di ujung tautan.
func trimTrailing(s string) string {
	for len(s) > 0 {
		switch s[len(s)-1] {
		case '.', ',', ';', ':', '!', '?', ')', ']', '}', '\'', '"':
			s = s[:len(s)-1]
		default:
			return s
		}
	}
	return s
}

// linkable true bila alamat boleh dijadikan tautan.
func linkable(raw string) bool {
	u := strings.ToLower(raw)
	if strings.Contains(u, domainBukanWeb) {
		return false
	}
	return strings.HasPrefix(u, "http://") ||
		strings.HasPrefix(u, "https://") ||
		strings.HasPrefix(u, "www.")
}

// hrefFor mengubah teks alamat menjadi URL tujuan tautan.
func hrefFor(raw string) string {
	if strings.HasPrefix(strings.ToLower(raw), "http") {
		return raw
	}
	return "http://" + raw
}

// linkifyHTML menerima teks yang SUDAH di-escape HTML dan menyisipkan tautan.
func linkifyHTML(escaped string) string {
	return urlRe.ReplaceAllStringFunc(escaped, func(m string) string {
		cut := trimTrailing(m)
		if cut == "" || !linkable(cut) {
			return m
		}
		href := html.EscapeString(hrefFor(cut))
		return `<a class="lnk" href="` + href + `" target="_blank">` + cut + `</a>` + strings.TrimPrefix(m, cut)
	})
}

func escapeBr(s string) string {
	e := html.EscapeString(s)
	e = strings.ReplaceAll(e, "\r\n", "\n")
	return strings.ReplaceAll(e, "\n", "<br/>")
}

// ---------- penggambar balon ----------

// tickHTML menggambar penanda status pesan keluar: satu centang abu-abu =
// belum terkirim, dua centang abu-abu = terkirim, dua biru = sudah dibaca.
func tickHTML(state string) string {
	if state == "" {
		state = "sent"
	}
	switch state {
	case "pending":
		return `<span class="msg-tick" title="` + T("Belum terkirim") + `">&#10003;</span>`
	case "read":
		return `<span class="msg-tick tick-read" title="` + T("Sudah dibaca") + `">&#10003;&#10003;</span>`
	default:
		return `<span class="msg-tick" title="` + T("Terkirim, belum dibaca") + `">&#10003;&#10003;</span>`
	}
}

// attachHTMLGo merender lampiran versi Go; harus identik dengan attachHTML di JS.
func attachHTMLGo(a *pageAttach) string {
	if a == nil {
		return ""
	}
	name := html.EscapeString(a.N)
	size := html.EscapeString(a.S)
	note := ""
	if a.BR != "" {
		note = `<div class="msg-note">` + html.EscapeString(a.BR) + `</div>`
	}
	if a.K == "img" && a.H != "" {
		click := ""
		if a.O != "" {
			click = ` onclick="openPath('` + jsArg(a.O) + `')" title="` + html.EscapeString(T("Klik untuk membuka")) + `"`
		}
		return `<div class="msg-phwrap"><img class="msg-ph"` + click + ` src="` + html.EscapeString(a.H) +
			`" alt="` + name + `"/></div>` +
			`<div class="msg-file"><span class="msg-fname">` + name + `</span> <span class="msg-fsize">` + size + `</span></div>` + note
	}
	href := "#"
	if a.O != "" {
		href = html.EscapeString(a.O)
	}
	return `<div class="msg-file"><a class="msg-doc" href="` + href + `">&#128196; ` +
		name + ` <span class="msg-fsize">` + size + `</span></a></div>` + note
}

// bubbleJS adalah versi JavaScript penggambar balon; harus identik dengan
// pageBubbleGo.
const bubbleJS = `function jsArg(s){
 return String(s).replace(/\\/g,'\\\\').replace(/'/g,"\\'").replace(/"/g,'&quot;').replace(/\r/g,'\\r').replace(/\n/g,'\\n');
}
function canLink(u){
 u = String(u).toLowerCase();
 if (u.indexOf(LINK_BLOCK) >= 0) { return false; }
 return /^http:\/\//.test(u) || /^https:\/\//.test(u) || /^www\./.test(u);
}
function openPath(u){ try { window.location.href = u; } catch(e){} }
function linkify(s){
 return String(s).replace(urlRe, function(m){
   var cut = m.replace(/[.,;:!?)\]}'"]+$/, '');
   if (!cut || !canLink(cut)) { return m; }
   var href = /^http/i.test(cut) ? cut : ('http://' + cut);
   return '<a class="lnk" href="' + href.replace(/"/g, '&quot;') + '" target="_blank">' + cut + '</a>' + m.substring(cut.length);
 });
}
function attachHTML(a){
 if (!a) { return ''; }
 var name = esc(a.n), size = esc(a.s || '');
 var note = a.br ? '<div class="msg-note">' + esc(a.br) + '</div>' : '';
 if (a.k === 'img' && a.h) {
   var click = a.o ? ' onclick="openPath(\'' + jsArg(a.o) + '\')" title="' + esc(TXT_OPEN) + '"' : '';
   return '<div class="msg-phwrap"><img class="msg-ph"' + click + ' src="' + esc(a.h) + '" alt="' + name + '"/></div>' +
          '<div class="msg-file"><span class="msg-fname">' + name + '</span> <span class="msg-fsize">' + size + '</span></div>' + note;
 }
 var href = a.o ? esc(a.o) : '#';
 return '<div class="msg-file"><a class="msg-doc" href="' + href + '">&#128196; ' + name +
        ' <span class="msg-fsize">' + size + '</span></a></div>' + note;
}
function bubbleHTML(m){
 var row='msg-row msg-'+(m.d==='sys'?'sys':m.d);
 if(m.d==='sys'){
   return '<div class="'+row+'"><div class="msg-sys-bubble">'+esc(m.t).replace(/\r?\n/g,'<br/>')+'</div></div>';
 }
 var h='<div class="'+row+'"'+(m.d==='out'&&m.id?' data-id="'+m.id+'"':'')+'><div class="msg-bubble">';
 var who=(m.d==='in'&&SHOW_SENDER&&m.w)?('<span class="msg-who">'+esc(m.w)+' : </span>'):'';
 if(m.a){ h+=who; h+=attachHTML(m.a); }
 else { h+='<div class="msg-text">'+who+linkify(esc(m.t).replace(/\r\n/g,'\n').replace(/\n/g,'<br/>'))+'</div>'; }
 h+='<div class="msg-time">'+m.ts;
 if(m.d==='out'&&m.id){h+=tickSpan(m.st);}
 h+='</div></div></div>';
 return h;
}
function tickSpan(st){
 if(st==='pending'){return '<span class="msg-tick" title="'+TXT_PEND+'">&#10003;</span>';}
 if(st==='read'){return '<span class="msg-tick tick-read" title="'+TXT_READ+'">&#10003;&#10003;</span>';}
 return '<span class="msg-tick" title="'+TXT_SENT+'">&#10003;&#10003;</span>';
}`

// pageBubbleGo merender satu pesan versi Go (halaman awal).
func pageBubbleGo(m chatMessage) string {
	return pageBubblePage(newPageMsg(m))
}

func pageBubblePage(m pageMsg) string {
	switch m.D {
	case "sys":
		return fmt.Sprintf(`<div class="msg-row msg-sys"><div class="msg-sys-bubble">%s</div></div>`+"\n",
			escapeBr(m.T))
	case "out":
		idAttr, tick := "", ""
		if m.ID != "" {
			idAttr = fmt.Sprintf(` data-id="%s"`, html.EscapeString(m.ID))
			tick = tickHTML(m.ST)
		}
		return fmt.Sprintf(`<div class="msg-row msg-out"%s><div class="msg-bubble">%s`+
			`<div class="msg-time">%s%s</div></div></div>`+"\n",
			idAttr, bodyHTML(m, ""), m.TS, tick)
	default: // "in"
		who := ""
		if m.W != "" && showSender {
			who = fmt.Sprintf(`<span class="msg-who">%s : </span>`, html.EscapeString(m.W))
		}
		return fmt.Sprintf(`<div class="msg-row msg-in"><div class="msg-bubble">%s`+
			`<div class="msg-time">%s</div></div></div>`+"\n",
			bodyHTML(m, who), m.TS)
	}
}

// bodyHTML menyatukan balon teks atau lampiran; who adalah nama pengirim
// yang dicetak tebal INLINE di depan pesan ("User 1 : selamat pagi").
func bodyHTML(m pageMsg, who string) string {
	if m.A != nil {
		return who + attachHTMLGo(m.A)
	}
	return fmt.Sprintf(`<div class="msg-text">%s%s</div>`, who, linkifyHTML(escapeBr(m.T)))
}

// showSender diisi lewat setShowSender sebelum membangun halaman.
var showSender bool

func setShowSender(v bool) { showSender = v }

// ---------- halaman lengkap ----------

type chatPageOpts struct {
	Name        string // nama rekan / grup di header
	Sub         string // teks kecil di bawah nama
	Group       bool   // grup: tampilkan nama pengirim pada balon masuk
	Typing      bool   // aktifkan indikator mengetik (chat pribadi)
	Placeholder string // panduan di kolom balas
	Messages    []chatMessage
}

func buildChatPageHTML(o chatPageOpts) string {
	setShowSender(o.Group)

	var initial strings.Builder
	for _, m := range o.Messages {
		initial.WriteString(pageBubbleGo(m))
	}

	avatarHue := 0
	avatarInitial := "B"
	for _, r := range o.Name {
		avatarHue = (avatarHue*31 + int(r)) % 360
		avatarInitial = strings.ToUpper(string(r))
		break
	}

	typingDots := `''`
	if o.Typing {
		typingDots = `'<span class="tdot">&#8226;</span><span class="tdot">&#8226;</span><span class="tdot">&#8226;</span>'`
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html>
<head>
<meta http-equiv="X-UA-Compatible" content="IE=edge" />
<meta charset="utf-8" />
<title>Belgindo Chat</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body { width: 100%; height: 100%; }
  body {
    font-family: "Segoe UI", "Segoe UI Emoji", sans-serif;
    font-size: 13px;
    background-color: #FFFFFF;
    overflow: hidden;
    display: -ms-flexbox; display: flex;
    -ms-flex-direction: column; flex-direction: column;
  }
  #hd {
    -ms-flex: 0 0 auto; flex: 0 0 auto;
    display: -ms-flexbox; display: flex;
    -ms-flex-align: center; align-items: center;
    background-color: #1F4E79;
    border-bottom: 1px solid #16405F;
    padding: 7px 12px;
  }
  #av {
    width: 38px; height: 38px; border-radius: 50%;
    background: TOKNAVATARCLR;
    color: #FFFFFF; font-size: 17px; font-weight: bold;
    text-align: center; line-height: 38px;
    margin-right: 10px;
    -ms-flex: 0 0 auto; flex: 0 0 auto;
  }
  #hdmain { min-width: 0; }
  #hdname { font-size: 14px; font-weight: bold; color: #FFFFFF; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  #hdsub  { font-size: 11.5px; color: #A8C8E3; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  #hdsub.typing { color: #CFE2F3; }
  .tdot { color: #CFE2F3; font-size: 11px; margin-right: 2px; }
  #msgs {
    -ms-flex: 1 1 auto; flex: 1 1 auto;
    overflow-y: auto; overflow-x: hidden;
    background-color: #EDF2F8;
    background-image: radial-gradient(#DCE5F0 1px, transparent 1px);
    background-size: 18px 18px;
    padding: 8px 10px;
  }
  .msg-row { display: block; width: 100%; margin-bottom: 5px; clear: both; }
  .msg-in  { text-align: left; }
  .msg-out { text-align: right; }
  .msg-bubble {
    display: inline-block;
    max-width: 82%;
    padding: 5px 9px 4px 9px;
    border-radius: 8px;
    box-shadow: 0 1px 1.5px rgba(0,0,0,0.13);
    word-wrap: break-word;
    text-align: left;
    line-height: 1.3;
  }
  .msg-in .msg-bubble  { background-color: #FFFFFF; border-top-left-radius: 2px; color: #111B21; }
  .msg-out .msg-bubble { background-color: #CFE2F3; border-top-right-radius: 2px; color: #111B21; }
  .msg-sys { text-align: center; margin: 8px 0; }
  .msg-sys-bubble {
    display: inline-block;
    background-color: rgba(255,255,255,0.9);
    padding: 4px 12px;
    border-radius: 6px;
    font-size: 11px;
    color: #54656F;
    box-shadow: 0 1px 1px rgba(0,0,0,0.08);
  }
  .msg-who { font-size: 12.5px; font-weight: bold; color: #1F4E79; }
  .msg-time { font-size: 10px; color: #667781; margin-left: 8px; float: right; margin-top: 3px; }
  .msg-tick { font-family: "Segoe UI Symbol", sans-serif; font-size: 10px; color: #667781; letter-spacing: -3px; margin-left: 3px; }
  .tick-read { color: #0078D4; }
  .msg-text {
    font-family: "Segoe UI", "Segoe UI Emoji", sans-serif;
    font-size: 13.5px;
    white-space: pre-wrap;
    word-break: break-word;
    display: inline;
  }
  .lnk { color: #0B5FA5; text-decoration: underline; cursor: pointer; }
  .msg-phwrap { margin: 2px 0 4px 0; }
  .msg-ph {
    max-width: 240px; max-height: 240px; display: block;
    border-radius: 6px; cursor: pointer; border: 1px solid #C7D6E6;
  }
  .msg-file { margin: 2px 0 3px 0; font-size: 12px; }
  .msg-doc { color: #0B5FA5; text-decoration: none; }
  .msg-doc:hover { text-decoration: underline; }
  .msg-fname { font-weight: bold; color: #1F4E79; }
  .msg-fsize { color: #667781; }
  .msg-note { font-size: 10.5px; color: #7A5200; margin-top: 2px; }
  #statusbar {
    -ms-flex: 0 0 auto; flex: 0 0 auto;
    display: none;
    padding: 3px 12px;
    font-size: 11px;
    color: #54656F;
    background-color: #E9EEF6;
    border-top: 1px solid #D5DFEC;
  }
  #cancelTx {
    display: none;
    margin-left: 10px;
    padding: 1px 8px;
    border: 1px solid #C0392B;
    border-radius: 3px;
    color: #C0392B;
    cursor: pointer;
  }
  #composer {
    -ms-flex: 0 0 auto; flex: 0 0 auto;
    display: -ms-flexbox; display: flex;
    -ms-flex-align: center; align-items: center;
    background-color: #E9EEF6;
    padding: 6px 8px;
  }
  .cbtn {
    width: 36px; height: 36px;
    border-radius: 50%;
    font-family: "Segoe UI Emoji", "Segoe UI", sans-serif;
    font-size: 20px;
    text-align: center; line-height: 36px;
    cursor: pointer;
    margin-right: 4px;
    -ms-flex: 0 0 auto; flex: 0 0 auto;
  }
  .cbtn:hover { background-color: #D9E3F0; }
  #inpw {
    -ms-flex: 1 1 auto; flex: 1 1 auto;
    background-color: #FFFFFF;
    border-radius: 8px;
    padding: 8px 12px;
    max-height: 96px;
    overflow-y: auto;
    margin-right: 4px;
    position: relative;
  }
  #inp {
    outline: none;
    border: 0;
    font-family: "Segoe UI", "Segoe UI Emoji", sans-serif;
    font-size: 13.5px;
    line-height: 1.35;
    color: #111B21;
    min-height: 18px;
    word-break: break-word;
    direction: ltr;
    text-align: left;
  }
  /* Placeholder dipasang absolute: MSHTML menghitung teks :before sebagai
     isi baris, sehingga kursor kosong sempat terlihat di ujung kanan. */
  #inp:empty:before {
    content: attr(data-ph);
    color: #8696A0;
    position: absolute;
    left: 12px;
    top: 8px;
    right: 12px;
  }
  #btnSend {
    width: 38px; height: 38px;
    border-radius: 50%;
    background-color: #1F4E79;
    color: #FFFFFF;
    font-family: "Segoe UI Symbol", "Segoe UI", sans-serif;
    font-size: 15px;
    text-align: center; line-height: 38px;
    cursor: pointer;
    -ms-flex: 0 0 auto; flex: 0 0 auto;
  }
  #btnSend:hover { background-color: #2A5A86; }
  #ctx {
    position: absolute; z-index: 50; display: none;
    background-color: #FFFFFF;
    border: 1px solid #9FB4C9;
    box-shadow: 0 2px 6px rgba(0,0,0,0.25);
    font-size: 12px;
    min-width: 170px;
  }
  .ctxi { padding: 5px 12px; cursor: pointer; color: #111B21; }
  .ctxi:hover { background-color: #CFE2F3; }
  .ctxi.off { color: #A0AEBB; cursor: default; }
  .ctxi.off:hover { background-color: #FFFFFF; }
</style>
</head>
<body>
  <div id="hd">
    <div id="av">TOKNAVATARLTR</div>
    <div id="hdmain">
      <div id="hdname">TOKPEERNAME</div>
      <div id="hdsub">TOKPEERSUB</div>
    </div>
  </div>
  <div id="msgs">
TOKMESSAGES  </div>
  <div id="statusbar"><span id="statusTx"></span><span id="cancelTx">TOKCANCEL</span></div>
  <div id="composer">
    <div class="cbtn" id="btnEmoji" title="TOKEMOJINAME">&#128512;</div>
    <div id="inpw"><div id="inp" contenteditable="true" data-ph="TOKPLACEHOLDER"></div></div>
    TOKATTACH    <div id="btnSend" title="TOKSENDNAME">&#10148;</div>
  </div>
  <div id="ctx">
    <div class="ctxi" id="ctxCopy">TOKCOPY</div>
    <div class="ctxi" id="ctxClear">TOKCLEAR</div>
  </div>
<script>
var urlRe = TOKURLPATTERN;
var LINK_BLOCK = TOKBLOCKHOST;
var SHOW_SENDER = TOKSHOWSENDER;
var TYPING_ON = TOKTYPINGON;
var TYPING_DOTS = TOKTYPINGDOTS;
var TXT_PEND = TOKTEXTPEND;
var TXT_READ = TOKTEXTREAD;
var TXT_SENT = TOKTEXTSENT;
var TXT_TYPING = TOKTEXTTYPE;
var TXT_OPEN = TOKTEXTOPEN;
var __seq = 0;
var ZWSP = String.fromCharCode(0x200b);
var lastTyping = 0;
var savedRange = null;
var msgs = document.getElementById('msgs');
var inp = document.getElementById('inp');
var inpw = document.getElementById('inpw');
var hdsub = document.getElementById('hdsub');
var subDefault = hdsub.innerHTML;
var ctxBox = document.getElementById('ctx');

function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
` + bubbleJS + `
function nearBottom(){
  return msgs.scrollTop + msgs.clientHeight >= msgs.scrollHeight - 140;
}
function scrollBottom(){ msgs.scrollTop = msgs.scrollHeight; }
function addMsgs(list){
  if (!list || !list.length) { return; }
  var stick = nearBottom() || list[0].d === 'out';
  for (var i = 0; i < list.length; i++) {
    var div = document.createElement('div');
    div.innerHTML = bubbleHTML(list[i]);
    while (div.firstChild) { msgs.appendChild(div.firstChild); }
  }
  if (stick) { scrollBottom(); }
}
function clearMsgs(){ msgs.innerHTML = ''; }
function dropMsgs(cmd){
  if (!cmd.ids) { return; }
  for (var i = 0; i < cmd.ids.length; i++) {
    var rows = document.querySelectorAll('[data-id="' + cmd.ids[i] + '"]');
    for (var j = 0; j < rows.length; j++) {
      if (rows[j].parentNode) { rows[j].parentNode.removeChild(rows[j]); }
    }
  }
}
function pad2(n){ return (n < 10 ? '0' : '') + n; }
function clockNow(){ var d = new Date(); return pad2(d.getHours()) + ':' + pad2(d.getMinutes()); }
// newMsgId memakai charset hex agar bisa dipakai sebagai atribut data-id.
function newMsgId(){
  var c = '0123456789abcdef', s = '';
  for (var i = 0; i < 16; i++) { s += c.charAt(Math.floor(Math.random() * 16)); }
  return s;
}
function setTicks(cmd){
  for (var i = 0; i < cmd.ids.length; i++) {
    var rows = document.querySelectorAll('[data-id="' + cmd.ids[i] + '"]');
    for (var j = 0; j < rows.length; j++) {
      var tick = rows[j].querySelector('.msg-tick');
      if (!tick) { continue; }
      var span = document.createElement('span');
      span.innerHTML = tickSpan(cmd.st);
      tick.parentNode.replaceChild(span.firstChild, tick);
    }
  }
}
function setStatus(t){
  var bar = document.getElementById('statusbar');
  var tx = document.getElementById('statusTx');
  if (!t) { bar.style.display = 'none'; tx.innerHTML = ''; return; }
  tx.innerHTML = esc(t);
  bar.style.display = 'block';
}
function showCancel(on){
  document.getElementById('cancelTx').style.display = on ? 'inline' : 'none';
}
function setTyping(on){
  if (on) { hdsub.className = 'typing'; hdsub.innerHTML = TXT_TYPING + TYPING_DOTS; }
  else    { hdsub.className = '';        hdsub.innerHTML = subDefault; }
}
function setPeer(cmd){
  document.getElementById('hdname').innerHTML = esc(cmd.n);
  subDefault = esc(cmd.s);
  hdsub.className = '';
  hdsub.innerHTML = subDefault;
}
function saveSel(){
  try {
    var s = document.getSelection();
    if (s && s.rangeCount > 0) {
      var r = s.getRangeAt(0);
      if (r.commonAncestorContainer === inp || inp.contains(r.commonAncestorContainer)) {
        savedRange = r.cloneRange();
      }
    }
  } catch (e) {}
}
function insertText(t){
  inp.focus();
  try {
    if (savedRange) {
      var s = document.getSelection();
      s.removeAllRanges();
      s.addRange(savedRange);
    }
  } catch (e) {}
  try {
    document.execCommand('insertText', false, t);
  } catch (e) {
    inp.innerText += t;
  }
  cleanEmpty();
  saveSel();
}
function cleanEmpty(){
  var s = inp.innerHTML;
  if (s === '<br>' || s === '<br/>' || s === ZWSP ||
      s === '<br>' + ZWSP || s === '<br/>' + ZWSP) { inp.innerHTML = ''; }
}
// newLine menyisipkan satu baris baru di kolom balas (Ctrl+Enter). MSHTML
// tidak menampilkan kursor di baris baru yang benar-benar kosong, jadi
// ditempel satu karakter tak terlihat (zero-width space) sebagai penjaga;
// submit() membuangnya lagi sehingga tidak pernah masuk ke pesan.
function noDefault(e){
  if (e.preventDefault) { e.preventDefault(); } else { e.returnValue = false; }
  e.cancelBubble = true;
}
function newLine(e){
  noDefault(e);
  var before = inp.innerHTML;
  var ok = false;
  try { ok = document.execCommand('insertHTML', false, '<br/>' + ZWSP); } catch (er) {}
  if (!ok || inp.innerHTML === before) {
    inp.innerHTML = before + '<br/>' + ZWSP;
    try { inp.focus(); } catch (er2) {}
  }
  cleanEmpty();
  saveSel();
  return false;
}
function noteTyping(){
  if (!TYPING_ON) { return; }
  var n = +new Date();
  if (n - lastTyping > 1500) {
    lastTyping = n;
    try { window.location.href = 'app://typing'; } catch (e) {}
  }
}
function submit(){
  var t = (inp.innerText || '').split(ZWSP).join('');
  t = t.replace(/^\s+|\s+$/g, '');
  if (!t) { return; }
  // Balon sendiri digambar SEKETIKA di halaman, tanpa menunggu perjalanan
  // pulang-pergi ke Go; ID dibawa serta supaya status centang nanti menempel
  // di balon yang sama dan Go tidak menambah balon kedua.
  var id = newMsgId();
  addMsgs([{d: 'out', w: '', t: t, ts: clockNow(), id: id, st: 'pending'}]);
  inp.innerHTML = '';
  savedRange = null;
  lastTyping = 0;
  __seq++;
  try { document.title = '\u0001' + __seq + '|' + id + '|' + encodeURIComponent(t); } catch (e) {}
}
function selectionText(){
  var t = '';
  try { t = String(window.getSelection()); } catch (e) {}
  if (!t && document.selection) { try { t = document.selection.createRange().text; } catch (e2) {} }
  return t;
}
function copySelection(){
  var t = selectionText();
  if (!t) { return; }
  var ok = false;
  try { if (window.clipboardData) { ok = window.clipboardData.setData('Text', t); } } catch (e) {}
  if (!ok) { try { ok = document.execCommand('copy'); } catch (e2) {} }
  if (!ok && t.length < 1200) {
    try { window.location.href = 'app://copy?p=' + encodeURIComponent(t); } catch (e3) {}
  }
}
function hideCtx(){ ctxBox.style.display = 'none'; }
document.body.onclick = function(){ hideCtx(); };
document.body.onmousedown = function(e){
  e = e || window.event;
  if (e.button === 2) { return false; }
  hideCtx();
};
document.body.oncontextmenu = function(e){
  e = e || window.event;
  var item = document.getElementById('ctxCopy');
  item.className = selectionText() ? 'ctxi' : 'ctxi off';
  var x = e.clientX, y = e.clientY;
  ctxBox.style.display = 'block';
  var maxX = document.body.clientWidth - ctxBox.offsetWidth - 2;
  var maxY = document.body.clientHeight - ctxBox.offsetHeight - 2;
  if (x > maxX) { x = maxX; }
  if (y > maxY) { y = maxY; }
  if (x < 0) { x = 0; }
  if (y < 0) { y = 0; }
  ctxBox.style.left = x + 'px';
  ctxBox.style.top = y + 'px';
  return false;
};
document.getElementById('ctxCopy').onclick = function(){
  if (this.className.indexOf('off') >= 0) { return; }
  copySelection(); hideCtx();
};
document.getElementById('ctxClear').onclick = function(){
  hideCtx();
  try { window.location.href = 'app://clearchat'; } catch (e) {}
};
document.getElementById('cancelTx').onclick = function(){
  try { window.location.href = 'app://cancel'; } catch (e) {}
};
function __dispatch(b64){
  try {
    var bin = window.atob(b64);
    var arr = [];
    for (var i = 0; i < bin.length; i++) { arr.push(bin.charCodeAt(i)); }
    var s = '';
    for (var k = 0; k < arr.length; k += 4096) {
      s += String.fromCharCode.apply(null, arr.slice(k, k + 4096));
    }
    var cmd = JSON.parse(decodeURIComponent(escape(s)));
    switch (cmd.c) {
      case 'add':       addMsgs(cmd.m); break;
      case 'drop':      dropMsgs(cmd); break;
      case 'ticks':     setTicks(cmd); break;
      case 'status':    setStatus(cmd.t); break;
      case 'typing':    setTyping(cmd.on); break;
      case 'peer':      setPeer(cmd); break;
      case 'insert':    insertText(cmd.t); break;
      case 'focus':     inp.focus(); break;
      case 'clear':     clearMsgs(); break;
      case 'cancelbtn': showCancel(!!cmd.on); break;
    }
  } catch (e) {}
}
// __dispatchAll menerima beberapa perintah dalam satu navigasi.
function __dispatchAll(list){
  if (!list) { return; }
  for (var i = 0; i < list.length; i++) {
    try { __dispatch(list[i]); } catch (e) {}
  }
}
// Enter di ruang chat bisa tiba sebagai keydown atau hanya sebagai keypress,
// tergantung cara MSHTML meneruskan tombol ke halaman. Keduanya diarahkan ke
// satu penangan; penjaga waktu membuat satu ketukan hanya dijalankan sekali.
var __lastEnter = 0;
function enterKey(e){
  e = e || window.event;
  if ((e.keyCode || e.which) !== 13) { return; }
  var now = +new Date();
  if (now - __lastEnter < 120) { return false; }
  __lastEnter = now;
  // Enter = kirim; Ctrl+Enter (atau Shift+Enter) = baris baru.
  if (e.ctrlKey || e.shiftKey) { return newLine(e); }
  noDefault(e);
  submit();
  return false;
}
inp.onkeydown = function(e){
  var r = enterKey(e);
  if (r !== undefined) { return r; }
  noteTyping();
};
inp.onkeypress = enterKey;
inp.onkeyup = function(){ cleanEmpty(); saveSel(); };
inp.onmouseup = saveSel;
inp.onfocus = saveSel;
inp.oninput = function(){ cleanEmpty(); noteTyping(); };
inp.onpaste = function(e){
  e = e || window.event;
  if (e.preventDefault) { e.preventDefault(); } else { e.returnValue = false; }
  var cd = e.clipboardData || window.clipboardData;
  if (cd) {
    var txt = cd.getData('Text');
    if (txt) { insertText(txt); noteTyping(); }
  }
  return false;
};
inpw.onclick = function(){ inp.focus(); };
document.getElementById('btnEmoji').onclick = function(){
  try { window.location.href = 'app://emoji'; } catch (e) {}
};
var ba = document.getElementById('btnAttach');
if (ba) { ba.onclick = function(){ try { window.location.href = 'app://attach'; } catch (e) {} }; }
document.getElementById('btnSend').onclick = submit;
window.onload = function(){
  scrollBottom();
  inp.focus();
  var fire = function(){ try { window.location.href = 'app://ready'; } catch (e) {} };
  setTimeout(fire, 30);
  setTimeout(fire, 200);
};
</script>
</body>
</html>`)

	out := sb.String()
	jsStr := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			return `""`
		}
		return string(b)
	}
	pattern := strings.ReplaceAll(strings.TrimPrefix(urlRe.String(), "(?i)"), "/", `\/`)
	attachBtn := `<div class="cbtn" id="btnAttach" title="` +
		html.EscapeString(T("Kirim file lampiran")) + `">&#128206;</div>`

	// CATATAN: semua token harus UNIK dan tidak boleh menjadi substring token
	// lain; uji regressi ada di chatpage_token_test.go.
	out = strings.ReplaceAll(out, "TOKURLPATTERN", "/"+pattern+"/gi")
	out = strings.ReplaceAll(out, "TOKBLOCKHOST", jsStr("."+domainBukanWeb))
	out = strings.ReplaceAll(out, "TOKSHOWSENDER", boolJS(o.Group))
	out = strings.ReplaceAll(out, "TOKTYPINGON", boolJS(o.Typing))
	out = strings.ReplaceAll(out, "TOKTYPINGDOTS", typingDots)
	out = strings.ReplaceAll(out, "TOKTEXTPEND", jsStr(T("Belum terkirim")))
	out = strings.ReplaceAll(out, "TOKTEXTREAD", jsStr(T("Sudah dibaca")))
	out = strings.ReplaceAll(out, "TOKTEXTSENT", jsStr(T("Terkirim, belum dibaca")))
	out = strings.ReplaceAll(out, "TOKTEXTTYPE", jsStr(T("sedang mengetik ")))
	out = strings.ReplaceAll(out, "TOKTEXTOPEN", jsStr(T("Klik untuk membuka")))
	out = strings.ReplaceAll(out, "TOKNAVATARCLR", fmt.Sprintf("hsl(%d, 62%%, 45%%)", avatarHue))
	out = strings.ReplaceAll(out, "TOKNAVATARLTR", html.EscapeString(avatarInitial))
	out = strings.ReplaceAll(out, "TOKPEERNAME", html.EscapeString(o.Name))
	out = strings.ReplaceAll(out, "TOKPEERSUB", html.EscapeString(o.Sub))
	out = strings.ReplaceAll(out, "TOKPLACEHOLDER", html.EscapeString(o.Placeholder))
	out = strings.ReplaceAll(out, "TOKATTACH", attachBtn+"    ")
	out = strings.ReplaceAll(out, "TOKMESSAGES", initial.String())
	out = strings.ReplaceAll(out, "TOKCOPY", html.EscapeString(T("Salin teks yang dipilih")))
	out = strings.ReplaceAll(out, "TOKCLEAR", html.EscapeString(T("Bersihkan obrolan")))
	out = strings.ReplaceAll(out, "TOKCANCEL", html.EscapeString(T("Batal")))
	out = strings.ReplaceAll(out, "TOKEMOJINAME", html.EscapeString(T("Sisipkan emoji")))
	out = strings.ReplaceAll(out, "TOKSENDNAME", html.EscapeString(T("Kirim (Enter)")))
	return out
}

func boolJS(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// jsArg mengamankan nilai untuk disisipkan ke dalam string JS berkutip
// satu, identik dengan function jsArg() di bubbleJS.
func jsArg(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `"`, `&quot;`, "\r", `\r`, "\n", `\n`)
	return r.Replace(s)
}

// buildChatHTML merender halaman chat (nama dipertahankan agar pengujian lama
// tetap sah).
func buildChatHTML(title string, messages []chatMessage) string {
	return buildChatPageHTML(chatPageOpts{
		Name:        title,
		Sub:         "Belgindo Office Messenger",
		Placeholder: T("Tulis pesan, Enter kirim, Ctrl+Enter baris baru"),
		Messages:    messages,
	})
}

// parseSendTitle membaca sinyal kirim dari judul dokumen:
// "\x01<seq>|<id>|<teks percent-encoded>". id dipakai kembali oleh Go agar
// balon yang sudah digambar halaman adalah balon yang sama.
func parseSendTitle(title string) (id, text string, ok bool) {
	if !strings.HasPrefix(title, "\x01") {
		return "", "", false
	}
	rest := title[1:]
	i := strings.Index(rest, "|")
	if i < 0 {
		return "", "", false
	}
	rest = rest[i+1:]
	id = ""
	if j := strings.Index(rest, "|"); j >= 0 {
		id = cleanMsgID(rest[:j])
		rest = rest[j+1:]
	}
	msg, err := url.QueryUnescape(rest)
	if err != nil {
		return "", "", false
	}
	return id, msg, true
}

// cleanMsgID menerima ID dari halaman; hanya hex pendek yang dipercaya.
func cleanMsgID(s string) string {
	if s == "" || len(s) > 32 {
		return ""
	}
	for _, r := range strings.ToLower(s) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return strings.ToLower(s)
}

// appURL menormalkan URL skema app:// seperti yang dilaporkan MSHTML. WebView
// IE memandang bagian setelah "app://" sebagai nama host sehingga selalu
// menambahkan garis miring penutup: "app://ready" tiba sebagai "app://ready/"
// dan "app://copy?p=1" sebagai "app://copy/?p=1". Tanpa normalisasi ini tidak
// ada satu pun sinyal halaman yang dikenali Go — jembatan dua arah mati.
func appURL(navURL string) string {
	if !strings.HasPrefix(navURL, "app://") {
		return navURL
	}
	rest := strings.TrimPrefix(navURL, "app://")
	path, query := rest, ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		path, query = rest[:i], rest[i+1:]
	}
	path = strings.TrimRight(path, "/")
	if query == "" {
		return "app://" + path
	}
	return "app://" + path + "?" + query
}

// parseAppQuery membaca satu parameter dari URL "app://...?k=v".
func parseAppQuery(navURL, key string) string {
	i := strings.Index(navURL, "?")
	if i < 0 {
		return ""
	}
	q, err := url.ParseQuery(navURL[i+1:])
	if err != nil {
		return ""
	}
	return q.Get(key)
}
