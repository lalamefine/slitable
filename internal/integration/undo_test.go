package integration

import (
	"fmt"
	"testing"
)

// step est une action nommée d'un scénario.
type step struct {
	name string
	do   func()
}

// richSteps construit pas à pas un classeur qui touche à presque tout :
// tableaux, colonnes calculées, cellules libres et nommées, tri, mise en
// forme, filtres, types, SQL, feuilles, tableaux croisés, graphiques,
// scripts, boutons, clé.
func richSteps(h *host) []step {
	return []step{
		{"coller un tableau", func() {
			h.paste(2, 2, []any{"Produit", "Prix", "Qté"}, []any{"Café", 3.5, 2}, []any{"Thé", 2.8, 5}, []any{"Cacao", 4, 1})
		}},
		{"renommer le tableau", func() { h.onlyTable("Ventes") }},
		{"colonne calculée", func() { h.set(2, 5, "Total"); h.set(3, 5, "=[Prix]*[Qté]") }},
		{"cellule libre formule", func() { h.set(8, 2, "=SOMME(Ventes[Total])") }},
		{"nommer une cellule", func() { h.set(9, 2, 0.2); h.do("cells.name", "row", 9, "col", 2, "name", "TVA") }},
		{"formule sur le nom", func() { h.set(10, 2, "=Cell[B8]*(1+TVA)") }},
		{"ajouter une ligne", func() { h.set(6, 2, "Sucre"); h.set(6, 3, 1.2); h.set(6, 4, 10) }},
		{"trier", func() { h.do("tables.sort", "table", "Ventes", "column", "Prix", "descending", true) }},
		{"mise en forme", func() {
			h.do("cells.style", "row", 2, "col", 2, "col2", 5, "style", map[string]any{"bold": true, "fill": "#fff2cc"})
		}},
		{"règle conditionnelle", func() {
			h.do("columns.set_rules", "table", "Ventes", "column", "Prix", "rules", []any{map[string]any{"op": ">", "value": "3", "style": map[string]any{"color": "#c00000"}}})
		}},
		{"filtre", func() {
			h.do("columns.set_filter", "table", "Ventes", "column", "Produit", "values", []any{"Café", "Thé", "Sucre"})
		}},
		{"retirer le filtre", func() { h.do("tables.clear_filters", "table", "Ventes") }},
		{"renommer une colonne", func() { h.do("columns.rename", "table", "Ventes", "column", "Qté", "name", "Quantité") }},
		{"insérer une colonne", func() { h.do("columns.insert", "table", "Ventes", "name", "Remise", "position", 3) }},
		{"fixer un type", func() { h.do("columns.set_type", "table", "Ventes", "column", "Remise", "type", "decimal") }},
		{"requête SQL", func() { h.do("db.execute", "sql", `UPDATE Ventes SET Remise = 0.1 WHERE Prix > 3`) }},
		{"supprimer une ligne", func() { h.do("tables.delete_row", "table", "Ventes", "row", 2) }},
		{"nouvelle feuille", func() { h.do("sheets.add", "name", "Synthèse") }},
		{"formule inter-feuilles", func() { h.do("cells.set", "sheet", "Synthèse", "row", 1, "col", 1, "value", "=Feuille1!Cell[B8]") }},
		{"largeur de colonne", func() { h.do("sheets.set_column_width", "col", 2, "width", 180) }},
		{"volets figés", func() { h.do("sheets.freeze", "rows", 2, "cols", 1) }},
		{"couleur d'onglet", func() { h.do("sheets.color", "sheet", "Synthèse", "color", "#4472c4") }},
		{"renommer la feuille", func() { h.do("sheets.rename", "sheet", "Feuille1", "name", "Données") }},
		{"déplacer la feuille", func() { h.do("sheets.move", "sheet", "Synthèse", "position", 1) }},
		{"tableau croisé", func() {
			h.do("pivots.create", "config", map[string]any{"source": "Ventes", "rowFields": []any{"Produit"}, "values": []any{map[string]any{"field": "Prix", "agg": "sum"}}})
		}},
		{"graphique", func() {
			h.do("charts.create", "sheet", "Données", "row", 12, "col", 2, "config", map[string]any{"source": "Ventes", "kind": "bar", "x": "Produit", "series": []any{"Prix"}, "title": "Prix"})
		}},
		{"script", func() {
			h.do("scripts.save", "name", "Hausse", "source", `db.execute([[UPDATE Ventes SET Prix = Prix * 2]])`)
		}},
		{"lancer le script", func() { h.do("scripts.run", "name", "Hausse") }},
		{"bouton", func() {
			h.do("buttons.create", "sheet", "Données", "row", 1, "col", 8, "label", "Doubler", "script", "Hausse")
		}},
		{"clé", func() { h.do("tables.set_key", "table", "Ventes", "column", "Produit") }},
		{"déplacer le tableau", func() { h.do("tables.move", "table", "Ventes", "row", 3, "col", 3) }},
		{"effacer des cellules", func() { h.do("cells.clear", "sheet", "Données", "row", 9, "col", 2, "row2", 11, "col2", 3) }},
	}
}

// Une longue suite d'actions variées, puis tout annuler, puis tout rétablir :
// chaque étape doit retrouver exactement l'état visible qu'elle avait.
func TestUndoRedoReplaysEveryStep(t *testing.T) {
	h := newHost(t)
	steps := richSteps(h)

	snaps := []string{h.snapshot()}
	for _, s := range steps {
		s.do()
		snaps = append(snaps, h.snapshot())
		if snaps[len(snaps)-1] == snaps[len(snaps)-2] {
			t.Fatalf("« %s » n'a rien changé de visible", s.name)
		}
	}
	// Les étapes peuvent compter plusieurs actions : on annule jusqu'à
	// retrouver chaque instantané, dans l'ordre inverse.
	for i := len(steps) - 1; i >= 0; i-- {
		undoUntil(t, h, snaps[i], fmt.Sprintf("annuler « %s »", steps[i].name))
	}
	if st := h.state(); st.Undo != "" {
		t.Fatalf("il reste « %s » à annuler", st.Undo)
	}
	for i := range steps {
		redoUntil(t, h, snaps[i+1], fmt.Sprintf("rétablir « %s »", steps[i].name))
	}
	if st := h.state(); st.Redo != "" {
		t.Fatalf("il reste « %s » à rétablir", st.Redo)
	}
}

func undoUntil(t *testing.T, h *host, want, what string) {
	t.Helper()
	for k := 0; k < 4; k++ {
		if h.state().Undo == "" {
			break
		}
		h.undo()
		if h.snapshot() == want {
			return
		}
	}
	sameSnapshot(t, what, want, h.snapshot())
}

func redoUntil(t *testing.T, h *host, want, what string) {
	t.Helper()
	for k := 0; k < 4; k++ {
		if h.state().Redo == "" {
			break
		}
		h.redo()
		if h.snapshot() == want {
			return
		}
	}
	sameSnapshot(t, what, want, h.snapshot())
}
