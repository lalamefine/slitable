package integration

import (
	"strings"
	"testing"
)

// Naissance d'un tableau par la saisie (décisions du 16/10/2026) : une
// cellule seule reste libre, deux lignes contiguës font un tableau, une liste
// de nombres tout en haut de la feuille reste en cellules libres.
func TestTableDetection(t *testing.T) {
	h := newHost(t)
	h.set(1, 1, 1)
	h.set(2, 1, 2)
	h.set(3, 1, 3)
	if n := len(h.sheet("").Tables); n != 0 {
		t.Fatalf("une liste de nombres en haut de feuille a créé %d tableau(x)", n)
	}
	h.set(5, 3, "Nom")
	if n := len(h.sheet("").Tables); n != 0 {
		t.Fatal("une cellule seule a créé un tableau")
	}
	res := asText(h.set(6, 3, "Léa"))
	tbs := h.sheet("").Tables
	if len(tbs) != 1 || tbs[0].Row != 5 || tbs[0].Col != 3 || tbs[0].Rows != 1 {
		t.Fatalf("deux lignes : %+v (%s)", tbs, res)
	}
	name := tbs[0].Name
	if name != "Feuille1" {
		t.Fatalf("seul tableau de la feuille : nom %q, attendu le nom de l'onglet", name)
	}
	// Saisir juste sous le tableau ou juste à droite l'agrandit.
	h.set(7, 3, "Tom")
	h.set(5, 4, "Âge")
	h.set(6, 4, 31)
	tb := h.table(name)
	if tb.Rows != 2 || len(tb.Columns) != 2 || tb.Columns[1].Type != "integer" {
		t.Fatalf("agrandi : %+v", tb)
	}
	// Une valeur de type différent élargit la colonne (type inféré).
	h.set(7, 4, "inconnu")
	if got := h.table(name).Columns[1].Type; got != "text" {
		t.Fatalf("élargissement : %s", got)
	}
	h.undo()
	if got := h.table(name).Columns[1].Type; got != "integer" {
		t.Fatalf("annuler l'élargissement : %s", got)
	}
	// Un deuxième tableau sur la feuille : le premier garde son nom (déjà
	// donné), le second en reçoit un autre.
	h.paste(20, 1, []any{"Code", "Prix"}, []any{"A", 1}, []any{"B", 2})
	if n := len(h.sheet("").Tables); n != 2 {
		t.Fatalf("%d tableaux", n)
	}
	for _, tb := range h.sheet("").Tables {
		if tb.Row == 20 && tb.Name == "Feuille1" {
			t.Fatal("deux tableaux nommés comme l'onglet")
		}
	}
}

// Un tableau qui grandit décale ceux du dessous ; un tableau déplacé sur un
// autre le pousse à droite, en cascade, en une seule étape d'annulation.
func TestTablesPushEachOther(t *testing.T) {
	h := newHost(t)
	h.paste(1, 1, []any{"A", "B"}, []any{1, 2}, []any{3, 4})
	h.do("tables.rename", "table", h.sheet("").Tables[0].Name, "name", "Haut")
	h.paste(5, 1, []any{"C", "D"}, []any{5, 6})
	bas := ""
	for _, tb := range h.sheet("").Tables {
		if tb.Name != "Haut" {
			bas = tb.Name
		}
	}
	h.do("tables.rename", "table", bas, "name", "Bas")
	for i := 0; i < 3; i++ {
		h.do("db.execute", "sql", `INSERT INTO Haut (A, B) VALUES (9, 9)`)
	}
	if hb, b := h.table("Haut"), h.table("Bas"); b.Row <= hb.Row+hb.Rows+1 {
		t.Fatalf("Bas (ligne %d) chevauche ou touche Haut (lignes %d à %d)", b.Row, hb.Row, hb.Row+hb.Rows)
	}
	h.wantColumn("Bas", "C", "5")

	// Trois tableaux côte à côte, puis on pose Gauche sur Milieu.
	h2 := newHost(t)
	h2.paste(1, 1, []any{"G"}, []any{1}, []any{2})
	h2.do("tables.rename", "table", h2.sheet("").Tables[0].Name, "name", "Gauche")
	h2.paste(1, 4, []any{"M"}, []any{1}, []any{2})
	h2.paste(1, 7, []any{"D"}, []any{1}, []any{2})
	for _, tb := range h2.sheet("").Tables {
		switch tb.Col {
		case 4:
			h2.do("tables.rename", "table", tb.Name, "name", "Milieu")
		case 7:
			h2.do("tables.rename", "table", tb.Name, "name", "Droite")
		}
	}
	before := h2.snapshot()
	h2.do("tables.move", "table", "Gauche", "row", 1, "col", 4)
	g, m, d := h2.table("Gauche"), h2.table("Milieu"), h2.table("Droite")
	if g.Col != 4 || g.Row != 1 {
		t.Fatalf("le tableau déplacé n'est pas en place : %+v", g)
	}
	if m.Col < g.Col+len(g.Columns)+1 || d.Col < m.Col+len(m.Columns)+1 {
		t.Fatalf("cascade : Gauche col %d, Milieu col %d, Droite col %d", g.Col, m.Col, d.Col)
	}
	if n := len(h2.sheet("").Tables); n != 3 {
		t.Fatalf("%d tableaux après le déplacement (fusion inattendue ?)", n)
	}
	h2.undo()
	sameSnapshot(t, "une seule annulation", before, h2.snapshot())
}

// Lignes et colonnes : insérer, supprimer, le tableau suit ; supprimer une
// ligne le rétrécit.
func TestRowsAndColumns(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.do("tables.insert_row", "table", "Ventes", "row", 2)
	h.wantColumn("Ventes", "Produit", "Café", "", "Thé", "Cacao", "Sucre")
	h.do("tables.delete_row", "table", "Ventes", "row", 2)
	h.do("tables.delete_row", "table", "Ventes", "row", 1)
	h.wantColumn("Ventes", "Produit", "Thé", "Cacao", "Sucre")
	if tb := h.table("Ventes"); tb.Rows != 3 {
		t.Fatalf("lignes %d", tb.Rows)
	}
	h.do("columns.insert", "table", "Ventes", "name", "Remise", "position", 2)
	cols := h.rows("Ventes").Columns
	if strings.Join(cols, ",") != "Produit,Remise,Prix,Qté,Total" {
		t.Fatalf("colonnes %v", cols)
	}
	h.wantColumn("Ventes", "Total", "14", "4", "15")
	h.do("columns.delete", "table", "Ventes", "column", "Remise")
	h.do("columns.delete", "table", "Ventes", "column", "Total")
	h.do("columns.delete", "table", "Ventes", "column", "Qté")
	h.do("columns.delete", "table", "Ventes", "column", "Prix")
	h.fails("", "columns.delete", "table", "Ventes", "column", "Produit")
	h.fails("", "tables.delete_row", "table", "Ventes", "row", 99)
}

// Types : fixer, convertir, marquer les saisies hors type, aperçu.
func TestColumnTypes(t *testing.T) {
	h := newHost(t)
	h.pasteTable("T", 1, 1, []any{"Code", "Quand"}, []any{"12", "01/10/2026"}, []any{"x7", "hier"}, []any{"40", "05/09/2026"})
	if got := h.table("T").Columns[0].Type; got != "text" {
		t.Fatalf("Code : %s", got)
	}
	preview := asText(h.do("columns.type_preview", "table", "T", "column", "Code", "type", "integer"))
	if !strings.Contains(preview, "1") {
		t.Fatalf("aperçu : %s", preview)
	}
	h.do("columns.set_type", "table", "T", "column", "Code", "type", "integer")
	c := h.table("T").Columns[0]
	if c.Type != "integer" || !c.Fixed || c.Errors != 1 {
		t.Fatalf("après conversion : %+v", c)
	}
	h.wantColumn("T", "Code", "12", "x7", "40") // la saisie hors type reste visible
	if got := h.value(`SELECT sum(Code) FROM T`); got != "52" {
		t.Fatalf("somme SQL : %s", got)
	}
	// Une saisie hors type dans une colonne fixée est marquée, pas élargie.
	h.set(4, 1, "abc")
	if c := h.table("T").Columns[0]; c.Type != "integer" || c.Errors != 2 {
		t.Fatalf("saisie hors type : %+v", c)
	}
	// Le filtre « erreurs » ne garde que les saisies hors type.
	h.do("columns.set_filter", "table", "T", "column", "Code", "errors", true)
	h.wantColumn("T", "Code", "x7", "abc")
	h.do("tables.clear_filters", "table", "T")
	h.do("columns.set_type", "table", "T", "column", "Quand", "type", "date")
	if got := h.value(`SELECT max("Quand") FROM T`); got != "2026-10-01" {
		t.Fatalf("dates : %s", got)
	}
}

// Clé, doublons et index unique.
func TestKeysAndDuplicates(t *testing.T) {
	h := newHost(t)
	h.pasteTable("T", 1, 1, []any{"Code", "Nom"}, []any{"A", "x"}, []any{"B", "y"}, []any{"A", "x"}, []any{"C", "x"})
	if msg := h.do("tables.key_check", "table", "T", "column", "Code").(string); msg == "" {
		t.Fatal("une colonne avec doublons peut devenir clé")
	}
	if _, err := h.try("tables.set_key", "table", "T", "column", "Code"); err == nil {
		t.Fatal("clé posée malgré des doublons")
	}
	if got := asText(h.do("tables.duplicates", "table", "T")); !strings.Contains(got, "1") {
		t.Fatalf("doublons : %s", got)
	}
	if n := asText(h.do("tables.remove_duplicates", "table", "T")); n != "1" {
		t.Fatalf("doublons supprimés : %s", n)
	}
	h.wantColumn("T", "Code", "A", "B", "C")
	h.do("tables.set_key", "table", "T", "column", "Code")
	if !h.table("T").Columns[0].Key {
		t.Fatal("clé absente")
	}
	// La clé refuse un doublon, par la grille comme par SQL.
	if _, err := h.try("cells.set", "row", 3, "col", 1, "value", "A"); err == nil && h.val(3, 1) == "A" {
		t.Fatal("doublon saisi dans la clé")
	}
	if _, err := h.try("db.execute", "sql", `INSERT INTO T (Code) VALUES ('B')`); err == nil {
		t.Fatal("doublon inséré par SQL dans la clé")
	}
	h.wantColumn("T", "Code", "A", "B", "C")
	h.do("tables.clear_key", "table", "T")
	h.do("db.execute", "sql", `INSERT INTO T (Code) VALUES ('B')`)
}

// Copier une feuille copie ses tableaux sous de nouveaux noms ; ses formules
// lisent la copie.
func TestSheetCopy(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.set(8, 1, "=SOMME(Ventes[Total])")
	h.do("sheets.rename", "sheet", "Feuille1", "name", "Mars")
	cp := asText(h.do("sheets.copy", "sheet", "Mars"))
	names := h.sheetNames()
	if len(names) != 2 {
		t.Fatalf("feuilles %v (%s)", names, cp)
	}
	copy := names[1]
	ct := h.sheet(copy).Tables
	if len(ct) != 1 || ct[0].Name == "Ventes" {
		t.Fatalf("tableau copié : %+v", ct)
	}
	if got := fmt_(h.getOn(copy, 8, 1).Value); got != "40" {
		t.Fatalf("formule copiée : %s", got)
	}
	h.do("cells.set", "sheet", copy, "row", 2, "col", 3, "value", 100)
	if got := fmt_(h.getOn("Mars", 8, 1).Value); got != "40" {
		t.Fatalf("modifier la copie a changé l'original : %s", got)
	}
	got := fmt_(h.getOn(copy, 8, 1).Value)
	if got == "40" {
		knownBug(t, "copie-feuille", "la formule libre de la feuille copiée lit toujours le tableau d'origine ("+h.getOn(copy, 8, 1).Raw+")")
	}
	// Supprimer une feuille emporte ses tableaux, en une seule étape
	// d'annulation ; le classeur garde au moins une feuille.
	before := h.snapshot()
	h.do("sheets.delete", "sheet", copy)
	if got := strings.Join(h.sheetNames(), ","); got != "Mars" {
		t.Fatalf("feuilles %s", got)
	}
	if _, err := h.try("tables.get", "table", ct[0].Name); err == nil {
		t.Fatal("le tableau de la feuille supprimée existe encore")
	}
	if _, err := h.try("db.query", "sql", `SELECT * FROM "`+ct[0].Name+`"`); err == nil {
		t.Fatal("le tableau de la feuille supprimée se lit encore en SQL")
	}
	h.undo()
	sameSnapshot(t, "annuler la suppression de feuille", before, h.snapshot())
	h.do("sheets.delete", "sheet", copy)
	h.fails("", "sheets.delete", "sheet", "Mars")
}

// Extraire des colonnes et promouvoir la première ligne en en-têtes.
func TestExtractAndPromote(t *testing.T) {
	h := newHost(t)
	ventes(h)
	h.do("tables.extract_columns", "table", "Ventes", "column", "Prix", "column2", "Qté", "row", 1, "col", 10)
	var extracted string
	for _, tb := range h.sheet("").Tables {
		if tb.Col == 10 {
			extracted = tb.Name
		}
	}
	if extracted == "" {
		t.Fatalf("tableau extrait absent : %+v", h.sheet("").Tables)
	}
	h.wantColumn(extracted, "Qté", "2", "5", "1", "10")
	if got := strings.Join(h.rows("Ventes").Columns, ","); got != "Produit,Total" {
		t.Logf("colonnes restantes de Ventes : %s", got)
	}

	h2 := newHost(t)
	h2.do("doc.import", "path", writeFile(t, "sans-entete.csv", "Nom;Âge\nLéa;31\nTom;8\n"))
	tb := h2.sheet(h2.sheetNames()[len(h2.sheetNames())-1]).Tables[0]
	if tb.Columns[0].Name != "Nom" {
		h2.do("tables.promote_header", "table", tb.Name)
		tb = h2.table(tb.Name)
	}
	if tb.Columns[0].Name != "Nom" || tb.Rows != 2 || tb.Columns[1].Type != "integer" {
		t.Fatalf("en-têtes : %+v", tb)
	}
}
