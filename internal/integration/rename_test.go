package integration

import (
	"fmt"
	"strings"
	"testing"
)

// Renommer un tableau, une colonne, une feuille ou une cellule nommée : ce qui
// les cite doit suivre (formules libres et calculées, autres feuilles,
// tableaux croisés, graphiques), comme Excel. tables.rename le promet
// (« les formules qui le citent suivent »).
func TestRenamesFollowReferences(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.set(8, 1, "=SOMME(Ventes[Total])")
	h.set(10, 1, "=Ventes[Produit][1]")
	h.set(12, 1, `=REFERENCE(Ventes; "Prix"; "Thé")`)
	h.set(14, 1, 7)
	h.do("cells.name", "row", 14, "col", 1, "name", "Taux")
	h.set(16, 1, "=Taux*2")
	h.do("sheets.add", "name", "Autre")
	h.do("cells.set", "sheet", "Autre", "row", 1, "col", 1, "value", "=Feuille1!Cell[A8]")
	h.do("cells.set", "sheet", "Autre", "row", 3, "col", 1, "value", "=SOMME(Ventes[Qté])")
	h.do("pivots.create", "config", map[string]any{"source": "Ventes", "rowFields": []any{"Produit"}, "values": []any{map[string]any{"field": "Qté", "agg": "sum"}}})
	h.do("charts.create", "sheet", "Feuille1", "row", 20, "col", 1, "config", map[string]any{"source": "Ventes", "kind": "bar", "x": "Produit", "series": []any{"Qté"}})

	type probe struct {
		what  string
		sheet string
		row   int
		col   int
	}
	probes := []probe{
		{"somme d'une colonne calculée", "Feuille1", 8, 1},
		{"valeur d'une ligne", "Feuille1", 10, 1},
		{"REFERENCE", "Feuille1", 12, 1},
		{"cellule nommée", "Feuille1", 16, 1},
		{"autre feuille (Feuille!Cell)", "Autre", 1, 1},
		{"autre feuille (tableau)", "Autre", 3, 1},
	}
	before := map[string]string{}
	for _, p := range probes {
		before[p.what] = fmt_(h.getOn(p.sheet, p.row, p.col).Value)
	}
	totals := h.column("Ventes", "Total")

	h.do("tables.rename", "table", "Ventes", "name", "Achats")
	h.do("columns.rename", "table", "Achats", "column", "Qté", "name", "Quantité")
	h.do("columns.rename", "table", "Achats", "column", "Prix", "name", "PU")
	h.do("sheets.rename", "sheet", "Feuille1", "name", "Données")
	h.do("cells.name", "row", 14, "col", 1, "name", "Coef", "sheet", "Données")

	var broken []string
	for _, p := range probes {
		sheet := p.sheet
		if sheet == "Feuille1" {
			sheet = "Données"
		}
		c := h.getOn(sheet, p.row, p.col)
		if got := fmt_(c.Value); got != before[p.what] {
			broken = append(broken, fmt.Sprintf("%s : %s → %s (%s)", p.what, before[p.what], got, c.Raw))
		}
	}
	if got := h.column("Achats", "Total"); strings.Join(got, "|") != strings.Join(totals, "|") {
		broken = append(broken, fmt.Sprintf("colonne calculée : %v → %v (%s)", totals, got, h.table("Achats").Columns[3].Formula))
	}
	for _, s := range h.sheetNames() {
		v := h.sheet(s)
		for _, o := range append(v.Pivots, v.Charts...) {
			if o.Error != "" {
				broken = append(broken, fmt.Sprintf("objet de « %s » : %s", s, o.Error))
			}
		}
	}
	for _, b := range broken {
		knownBug(t, "renommage", b)
	}

	// Quoi qu'il arrive, tout revient d'une série d'annulations.
	for i := 0; i < 5; i++ {
		h.undo()
	}
	for _, p := range probes {
		if got := fmt_(h.getOn(p.sheet, p.row, p.col).Value); got != before[p.what] {
			t.Fatalf("après annulation des renommages, %s = %s, attendu %s", p.what, got, before[p.what])
		}
	}
}

// Les noms refusés : doublons (sans tenir compte de la casse), caractères
// interdits, noms vides.
func TestRenameRejectsBadNames(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.paste(1, 8, []any{"Nom"}, []any{"x"}, []any{"y"})
	tbs := h.sheet("").Tables
	var other string
	for _, tb := range tbs {
		if tb.Name != "Ventes" {
			other = tb.Name
		}
	}
	h.fails("existe déjà", "tables.rename", "table", other, "name", "ventes")
	h.fails("nom de tableau", "tables.rename", "table", other, "name", "Mes ventes")
	h.fails("nom de tableau", "tables.rename", "table", other, "name", "")
	// Deux en-têtes identiques rendent [Produit] ambigu dans les formules ;
	// en SQL, le second devient « Produit (2) ».
	for _, name := range []string{"Produit", "produit"} {
		if _, err := h.try("columns.rename", "table", "Ventes", "column", "Prix", "name", name); err == nil {
			knownBug(t, "colonnes-doublon", "columns.rename accepte une deuxième colonne « "+name+" » dans le tableau")
			h.undo()
		}
	}
	h.set(1, 3, "Produit")
	if cols := h.rows("Ventes").Columns; cols[2] == "Produit" {
		knownBug(t, "colonnes-doublon", "saisir « Produit » dans un en-tête crée une deuxième colonne « Produit »")
	}
	h.undo()
	h.do("sheets.add", "name", "B")
	h.fails("", "sheets.rename", "sheet", "B", "name", "Feuille1")
	if _, err := h.try("sheets.rename", "sheet", "B", "name", "feuille1"); err == nil {
		knownBug(t, "feuilles-casse", "deux feuilles « Feuille1 » et « feuille1 » acceptées (Excel les refuse)")
		h.undo()
	}
	if _, err := h.try("sheets.rename", "sheet", "B", "name", ""); err == nil {
		t.Fatal("feuille sans nom acceptée")
	}
	// Rien n'a changé.
	if got := strings.Join(h.sheetNames(), ","); got != "Feuille1,B" {
		t.Fatalf("feuilles %s", got)
	}
}
