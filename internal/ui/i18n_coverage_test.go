//go:build windows

package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestCatalogCoversAllKeys adalah jaminan bahwa bahasa apa pun yang dipilih
// tidak menyisakan teks campur-campur (permintaan di Update.txt): setiap
// T("...") yang ditulis di kode harus punya entri di katalog EN dan ZH.
//
// Kuncinya dibaca langsung dari AST berkas sumber, jadi tambah teks baru tanpa
// menerjemahkannya akan membuat `go test` gagal — bukan baru ketahuan saat
// pengguna mengganti bahasa.
func TestCatalogCoversAllKeys(t *testing.T) {
	keys := sourceTKeys(t)
	if len(keys) < 100 {
		t.Fatalf("hanya %d kunci T() ditemukan — parser mungkin gagal membaca sumber", len(keys))
	}
	for code, cat := range map[string]map[string]string{"en": catalogEN, "zh": catalogZH} {
		var missing []string
		for k := range keys {
			if v, ok := cat[k]; !ok || v == "" {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		for _, k := range missing {
			t.Errorf("bahasa %s kehilangan terjemahan untuk %q", code, k)
		}
		if n := len(missing); n > 0 {
			t.Errorf("bahasa %s: %d dari %d kunci belum diterjemahkan", code, n, len(keys))
		}
	}
}

// TestCatalogTidakPunyaKunciYatim mencegah katalog membengkak dengan entri
// yang sudah dihapus dari kode.
func TestCatalogTidakPunyaKunciYatim(t *testing.T) {
	keys := sourceTKeys(t)
	// T() juga dipanggil dengan nilai dari tabel (label status), jadi nilai
	// tabel itu ikut dihitung sebagai kunci yang dipakai.
	for _, s := range statusLabels {
		keys[s] = true
	}
	for _, s := range statusCaptions {
		keys[s] = true
	}
	for _, cat := range []map[string]string{catalogEN, catalogZH} {
		for k := range cat {
			if !keys[k] {
				t.Errorf("katalog berisi %q yang tidak lagi dipakai kode", k)
			}
		}
	}
}

// TestTJatuhKeKunciAsli memverifikasi bahasa dasar tetap memakai teks
// Indonesia dan bahasa asing benar-benar ikut katalog.
func TestTJatuhKeKunciAsli(t *testing.T) {
	const kunci = "Batal"
	old := Lang()
	defer SetLanguage(old)

	if !SetLanguage("id") || T(kunci) != kunci {
		t.Fatalf("bahasa id harus memakai teks asli, dapat %q", T(kunci))
	}
	if !SetLanguage("en") || T(kunci) == kunci {
		t.Fatalf("bahasa en harus menerjemahkan %q", kunci)
	}
	if SetLanguage("xx") {
		t.Error("kode bahasa tidak dikenal harus ditolak")
	}
}

// sourceTKeys mengumpulkan seluruh argumen literal T() di berkas sumber paket.
func sourceTKeys(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	keys := make(map[string]bool)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "catalog_en.go" || f == "catalog_zh.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		node, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parser %s: %v", f, err)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "T" {
				return true
			}
			if s, ok := stringLit(call.Args[0]); ok {
				keys[s] = true
			}
			return true
		})
	}
	return keys
}

// stringLit membaca nilai string dari literal, termasuk hasil penggabungan
// beberapa literal dengan operator +.
func stringLit(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		a, ok1 := stringLit(v.X)
		b, ok2 := stringLit(v.Y)
		if ok1 && ok2 {
			return a + b, true
		}
	}
	return "", false
}
