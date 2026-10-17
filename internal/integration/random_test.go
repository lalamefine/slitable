package integration

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Suites d'actions tirées au hasard (graine fixe, donc reproductibles) sur
// deux tableaux et des formules. Après chaque action, la grille et le SQL
// disent la même chose ; à la fin, l'enregistrement est fidèle, et tout
// annuler ramène au classeur vide.
func TestRandomEditsKeepInvariants(t *testing.T) {
	seeds, ops := int64(12), 80
	if testing.Short() {
		seeds, ops = 3, 40
	}
	for seed := int64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprint("graine ", seed), func(t *testing.T) { randomSession(t, seed, ops) })
	}
}

func randomSession(t *testing.T, seed int64, n int) {
	h := newHost(t)
	r := rand.New(rand.NewSource(seed))
	start := h.snapshot()
	ventes(h)
	h.set(10, 1, "=SOMME(Ventes[Total])")
	h.set(12, 1, "=NBVAL(Ventes[Produit])")
	words := []string{"Café", "Thé", "Miel", "Sel", "Riz"}

	var log []string
	ops := []func() string{
		func() string { // saisir un prix
			row := 1 + r.Intn(h.table("Ventes").Rows)
			v := float64(r.Intn(100)) / 4
			h.do("cells.set", "sheet", "Feuille1", "row", 1+row, "col", 2, "value", v)
			return fmt.Sprintf("Prix[%d] = %v", row, v)
		},
		func() string { // saisir une quantité
			row := 1 + r.Intn(h.table("Ventes").Rows)
			v := r.Intn(20)
			h.do("cells.set", "sheet", "Feuille1", "row", 1+row, "col", 3, "value", v)
			return fmt.Sprintf("Qté[%d] = %d", row, v)
		},
		func() string { // ajouter une ligne par SQL
			w := words[r.Intn(len(words))]
			h.do("db.execute", "sql", `INSERT INTO Ventes (Produit, Prix, "Qté") VALUES (?, ?, ?)`, "params", []any{w, r.Intn(10), r.Intn(5)})
			return "INSERT " + w
		},
		func() string { // supprimer une ligne
			tb := h.table("Ventes")
			if tb.Rows <= 1 {
				return "rien"
			}
			row := 1 + r.Intn(tb.Rows)
			h.do("tables.delete_row", "table", "Ventes", "row", row)
			return fmt.Sprintf("supprimer ligne %d", row)
		},
		func() string { // insérer une ligne vide
			row := 1 + r.Intn(h.table("Ventes").Rows)
			h.do("tables.insert_row", "table", "Ventes", "row", row)
			return fmt.Sprintf("insérer ligne %d", row)
		},
		func() string { // trier
			col := []string{"Produit", "Prix", "Qté"}[r.Intn(3)]
			desc := r.Intn(2) == 0
			h.do("tables.sort", "table", "Ventes", "column", col, "descending", desc)
			return fmt.Sprintf("trier %s %v", col, desc)
		},
		func() string { // mise à jour SQL
			h.do("db.execute", "sql", `UPDATE Ventes SET "Qté" = coalesce("Qté", 0) + 1 WHERE Prix > ?`, "params", []any{r.Intn(5)})
			return "UPDATE Qté"
		},
		func() string { // script
			h.lua(`for l in db.rows("SELECT _id, Prix FROM Ventes WHERE Prix IS NOT NULL") do db.update("Ventes", l._id, {Prix = l.Prix + 1}) end`)
			return "script Prix + 1"
		},
		func() string { // annuler
			if h.state().Undo == "" {
				return "rien"
			}
			h.undo()
			return "annuler"
		},
		func() string { // rétablir
			if h.state().Redo == "" {
				return "rien"
			}
			h.redo()
			return "rétablir"
		},
	}
	for i := 0; i < n; i++ {
		what := ops[r.Intn(len(ops))]()
		log = append(log, what)
		checkGridMatchesSQL(t, h, strings.Join(log, " ; "))
	}

	want := h.snapshot()
	h.reopen(path(t, "hasard.lattis"))
	sameSnapshot(t, "après réouverture", want, h.snapshot())
	for h.state().Undo != "" {
		h.undo()
	}
	sameSnapshot(t, "tout annulé", start, h.snapshot())
}

// checkGridMatchesSQL compare les lignes affichées de Ventes à la table SQL
// et les formules libres à un calcul SQL.
func checkGridMatchesSQL(t *testing.T, h *host, log string) {
	t.Helper()
	grid := h.rows("Ventes")
	var q struct {
		Rows [][]any `json:"rows"`
	}
	decode(t, h.do("db.query", "sql", `SELECT Produit, Prix, "Qté" FROM Ventes`), &q)
	if len(q.Rows) != len(grid.Rows) {
		t.Fatalf("%d lignes en SQL, %d affichées\naprès : %s", len(q.Rows), len(grid.Rows), log)
	}
	var total float64
	for i, row := range grid.Rows {
		for j := 0; j < 3; j++ {
			if a, b := text(row[j]), text(q.Rows[i][j]); a != b {
				t.Fatalf("ligne %d colonne %d : %q affiché, %q en SQL\naprès : %s", i+1, j+1, a, b, log)
			}
		}
		p, _ := q.Rows[i][1].(float64)
		n, _ := q.Rows[i][2].(float64)
		total += p * n
	}
	for _, c := range h.sheet("Feuille1").Cells {
		switch {
		case strings.HasPrefix(c.Raw, "=SOMME("):
			want := strings.ReplaceAll(strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", total), "0"), "."), ".", ",")
			if c.Value != want {
				t.Fatalf("=SOMME(Ventes[Total]) = %s, attendu %s\naprès : %s", c.Value, want, log)
			}
		case strings.HasPrefix(c.Raw, "=NBVAL("):
			filled := 0
			for _, row := range grid.Rows {
				if text(row[0]) != "" {
					filled++
				}
			}
			if c.Value != fmt.Sprint(filled) {
				t.Fatalf("=NBVAL(Ventes[Produit]) = %s pour %d produits\naprès : %s", c.Value, filled, log)
			}
		}
	}
}
