package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lalamefine/lattis/internal/mcp"
)

// mcpClient parle au serveur MCP par HTTP, comme un assistant externe.
type mcpClient struct {
	t     *testing.T
	url   string
	token string
	id    int
}

func newMCP(t *testing.T, h *host) *mcpClient {
	t.Helper()
	srv := httptest.NewServer(mcp.Handler(&mcp.Server{Host: h, Version: "test"}, "jeton"))
	t.Cleanup(srv.Close)
	return &mcpClient{t: t, url: srv.URL, token: "jeton"}
}

func (c *mcpClient) post(body string, token string) (int, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (c *mcpClient) rpc(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	p, _ := json.Marshal(params)
	code, b := c.post(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, c.id, method, p), c.token)
	if code != http.StatusOK {
		c.t.Fatalf("%s : HTTP %d %s", method, code, b)
	}
	var r map[string]any
	if err := json.Unmarshal(b, &r); err != nil {
		c.t.Fatalf("%s : %v %s", method, err, b)
	}
	return r
}

// tool appelle un outil ; renvoie son texte et s'il est en erreur.
func (c *mcpClient) tool(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	r := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	if r["error"] != nil {
		c.t.Fatalf("%s : %v", name, r["error"])
	}
	res := r["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	return text, res["isError"] == true
}

func (c *mcpClient) ok(name string, args map[string]any) string {
	c.t.Helper()
	text, isErr := c.tool(name, args)
	if isErr {
		c.t.Fatalf("%s %v : %s", name, args, text)
	}
	return text
}

// Un assistant construit un classeur par MCP, l'enregistre, le rouvre et
// annule : tout passe par le protocole, avec un vrai classeur derrière.
func TestMCPSession(t *testing.T) {
	h := newHost(t)
	c := newMCP(t, h)

	init := c.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init["result"] == nil {
		t.Fatalf("initialize : %v", init)
	}
	if code, _ := c.post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, c.token); code != http.StatusAccepted {
		t.Fatalf("notification : HTTP %d", code)
	}
	list := c.rpc("tools/list", map[string]any{})
	tools := list["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, x := range tools {
		names[x.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"cells_paste", "db_query", "doc_save", "doc_undo", "tables_rename"} {
		if !names[want] {
			t.Fatalf("outil %s absent (%d outils)", want, len(names))
		}
	}

	c.ok("cells_paste", map[string]any{"row": 1, "col": 1, "values": [][]any{{"Client", "Montant"}, {"Durand", 120}, {"Martin", 80.5}}})
	tbl := h.sheet("").Tables[0].Name
	c.ok("tables_rename", map[string]any{"table": tbl, "name": "Commandes"})
	c.ok("cells_set", map[string]any{"row": 5, "col": 1, "value": "=SOMME(Commandes[Montant])"})
	c.ok("db_execute", map[string]any{"sql": `INSERT INTO Commandes (Client, Montant) VALUES (?, ?)`, "params": []any{"Petit", 15}})
	if got := c.ok("db_query", map[string]any{"sql": "SELECT sum(Montant) AS s FROM Commandes"}); !strings.Contains(got, "215.5") {
		t.Fatalf("db_query : %s", got)
	}
	// La somme a été décalée par la ligne ajoutée.
	if got := c.ok("cells_get", map[string]any{"row": 6, "col": 1}); !strings.Contains(got, "215,5") {
		t.Fatalf("cells_get : %s", got)
	}

	// Les erreurs sont des résultats d'outil en erreur, pas des pannes.
	if text, isErr := c.tool("tables_rename", map[string]any{"table": "Absent", "name": "X"}); !isErr || !strings.Contains(text, "Absent") {
		t.Fatalf("tableau absent : %v %s", isErr, text)
	}
	if text, isErr := c.tool("cells_set", map[string]any{"row": 1}); !isErr || !strings.Contains(text, "col") {
		t.Fatalf("paramètre manquant : %v %s", isErr, text)
	}
	if text, isErr := c.tool("db_execute", map[string]any{"sql": "DROP TABLE Commandes"}); !isErr {
		t.Fatalf("DROP accepté : %s", text)
	}
	if r := c.rpc("tools/call", map[string]any{"name": "outil_absent", "arguments": map[string]any{}}); r["error"] == nil {
		if res, _ := r["result"].(map[string]any); res == nil || res["isError"] != true {
			t.Fatalf("outil inconnu : %v", r)
		}
	}

	p := path(t, "mcp.lattis")
	c.ok("doc_save", map[string]any{"path": p})
	c.ok("doc_new", map[string]any{})
	if got := c.ok("sheets_get", map[string]any{}); strings.Contains(got, "Commandes") {
		t.Fatalf("doc_new garde le classeur : %s", got)
	}
	c.ok("doc_open", map[string]any{"path": p})
	if got := c.ok("doc_state", map[string]any{}); !strings.Contains(got, `"dirty": false`) || !strings.Contains(got, "mcp.lattis") {
		t.Fatalf("doc_state : %s", got)
	}
	c.ok("doc_undo", map[string]any{})
	if got := c.ok("db_query", map[string]any{"sql": "SELECT count(*) AS n FROM Commandes"}); !strings.Contains(got, "2") {
		t.Fatalf("après doc_undo : %s", got)
	}
	c.ok("doc_redo", map[string]any{})
	h.wantColumn("Commandes", "Client", "Durand", "Martin", "Petit")

	// Un script lancé par MCP est une seule étape d'annulation.
	c.ok("scripts_run", map[string]any{"source": `for i = 1, 3 do db.insert("Commandes", {Client = "S" .. i, Montant = i}) end`})
	h.wantColumn("Commandes", "Client", "Durand", "Martin", "Petit", "S1", "S2", "S3")
	c.ok("doc_undo", map[string]any{})
	h.wantColumn("Commandes", "Client", "Durand", "Martin", "Petit")
}

// Le point d'accès refuse les requêtes sans jeton ou d'une origine distante,
// et un message mal formé reçoit une erreur JSON-RPC.
func TestMCPRejectsBadRequests(t *testing.T) {
	h := newHost(t)
	c := newMCP(t, h)
	if code, _ := c.post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "mauvais"); code != http.StatusUnauthorized {
		t.Fatalf("mauvais jeton : HTTP %d", code)
	}
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer jeton")
	req.Header.Set("Origin", "https://exemple.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("origine distante : HTTP %d", res.StatusCode)
	}
	for _, body := range []string{`{`, `{"jsonrpc":"2.0","id":1,"method":"inconnue"}`} {
		code, b := c.post(body, c.token)
		if code != http.StatusOK || !strings.Contains(string(b), `"error"`) {
			t.Fatalf("%s : HTTP %d %s", body, code, b)
		}
	}
	// Un lot de requêtes reçoit un lot de réponses.
	code, b := c.post(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sheets_list","arguments":{}}}]`, c.token)
	var batch []map[string]any
	if code != http.StatusOK || json.Unmarshal(b, &batch) != nil || len(batch) != 2 {
		t.Fatalf("lot : HTTP %d %s", code, b)
	}
}
