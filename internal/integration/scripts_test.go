package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/lalamefine/lattis/internal/api"
)

// script exécute un script et renvoie son issue, même en erreur.
func (h *host) script(src string) api.ScriptResult {
	h.t.Helper()
	res, err := api.RunScript(h, "Essai", src, "")
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

// Un script en erreur garde ce qu'il a écrit ; une annulation défait tout
// ce qu'il a fait, d'un coup (doc : § 8).
func TestScriptErrorKeepsWritesUntilUndo(t *testing.T) {
	h := newHost(t)
	commandes(h)
	before := h.snapshot()
	res := h.script(`
db.insert("Commandes", {Client = "Petit", Nombre = 1})
sheets.add("Brouillon")
db.execute([[UPDATE Commandes SET Nombre = Nombre + 100]])
error("stop")
db.insert("Commandes", {Client = "Jamais"})
`)
	if !strings.Contains(res.Error, "stop") {
		t.Fatalf("erreur %q", res.Error)
	}
	h.wantColumn("Commandes", "Client", "Durand", "Martin", "L'Atelier", "Petit")
	h.wantColumn("Commandes", "Nombre", "103", "101", "102", "101")
	if st := h.state(); st.Undo != "Script « Essai »" {
		t.Fatalf("étape %q", st.Undo)
	}
	h.undo()
	sameSnapshot(t, "après annulation du script en erreur", before, h.snapshot())
	h.redo()
	h.wantColumn("Commandes", "Client", "Durand", "Martin", "L'Atelier", "Petit")
}

// Les écritures d'un script suivent les règles de la saisie : élargissement
// d'un type inféré, saisie marquée hors type pour un type fixé, tableau
// qui grandit et pousse ceux du dessous, formules recalculées.
func TestScriptWritesBehaveLikeTyping(t *testing.T) {
	h := newHost(t)
	commandes(h)
	h.paste(7, 1, []any{"Sous", "Valeur"}, []any{"x", 1})
	var sous string
	for _, tb := range h.sheet("").Tables {
		if tb.Row == 7 {
			sous = tb.Name
		}
	}
	h.do("columns.fix_type", "table", "Commandes", "column", "Montant HT")
	h.set(20, 1, "=SOMME(Commandes[Nombre])")
	sum := func() string {
		for _, c := range h.sheet("").Cells {
			if strings.HasPrefix(c.Raw, "=SOMME(Commandes[Nombre])") {
				return c.Value
			}
		}
		t.Fatal("formule perdue")
		return ""
	}
	h.lua(`for i = 1, 4 do db.insert("Commandes", {Client = "N" .. i, Nombre = i}) end`)
	if got := sum(); got != "16" {
		t.Fatalf("somme après insertions : %q", got)
	}
	h.lua(`db.insert("Commandes", {Client = "Texte", Nombre = "beaucoup", ["Montant HT"] = "cher"})`)
	tb := h.table("Commandes")
	if tb.Rows != 8 {
		t.Fatalf("lignes %d", tb.Rows)
	}
	if tb.Columns[1].Type != "text" {
		t.Fatalf("Nombre (inféré) devrait s'élargir en texte : %s", tb.Columns[1].Type)
	}
	if c := tb.Columns[2]; c.Type != "decimal" || c.Errors != 1 {
		t.Fatalf("Montant HT (fixé) : %+v", c)
	}
	if s := h.table(sous); s.Row <= tb.Row+tb.Rows+1 {
		t.Fatalf("le tableau du dessous n'a pas été poussé : ligne %d, Commandes jusqu'à %d", s.Row, tb.Row+tb.Rows)
	}
	// À arbitrer : une seule valeur texte passe toute la colonne en texte, et
	// SOMME ignore alors les nombres déjà saisis (Excel les garderait).
	if got := sum(); got != "16" {
		t.Logf("à arbitrer : après élargissement de Nombre en texte, =SOMME(Commandes[Nombre]) vaut %s au lieu de 16", got)
	}
}

// Le bac à sable : ni fichiers, ni programmes, ni chargement de code.
func TestScriptSandbox(t *testing.T) {
	h := newHost(t)
	for _, src := range []string{
		`io.open("/etc/passwd")`,
		`os.execute("echo x")`,
		`os.remove("x")`,
		`os.getenv("HOME")`,
		`require("os")`,
		`loadstring("return 1")()`,
		`load(function() return nil end)`,
		`dofile("/etc/passwd")`,
		`debug.getinfo(1)`,
		`string.rep("x", 100 * 1024 * 1024)`,
		`local function f() return f() + 1 end f()`,
		`db.execute("CREATE TABLE x (a)")`,
		`db.execute("DELETE FROM _lattis_sheets")`,
	} {
		if res := h.script(src); res.Error == "" {
			t.Errorf("accepté : %s", src)
		}
	}
	if got := strings.Join(h.sheetNames(), ","); got != "Feuille1" {
		t.Fatalf("feuilles %s", got)
	}
	// os.date et os.time restent disponibles.
	h.lua(`assert(os.date("%Y") + 0 >= 2026); assert(os.time() > 0)`)
}

// Arrêter : une boucle infinie et une requête SQL sans fin s'arrêtent au
// bouton ; un seul script tourne à la fois.
func TestScriptStop(t *testing.T) {
	h := newHost(t)
	for _, src := range []string{
		`while true do end`,
		`db.value("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c")`,
	} {
		done := make(chan api.ScriptResult, 1)
		go func() {
			res, _ := api.RunScript(h, "Boucle", src, "")
			done <- res
		}()
		time.Sleep(200 * time.Millisecond)
		if _, err := api.RunScript(h, "Autre", `print(1)`, ""); err != api.ErrBusy {
			t.Errorf("deuxième script pendant le premier : %v", err)
		}
		api.StopScript()
		select {
		case res := <-done:
			if !strings.Contains(res.Error, "arrêté") {
				t.Errorf("%s : %q", src, res.Error)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s : pas arrêté au bout de 5 s", src)
		}
	}
	h.lua(`print("de nouveau libre")`)
}

// Les scripts et boutons voyagent avec le classeur : après réouverture, un
// bouton relance sa fonction, en une étape d'annulation.
func TestButtonsSurviveReopen(t *testing.T) {
	h := newHost(t)
	commandes(h)
	h.do("scripts.save", "name", "Outils", "source", `
local taux = 2
function doubler()
  db.execute([[UPDATE Commandes SET Nombre = Nombre * ?]], taux)
  print("doublé")
end
function compter()
  print(db.value("SELECT count(*) FROM Commandes"))
end`)
	h.do("buttons.create", "row", 1, "col", 8, "label", "Doubler", "script", "Outils", "function", "doubler")
	h.do("buttons.create", "row", 3, "col", 8, "label", "Compter", "script", "Outils", "function", "compter")
	h.reopen(path(t, "boutons.lattis"))
	btns := h.do("buttons.list").([]api.ButtonView)
	if len(btns) != 2 {
		t.Fatalf("boutons %+v", btns)
	}
	res := h.do("buttons.run", "button", "Doubler").(api.ScriptResult)
	if res.Error != "" || strings.Join(res.Output, ";") != "doublé" {
		t.Fatalf("clic : %+v", res)
	}
	h.wantColumn("Commandes", "Nombre", "6", "2", "4")
	if st := h.state(); st.Undo != "Script « Outils »" {
		t.Fatalf("étape %q", st.Undo)
	}
	h.undo()
	h.wantColumn("Commandes", "Nombre", "3", "1", "2")
	// Un bouton dont la fonction n'existe plus signale une erreur sans rien casser.
	h.do("scripts.save", "name", "Outils", "source", `function autre() end`)
	if res, err := h.try("buttons.run", "button", "Compter"); err == nil && res.(api.ScriptResult).Error == "" {
		t.Fatal("bouton sans fonction exécuté sans erreur")
	}
	// Supprimer le script : le bouton reste, mais son clic échoue proprement.
	h.do("scripts.delete", "name", "Outils")
	if res, err := h.try("buttons.run", "button", "Doubler"); err == nil && res.(api.ScriptResult).Error == "" {
		t.Fatal("bouton sans script exécuté sans erreur")
	}
}
