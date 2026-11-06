//go:build windows

package ui

import (
	"strings"
	"testing"
)

// chatPageTokens adalah seluruh penanda TOK* yang diganti buildChatPageHTML.
//
// Aturan regressi (bug v1.1.0): token lama "TYPINGJS" adalah substring token
// "TXTTYPINGJS", sehingga penggantian TYPINGJS ikut merusak baris TXT_TYPING
// menjadi "var TXT_TYPING = TXTtrue;" — ReferenceError yang mematikan SELURUH
// script halaman chat (ruang chat tampak normal tetapi mati total: tidak bisa
// kirim, tidak bisa terima, app://ready tak pernah dikirim). Karena itu nama
// token harus unik dan tidak boleh menjadi substring token lain.
var chatPageTokens = []string{
	"TOKURLPATTERN", "TOKBLOCKHOST", "TOKSHOWSENDER", "TOKTYPINGON",
	"TOKTYPINGDOTS", "TOKTEXTPEND", "TOKTEXTREAD", "TOKTEXTSENT",
	"TOKTEXTTYPE", "TOKTEXTOPEN", "TOKNAVATARCLR", "TOKNAVATARLTR",
	"TOKPEERNAME", "TOKPEERSUB", "TOKPLACEHOLDER", "TOKATTACH",
	"TOKMESSAGES", "TOKCOPY", "TOKCLEAR", "TOKCANCEL", "TOKEMOJINAME",
	"TOKSENDNAME",
}

// TestChatPageTokensDisjoint memastikan tidak ada token yang menjadi substring
// token lain (penyebab bug v1.1.0).
func TestChatPageTokensDisjoint(t *testing.T) {
	for i, a := range chatPageTokens {
		for j, b := range chatPageTokens {
			if i != j && strings.Contains(b, a) {
				t.Fatalf("token %q adalah substring token %q — penggantian akan merusak halaman chat", a, b)
			}
		}
	}
}

// TestChatPageAllTokensReplaced membangun halaman chat pribadi maupun grup dan
// memastikan tidak ada penanda yang tersisa serta variabel kunci berisi nilai
// JavaScript yang sah (string/boolean), bukan identifier yang belum terdefinisi.
func TestChatPageAllTokensReplaced(t *testing.T) {
	attach := &chatAttach{Name: "catatan.txt", Size: 1234, Path: `C:\tmp\catatan.txt`}
	msgs := []chatMessage{{TS: 1, Dir: "in", Who: "Budi", Text: "halo", Att: attach}}
	optsList := []chatPageOpts{
		{Name: "Budi", Sub: "PC-01", Typing: true, Placeholder: "ph", Messages: msgs},
		{Name: "Grup Kantor", Sub: "2 anggota", Group: true, Typing: false, Placeholder: "ph2", Messages: msgs},
		{Name: "X"}, // tanpa pesan, tanpa mengetik
	}
	for k, o := range optsList {
		page := buildChatPageHTML(o)
		for _, tok := range chatPageTokens {
			if strings.Contains(page, tok) {
				t.Errorf("ops[%d]: token %s masih tersisa di halaman (gagal diganti)", k, tok)
			}
		}
		if _, ok := extractLine(page, "var TXT_TYPING = "); !ok {
			t.Errorf("ops[%d]: baris TXT_TYPING tidak ditemukan", k)
		} else if !strings.Contains(page, "var TXT_TYPING = \"") {
			t.Errorf("ops[%d]: TXT_TYPING harus string literal, dapat: %s", k, mustLine(page, "var TXT_TYPING"))
		}
		for _, need := range []string{"var TYPING_ON = ", "var SHOW_SENDER = "} {
			ln, ok := extractLine(page, need)
			if !ok {
				t.Errorf("ops[%d]: baris %s tidak ditemukan", k, need)
				continue
			}
			if !strings.HasSuffix(ln, "true;") && !strings.HasSuffix(ln, "false;") {
				t.Errorf("ops[%d]: %s harus boolean, dapat: %s", k, need, ln)
			}
		}
		// handler wajib ada: bila script mati karena galat, halaman mati total
		for _, need := range []string{"function __dispatch(", "app://ready"} {
			if !strings.Contains(page, need) {
				t.Errorf("ops[%d]: halaman kehilangan %q", k, need)
			}
		}
	}
}

func extractLine(page, needle string) (string, bool) {
	for _, ln := range strings.Split(page, "\n") {
		if strings.Contains(ln, needle) {
			return strings.TrimSpace(ln), true
		}
	}
	return "", false
}

func mustLine(page, needle string) string {
	if s, ok := extractLine(page, needle); ok {
		return s
	}
	return "(tidak ada)"
}
