package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/config"
)

// moveLinks starts Lookout behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@.
func moveLinks(t *testing.T) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "watches:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"lookout","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/watches", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/watches", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, strings.NewReader("{}"), "application/json"); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, nil, "")
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 1 || p.Exports[0].Kind != core.KindPocket || len(p.Imports) != 2 {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's watches to Bob: the import adds once, and nothing of
// one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t)
	a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": "https://shop.example.com/kettle", "title": "Kettle"})

	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", nil, "")
	if code != 200 || !strings.Contains(string(export), "shop.example.com/kettle") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", nil, ""); strings.Contains(string(bob), "kettle") {
		t.Fatalf("bob's export holds alice's watches: %s", bob)
	}

	imp := func() map[string]any {
		code, out := a.raw("pcx_bob_import", "POST", "/api/v1/import", bytes.NewReader(export), "application/json")
		if code != 200 {
			t.Fatalf("import → %d %s", code, out)
		}
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m
	}
	if first := imp(); num(first, "added") != 1 {
		t.Fatalf("first import: %v", first)
	}
	if second := imp(); num(second, "added") != 0 || num(second, "skipped") != 1 {
		t.Fatalf("second import: %v", second)
	}
	if l := a.list(a.bob, "/api/v1/watches"); len(l) != 1 {
		t.Fatalf("bob's watches: %v", l)
	}
	if l := a.list(a.alice, "/api/v1/watches"); len(l) != 1 {
		t.Fatalf("alice's watches changed: %v", l)
	}
}
