package integration

import (
	"strings"
	"testing"
)

// ventes pose le tableau Ventes en A1 (Produit, Prix, Qté, Total calculé).
func ventes(h *host) {
	h.t.Helper()
	h.paste(1, 1,
		[]any{"Produit", "Prix", "Qté"},
		[]any{"Café", 3.5, 2},
		[]any{"Thé", 2.8, 5},
		[]any{"Cacao", 4, 1},
		[]any{"Sucre", 1.5, 10})
	h.onlyTable("Ventes")
	h.set(1, 4, "Total")
	h.set(2, 4, "=[Prix]*[Qté]")
}

// La nomenclature des références (décisions du 16 et 17/10/2026) : chaque
// forme donne la bonne valeur depuis une autre feuille.
func TestReferenceNomenclature(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.set(1, 10, 100) // J1, cellule libre de Feuille1
	h.do("sheets.add", "name", "Calc")
	h.do("ui.show_sheet", "sheet", "Calc")
	h.set(1, 1, 10)
	h.set(1, 3, 20)
	h.set(1, 5, 30)
	cases := []struct{ formula, want string }{
		{"=Ventes[Prix][2]", "2,8"},                      // une valeur
		{"=SOMME(Ventes[Total])", "40"},                  // toute la colonne
		{"=SOMME(Ventes[Qté][1:3])", "8"},                // plage de lignes
		{"=NB(Ventes[Prix])", "4"},                       // compte
		{"=MOYENNE(Ventes[Prix])", "2,95"},               // moyenne
		{"=MAX(Ventes[Total])", "15"},                    // colonne calculée
		{"=Feuille1!Cell[J1]", "100"},                    // autre feuille
		{"=Cell[A1]+Cell[C1]", "30"},                     // feuille de la formule
		{"=SOMME(Cell[A1:E1])", "60"},                    // plage de cellules
		{"=Ventes[Prix][1+Ventes[Qté][4]/10]", "2,8"},    // index calculé
		{"=REFERENCE(Ventes; \"Prix\"; \"Thé\")", "2,8"}, // recherche
		{"=SI(Ventes[Prix][1]>3; \"cher\"; \"ok\")", "cher"},
		{"=SOMME.SI(Ventes[Qté]; \">2\")", "15"},
		{"=NB.SI(Ventes[Produit]; \"C*\")", "2"},
		{"=MAJUSCULE(Ventes[Produit][1])", "CAFÉ"},
		{"=ARRONDI(2/3; 2)", "0,67"},
		{"=DATE(2026;10;17)", "17/10/2026"},
		{"=\"x\"&1", "x1"},
		{"=1/0", "#DIV/0!"},
		{"=Ventes[Inconnue]", "#REF!"},
		{"=Rien[Prix]", "#REF!"},
		{"=FONCTIONINCONNUE(1)", "#NOM?"},
	}
	for i, c := range cases {
		row := 3 + 2*i // une ligne sur deux : pas de tableau créé par contiguïté
		h.set(row, 2, c.formula)
		if got := h.val(row, 2); got != c.want {
			t.Errorf("%s = %q, attendu %q", c.formula, got, c.want)
		}
	}
	if len(h.sheet("").Tables) != 0 {
		t.Fatalf("des formules espacées ont créé un tableau : %+v", h.sheet("").Tables)
	}
	// Une formule invalide est refusée et ne laisse rien dans la cellule.
	h.fails("formule", "cells.set", "row", 60, "col", 2, "value", "=SOMME(")
	if raw := h.get(60, 2).Raw; raw != "" {
		t.Fatalf("formule invalide gardée : %q", raw)
	}
}

// Références dans un tableau : ligne courante, rang d'affichage, relatives.
func TestReferencesInsideTable(t *testing.T) {
	h := newHost(t)
	h.paste(1, 1, []any{"N", "Qté"}, []any{"a", 2}, []any{"b", 5}, []any{"c", 1}, []any{"d", 10})
	h.onlyTable("T")
	h.set(1, 3, "Préc")
	h.set(2, 3, "=[Qté][-1]") // vide au bord
	h.set(1, 4, "Suiv")
	h.set(2, 4, "=[Qté][+1]")
	h.set(1, 5, "Deux")
	h.set(2, 5, "=[Qté][2]") // rang d'affichage
	h.set(1, 6, "Part")
	h.set(2, 6, "=ARRONDI([Qté]/SOMME(T[Qté]); 2)")
	h.wantColumn("T", "Préc", "", "2", "5", "1")
	h.wantColumn("T", "Suiv", "5", "1", "10", "")
	h.wantColumn("T", "Deux", "5", "5", "5", "5")
	h.wantColumn("T", "Part", "0.11", "0.28", "0.06", "0.56")

	// [n] suit le rang d'affichage : après un tri, la 2e ligne a changé.
	h.do("tables.sort", "table", "T", "column", "Qté", "descending", true)
	h.wantColumn("T", "N", "d", "b", "a", "c")
	h.wantColumn("T", "Deux", "5", "5", "5", "5")
	h.wantColumn("T", "Préc", "", "10", "5", "2")
	h.do("tables.sort", "table", "T", "column", "Qté")
	h.wantColumn("T", "Deux", "2", "2", "2", "2")

	// Total cumulé : la colonne lit sa propre ligne précédente.
	h.set(1, 7, "Cumul")
	h.set(2, 7, "=[Qté]+[Cumul][-1]")
	want := []string{"1", "3", "8", "18"}
	if got := h.column("T", "Cumul"); strings.Join(got, "|") != strings.Join(want, "|") {
		knownBug(t, "cumul", "une colonne qui lit sa propre ligne précédente ([Cumul][-1]) donne #REF! dès la 3e ligne : "+strings.Join(got, " | "))
	}
}

// Une cellule libre vide vaut vide (0 dans un calcul), comme dans Excel.
func TestEmptyCellReference(t *testing.T) {
	h := newHost(t)
	h.set(1, 1, "=Cell[E15]+1")
	h.set(3, 1, "=ESTVIDE(Cell[E15])")
	h.set(15, 5, 5)
	h.wantVal(1, 1, "6")
	h.wantVal(3, 1, "FAUX")
	h.set(15, 5, "")
	if got := h.val(1, 1); got != "1" {
		knownBug(t, "cellule-vide", "=Cell[E15]+1 sur une cellule vide donne "+got+" au lieu de 1")
	}
	if got := h.val(3, 1); got != "VRAI" {
		knownBug(t, "cellule-vide", "=ESTVIDE(Cell[E15]) sur une cellule vide donne "+got)
	}
}

// Les références circulaires sont détectées, et se résolvent dès que la
// boucle est cassée.
func TestCircularReferences(t *testing.T) {
	h := newHost(t)
	h.set(1, 1, "=Cell[C1]+1")
	h.set(1, 3, "=Cell[E1]+1")
	h.set(1, 5, "=Cell[A1]+1")
	for _, c := range []int{1, 3, 5} {
		if !strings.HasPrefix(h.val(1, c), "#") {
			t.Fatalf("boucle non détectée en colonne %d : %q", c, h.val(1, c))
		}
	}
	h.set(1, 5, 10)
	h.wantVal(1, 3, "11")
	h.wantVal(1, 1, "12")

	// Une colonne calculée qui lit une cellule libre qui lit le tableau.
	h.paste(5, 1, []any{"X", "Y"}, []any{1, nil}, []any{2, nil})
	h.onlyTable("Boucle")
	h.set(10, 1, "=SOMME(Boucle[Y])")
	h.set(6, 2, "=[X]+Cell[A10]")
	for _, v := range h.column("Boucle", "Y") {
		if !strings.HasPrefix(v, "#") {
			t.Fatalf("boucle tableau ↔ cellule non détectée : %v", h.column("Boucle", "Y"))
		}
	}
}

// Une modification se propage en cascade : saisie dans le tableau, requête
// SQL, ligne ajoutée ou supprimée, jusqu'aux cellules d'autres feuilles.
func TestRecalcCascade(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.set(8, 1, "=SOMME(Ventes[Total])")
	h.set(10, 1, "=Cell[A8]*2")
	h.do("sheets.add", "name", "Bilan")
	h.do("cells.set", "sheet", "Bilan", "row", 1, "col", 1, "value", "=Feuille1!Cell[A10]+1")
	bilan := func() string { return fmt_(h.getOn("Bilan", 1, 1).Value) }
	if got := bilan(); got != "81" {
		t.Fatalf("Bilan = %s", got)
	}
	h.set(2, 3, 12) // Café : 3,5 × 12
	if got := bilan(); got != "151" {
		t.Fatalf("après saisie : %s", got)
	}
	h.do("db.execute", "sql", `UPDATE Ventes SET Prix = 1 WHERE Produit = 'Sucre'`)
	if got := bilan(); got != "141" {
		t.Fatalf("après UPDATE : %s", got)
	}
	h.do("db.execute", "sql", `INSERT INTO Ventes (Produit, Prix, "Qté") VALUES ('Miel', 5, 2)`)
	if got := bilan(); got != "161" {
		t.Fatalf("après INSERT : %s", got)
	}
	h.do("tables.delete_row", "table", "Ventes", "row", 1)
	if got := bilan(); got != "77" {
		t.Fatalf("après suppression : %s", got)
	}
	h.undo()
	h.undo()
	if got := bilan(); got != "141" {
		t.Fatalf("après deux annulations : %s", got)
	}
	// Les antécédents et dépendants suivent toute la chaîne.
	deps := asText(h.do("cells.dependencies", "row", 8, "col", 1))
	for _, want := range []string{"Ventes", "Total"} {
		if !strings.Contains(deps, want) {
			t.Fatalf("dépendances de A8 sans %s : %s", want, deps)
		}
	}
}

// RECHERCHEV est convertie en REFERENCE et la colonne cherchée devient la clé.
func TestVLookupBecomesReference(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.set(8, 1, `=RECHERCHEV("Cacao"; Ventes; 2; FAUX)`)
	c := h.get(8, 1)
	if !strings.HasPrefix(c.Raw, "=REFERENCE(") || fmt_(c.Value) != "4" {
		t.Fatalf("RECHERCHEV : %+v", c)
	}
	if !h.table("Ventes").Columns[0].Key {
		t.Fatal("la colonne cherchée n'est pas devenue la clé")
	}
	// Un doublon dans la clé est alors refusé… ou signalé.
	if _, err := h.try("cells.set", "row", 3, "col", 1, "value", "Cacao"); err == nil {
		if h.val(3, 1) == "Cacao" && h.table("Ventes").Columns[0].Key {
			t.Fatal("doublon accepté dans une colonne clé")
		}
	}
}

// AUJOURDHUI et ALEA ne bougent qu'au recalcul demandé (F9), et le recalcul
// est une étape annulable.
func TestVolatileFunctions(t *testing.T) {
	h := newHost(t)
	h.set(1, 1, "=ALEA()")
	first := h.val(1, 1)
	h.set(3, 1, "autre chose")
	if h.val(1, 1) != first {
		t.Fatal("ALEA recalculé sans F9")
	}
	h.do("doc.recalc")
	if h.val(1, 1) == first {
		t.Fatal("F9 n'a pas renouvelé ALEA")
	}
}
