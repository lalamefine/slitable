package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// pivotCells lit les cellules calculées du premier TCD d'une feuille.
func (h *host) pivotCells(sheet string) string {
	h.t.Helper()
	lay, err := h.d.Layout(context.Background(), h.sheet(sheet).ID)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(lay.Pivots) == 0 {
		h.t.Fatalf("pas de TCD sur %s", sheet)
	}
	if lay.Pivots[0].Error != "" {
		h.t.Fatalf("TCD en erreur : %s", lay.Pivots[0].Error)
	}
	b, _ := json.Marshal(lay.Pivots[0].Cells)
	return string(b)
}

// chartData lit les données du premier graphique d'une feuille.
func (h *host) chartData(sheet string) string {
	h.t.Helper()
	lay, err := h.d.Layout(context.Background(), h.sheet(sheet).ID)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(lay.Charts) == 0 {
		h.t.Fatalf("pas de graphique sur %s", sheet)
	}
	c := lay.Charts[0]
	b, _ := json.Marshal(map[string]any{"labels": c.Labels, "series": c.Series, "error": c.Error})
	return string(b)
}

// Le TCD et le graphique suivent leurs données : saisie, SQL, script,
// filtre du TCD, puis enregistrement et réouverture.
func TestPivotAndChartFollowData(t *testing.T) {
	h := newHost(t)
	h.paste(1, 1,
		[]any{"Client", "Mois", "Montant"},
		[]any{"Durand", "jan", 10},
		[]any{"Martin", "jan", 5},
		[]any{"Durand", "fév", 7})
	h.onlyTable("V")
	f := h.do("pivots.create", "config", map[string]any{"source": "V", "rowFields": []any{"Client"}, "cols": "Mois",
		"values": []any{map[string]any{"field": "Montant", "agg": "sum"}}})
	var tcd struct{ Name string }
	decode(t, f, &tcd)
	h.do("charts.create", "sheet", "Feuille1", "row", 10, "col", 1, "config", map[string]any{"source": "V", "kind": "bar", "x": "Client", "series": []any{"Montant"}})

	cells := h.pivotCells(tcd.Name)
	for _, want := range []string{`"jan"`, `"fév"`, `"Durand"`, `"17"`, `"22"`} {
		if !strings.Contains(cells, want) {
			t.Fatalf("TCD sans %s : %s", want, cells)
		}
	}
	if got := h.chartData("Feuille1"); !strings.Contains(got, `"values":[17,5]`) {
		t.Fatalf("graphique : %s", got)
	}

	h.set(3, 3, 50)                                                                               // Martin jan : 5 → 50
	h.do("db.execute", "sql", `INSERT INTO V (Client, Mois, Montant) VALUES ('Petit', 'mar', 1)`) // nouveau client
	h.lua(`db.execute([[UPDATE V SET Montant = Montant + 1 WHERE Client = 'Durand']])`)
	cells = h.pivotCells(tcd.Name)
	for _, want := range []string{`"Petit"`, `"mar"`, `"19"`, `"50"`, `"70"`} {
		if !strings.Contains(cells, want) {
			t.Fatalf("TCD après modifications, sans %s : %s", want, cells)
		}
	}
	if got := h.chartData("Feuille1"); !strings.Contains(got, `"values":[19,50,1]`) {
		t.Fatalf("graphique après modifications : %s", got)
	}

	// Le TCD se lit en SQL sous son nom.
	if got := h.value(`SELECT count(*) FROM ` + strings.ReplaceAll(tcdName(t, h), " ", "")); got != "3" {
		t.Fatalf("TCD en SQL : %s lignes", got)
	}

	// Filtre de rapport, total général masqué.
	h.do("pivots.set", "pivot", tcdName(t, h), "config", map[string]any{"source": "V", "rowFields": []any{"Client"},
		"values":  []any{map[string]any{"field": "Montant", "agg": "count"}},
		"filters": []any{map[string]any{"field": "Mois", "values": []any{"jan"}}}, "noGrandTotal": true})
	cells = h.pivotCells(tcd.Name)
	if strings.Contains(cells, "Petit") || strings.Contains(cells, "Total") {
		t.Fatalf("TCD filtré : %s", cells)
	}

	want := h.snapshot()
	h.reopen(path(t, "tcd.lattis"))
	sameSnapshot(t, "TCD et graphique après réouverture", want, h.snapshot())

	// Supprimer la source : TCD et graphique affichent une erreur au lieu
	// de planter, et l'annulation les répare.
	h.do("tables.delete", "tables", []any{"V"})
	lay, _ := h.d.Layout(context.Background(), h.sheet(tcd.Name).ID)
	if len(lay.Pivots) == 0 || lay.Pivots[0].Error == "" {
		t.Fatalf("TCD sans source : %+v", lay.Pivots)
	}
	h.undo()
	h.pivotCells(tcd.Name)
}

func tcdName(t *testing.T, h *host) string {
	t.Helper()
	for _, s := range h.sheetNames() {
		for _, p := range h.sheet(s).Pivots {
			return p.Name
		}
	}
	t.Fatal("pas de TCD")
	return ""
}

// Les objets refusent les configurations impossibles.
func TestObjectsRejectBadConfig(t *testing.T) {
	h := newHost(t)
	ventes(h)
	for _, cfg := range []map[string]any{
		{"source": "Absent", "rowFields": []any{"Produit"}, "values": []any{map[string]any{"field": "Prix", "agg": "sum"}}},
		{"source": "Ventes", "rowFields": []any{"Inconnu"}, "values": []any{map[string]any{"field": "Prix", "agg": "sum"}}},
		{"source": "Ventes", "rowFields": []any{"Produit"}, "values": []any{map[string]any{"field": "Prix", "agg": "médiane-inconnue"}}},
	} {
		if _, err := h.try("pivots.create", "config", cfg); err == nil {
			t.Errorf("TCD accepté : %v", cfg)
		}
	}
	if _, err := h.try("charts.create", "row", 10, "col", 1, "config", map[string]any{"source": "Ventes", "kind": "camembert-3d", "x": "Produit", "series": []any{"Prix"}}); err == nil {
		t.Error("type de graphique inconnu accepté")
	}
	if got := strings.Join(h.sheetNames(), ","); got != "Feuille1" {
		t.Fatalf("un TCD refusé a laissé une feuille : %s", got)
	}
}
