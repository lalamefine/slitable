// Package integration rassemble des tests de bout en bout : chaque scénario
// pilote un vrai classeur (archive, base SQLite, bus de commandes, modèle des
// feuilles, formules) par la couche de commandes commune aux scripts Lua, au
// serveur MCP et à l'interface (internal/api), l'enregistre, le rouvre, et
// vérifie que tout ce qui est visible est resté cohérent.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lalamefine/lattis/internal/api"
	"github.com/lalamefine/lattis/internal/archive"
	"github.com/lalamefine/lattis/internal/doc"
	"github.com/lalamefine/lattis/internal/store"
)

// TestMain coupe le journal SQL en console (LATTIS_SQL=1 le rallume).
func TestMain(m *testing.M) {
	if os.Getenv("LATTIS_SQL") != "1" {
		store.SetSQLLog(false)
	}
	os.Exit(m.Run())
}

// host est une application sans interface : un classeur réel, ouvert,
// enregistré et rouvert comme dans l'application.
type host struct {
	t       *testing.T
	d       *doc.Document
	sel     api.Selection
	changed int
	answer  map[string]any
	prints  []string
	notices []string
}

func (h *host) Doc() (*doc.Document, error) { return h.d, nil }
func (h *host) Changed(context.Context)     { h.changed++ }
func (h *host) Selection() api.Selection    { return h.sel }
func (h *host) Notify(m string)             { h.notices = append(h.notices, m) }
func (h *host) Print(l string)              { h.prints = append(h.prints, l) }
func (h *host) ShowView(_ context.Context, v string) error {
	h.sel.View = v
	return nil
}
func (h *host) Select(_ context.Context, s api.Selection) error {
	h.sel = s
	return nil
}
func (h *host) Dialog(_ context.Context, d api.Dialog) (map[string]any, bool, error) {
	return h.answer, h.answer != nil, nil
}

func (h *host) NewDoc(ctx context.Context) error {
	d, err := doc.New(ctx)
	if err != nil {
		return err
	}
	h.swap(d)
	return nil
}

func (h *host) OpenDoc(ctx context.Context, path string) error {
	d, err := doc.Open(ctx, path)
	if err != nil {
		return err
	}
	h.swap(d)
	return nil
}

func (h *host) SaveDoc(ctx context.Context, path string) error { return h.d.Save(ctx, path) }

func (h *host) swap(d *doc.Document) {
	if h.d != nil {
		h.d.Close()
	}
	h.d = d
	h.sel = api.Selection{} // la première feuille
}

// newHost ouvre un classeur vide. Le cache de l'application (répertoires de
// travail des classeurs ouverts) est isolé dans un répertoire temporaire.
func newHost(t *testing.T) *host {
	t.Helper()
	isolateCache(t)
	h := &host{t: t}
	if err := h.NewDoc(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if h.d != nil {
			h.d.Close()
		}
	})
	return h
}

func isolateCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := archive.Root
	archive.Root = func() (string, error) { return dir, nil }
	t.Cleanup(func() { archive.Root = old })
}

// args construit les arguments d'une commande : clé, valeur, clé, valeur…
func args(kv ...any) map[string]any {
	if len(kv)%2 != 0 {
		panic("args : nombre impair")
	}
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = norm(kv[i+1])
	}
	return m
}

// norm convertit les int en int64 et les tableaux Go en []any, comme les
// arguments que reçoit la couche de commandes (JSON, Lua).
func norm(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case [][]any:
		out := make([]any, len(x))
		for i, r := range x {
			out[i] = norm(r)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = norm(e)
		}
		return out
	}
	return v
}

// do exécute une commande et échoue le test en cas d'erreur.
func (h *host) do(name string, kv ...any) any {
	h.t.Helper()
	res, err := api.Invoke(context.Background(), h, name, args(kv...))
	if err != nil {
		h.t.Fatalf("%s %v : %v", name, kv, err)
	}
	return res
}

// try exécute une commande et renvoie son erreur.
func (h *host) try(name string, kv ...any) (any, error) {
	return api.Invoke(context.Background(), h, name, args(kv...))
}

// fails vérifie qu'une commande est refusée avec un message contenant want.
func (h *host) fails(want, name string, kv ...any) {
	h.t.Helper()
	_, err := h.try(name, kv...)
	if err == nil {
		h.t.Fatalf("%s %v : acceptée, erreur attendue (%q)", name, kv, want)
	}
	if !strings.Contains(err.Error(), want) {
		h.t.Fatalf("%s %v : erreur %q, attendu %q", name, kv, err, want)
	}
}

// lua exécute un script et échoue le test s'il s'arrête en erreur.
func (h *host) lua(src string) api.ScriptResult {
	h.t.Helper()
	res, err := api.RunScript(h, "Test", src, "")
	if err != nil {
		h.t.Fatal(err)
	}
	if res.Error != "" {
		h.t.Fatalf("script : %s\n%s", res.Error, src)
	}
	return res
}

// paste colle un bloc à partir de (row, col) sur la feuille affichée.
func (h *host) paste(row, col int, rows ...[]any) {
	h.t.Helper()
	h.do("cells.paste", "row", row, "col", col, "values", norm(rows))
}

// set saisit une cellule de la feuille affichée.
func (h *host) set(row, col int, v any) any {
	h.t.Helper()
	return h.do("cells.set", "row", row, "col", col, "value", norm(v))
}

// get lit une cellule de la feuille affichée.
func (h *host) get(row, col int) api.CellValue {
	h.t.Helper()
	return h.do("cells.get", "row", row, "col", col).(api.CellValue)
}

// getOn lit une cellule d'une feuille donnée.
func (h *host) getOn(sheet string, row, col int) api.CellValue {
	h.t.Helper()
	return h.do("cells.get", "sheet", sheet, "row", row, "col", col).(api.CellValue)
}

// val est la valeur affichée d'une cellule, sous forme de texte.
func (h *host) val(row, col int) string {
	h.t.Helper()
	return fmt.Sprint(h.get(row, col).Value)
}

// wantVal vérifie la valeur affichée d'une cellule.
func (h *host) wantVal(row, col int, want string) {
	h.t.Helper()
	if got := h.val(row, col); got != want {
		h.t.Fatalf("cellule L%dC%d = %q, attendu %q (saisie %q)", row, col, got, want, h.get(row, col).Raw)
	}
}

// sheet décrit une feuille (vide : la feuille affichée).
func (h *host) sheet(name string) api.SheetView {
	h.t.Helper()
	if name == "" {
		return h.do("sheets.get").(api.SheetView)
	}
	return h.do("sheets.get", "sheet", name).(api.SheetView)
}

// table trouve un tableau par son nom, sur n'importe quelle feuille.
func (h *host) table(name string) api.Table {
	h.t.Helper()
	for _, s := range h.sheetNames() {
		for _, tb := range h.sheet(s).Tables {
			if tb.Name == name {
				return tb
			}
		}
	}
	h.t.Fatalf("tableau %q introuvable", name)
	return api.Table{}
}

// sheetNames liste les feuilles dans l'ordre des onglets.
func (h *host) sheetNames() []string {
	h.t.Helper()
	var list []struct {
		Name string `json:"name"`
	}
	decode(h.t, h.do("sheets.list"), &list)
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Name
	}
	return out
}

// decode convertit un résultat de commande (types non exportés compris)
// par un aller-retour JSON, comme le voit un client MCP.
func decode(t *testing.T, v any, into any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%v : %s", err, b)
	}
}

// onlyTable renomme l'unique tableau de la feuille affichée.
func (h *host) onlyTable(name string) api.Table {
	h.t.Helper()
	tbs := h.sheet("").Tables
	if len(tbs) != 1 {
		h.t.Fatalf("%d tableaux sur la feuille : %+v", len(tbs), tbs)
	}
	if tbs[0].Name != name {
		h.do("tables.rename", "table", tbs[0].Name, "name", name)
	}
	return h.table(name)
}

// rows lit toutes les lignes affichées d'un tableau.
func (h *host) rows(table string) api.Rows {
	h.t.Helper()
	return h.do("tables.rows", "table", table).(api.Rows)
}

// column lit une colonne d'un tableau, valeurs mises en texte.
func (h *host) column(table, col string) []string {
	h.t.Helper()
	r := h.rows(table)
	idx := -1
	for i, c := range r.Columns {
		if c == col {
			idx = i
		}
	}
	if idx < 0 {
		h.t.Fatalf("tableau %s : pas de colonne %s (%v)", table, col, r.Columns)
	}
	out := make([]string, len(r.Rows))
	for i, row := range r.Rows {
		out[i] = text(row[idx])
	}
	return out
}

func (h *host) wantColumn(table, col string, want ...string) {
	h.t.Helper()
	if got := h.column(table, col); strings.Join(got, "|") != strings.Join(want, "|") {
		h.t.Fatalf("%s[%s] = %q, attendu %q", table, col, got, want)
	}
}

// value exécute une requête qui renvoie une seule valeur.
func (h *host) value(sql string, params ...any) string {
	h.t.Helper()
	kv := []any{"sql", sql}
	if len(params) > 0 {
		kv = append(kv, "params", norm(params))
	}
	var q struct {
		Rows [][]any `json:"rows"`
	}
	decode(h.t, h.do("db.query", kv...), &q)
	if len(q.Rows) == 0 || len(q.Rows[0]) == 0 {
		return "<aucune>"
	}
	return text(q.Rows[0][0])
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
	}
	return fmt.Sprint(v)
}

func (h *host) state() api.DocState {
	h.t.Helper()
	return h.do("doc.state").(api.DocState)
}

func (h *host) undo() { h.t.Helper(); h.do("doc.undo") }
func (h *host) redo() { h.t.Helper(); h.do("doc.redo") }

// path donne un chemin de fichier dans un répertoire temporaire du test.
func path(t *testing.T, name string) string {
	return filepath.Join(t.TempDir(), name)
}

// reopen enregistre le classeur dans path, le ferme et le rouvre.
func (h *host) reopen(p string) {
	h.t.Helper()
	h.do("doc.save", "path", p)
	if st := h.state(); st.Dirty {
		h.t.Fatalf("modifié juste après l'enregistrement : %+v", st)
	}
	h.do("doc.new")
	h.do("doc.open", "path", p)
}

// snapshot décrit tout ce qui est visible du classeur : feuilles, cellules
// libres, tableaux (colonnes, types, formules, lignes), noms, objets. Deux
// instantanés égaux signifient deux classeurs identiques pour l'utilisateur.
func (h *host) snapshot() string {
	h.t.Helper()
	var b strings.Builder
	for _, s := range h.sheetNames() {
		v := h.sheet(s)
		fmt.Fprintf(&b, "== %s\n", v.Name)
		cells := append([]api.CellView(nil), v.Cells...)
		sort.Slice(cells, func(i, j int) bool {
			if cells[i].Row != cells[j].Row {
				return cells[i].Row < cells[j].Row
			}
			return cells[i].Col < cells[j].Col
		})
		for _, c := range cells {
			fmt.Fprintf(&b, "  L%dC%d %q = %q err=%v\n", c.Row, c.Col, c.Raw, c.Value, c.Error)
		}
		for _, n := range v.Names {
			fmt.Fprintf(&b, "  nom %s L%dC%d\n", n.Name, n.Row, n.Col)
		}
		tbs := append([]api.Table(nil), v.Tables...)
		sort.Slice(tbs, func(i, j int) bool { return tbs[i].Name < tbs[j].Name })
		for _, tb := range tbs {
			fmt.Fprintf(&b, "  tableau %s @L%dC%d %d lignes\n", tb.Name, tb.Row, tb.Col, tb.Rows)
			for _, c := range tb.Columns {
				fmt.Fprintf(&b, "    col %s %s fixe=%v clé=%v f=%q err=%d\n", c.Name, c.Type, c.Fixed, c.Key, c.Formula, c.Errors)
			}
			r := h.rows(tb.Name)
			for _, row := range r.Rows {
				cells := make([]string, len(row))
				for i, c := range row {
					cells[i] = text(c)
				}
				fmt.Fprintf(&b, "    | %s\n", strings.Join(cells, " | "))
			}
		}
		for _, o := range v.Pivots {
			c, _ := json.Marshal(o.Config)
			fmt.Fprintf(&b, "  tcd %s @L%dC%d %s %s\n", o.Name, o.Row, o.Col, c, o.Error)
		}
		for _, o := range v.Charts {
			c, _ := json.Marshal(o.Config)
			fmt.Fprintf(&b, "  graphique %s @L%dC%d %s %s\n", o.Name, o.Row, o.Col, c, o.Error)
		}
		for _, o := range v.Buttons {
			fmt.Fprintf(&b, "  bouton %s %s.%s @L%dC%d\n", o.Label, o.Script, o.Function, o.Row, o.Col)
		}
		// Tout ce que la grille affiche : mise en forme, largeurs, volets,
		// protection, règles, filtres…
		lay, err := h.d.Layout(context.Background(), v.ID)
		if err != nil {
			h.t.Fatal(err)
		}
		js, _ := json.MarshalIndent(lay, "  ", " ")
		b.Write(js)
		b.WriteString("\n")
	}
	scripts, _ := json.Marshal(h.do("scripts.list"))
	fmt.Fprintf(&b, "scripts %s\n", scripts)
	return b.String()
}

// sameSnapshot compare deux instantanés et montre la première différence.
func sameSnapshot(t *testing.T, what, want, got string) {
	t.Helper()
	if want == got {
		return
	}
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			t.Fatalf("%s : différence ligne %d\n  attendu : %s\n  obtenu  : %s\n--- attendu ---\n%s\n--- obtenu ---\n%s", what, i+1, a, b, want, got)
		}
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := path(t, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fmt_ met une valeur affichée en texte.
func fmt_(v any) string { return fmt.Sprint(v) }

// asText met un résultat de commande en JSON.
func asText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// pasteTable colle un tableau (première ligne : en-têtes) sur la feuille
// affichée et le nomme. Si le collage n'a pas reconnu la ligne d'en-têtes
// (données toutes en texte), l'anomalie est signalée et l'en-tête promu.
func (h *host) pasteTable(name string, row, col int, rows ...[]any) {
	h.t.Helper()
	h.paste(row, col, rows...)
	var tb api.Table
	for _, x := range h.sheet("").Tables {
		if x.Row == row && x.Col == col {
			tb = x
		}
	}
	if tb.Name == "" {
		h.t.Fatalf("pas de tableau collé en L%dC%d", row, col)
	}
	if first := fmt.Sprint(rows[0][0]); tb.Columns[0].Name != first {
		knownBug(h.t, "collage-entetes", fmt.Sprintf("cells.paste : la ligne d'en-têtes %v n'est pas reconnue quand les données sont toutes du texte (colonnes « %s »…)", rows[0], tb.Columns[0].Name))
		h.do("tables.promote_header", "table", tb.Name)
	}
	if tb.Name != name {
		h.do("tables.rename", "table", tb.Name, "name", name)
	}
}
