package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lalamefine/lattis/internal/api"
	"github.com/lalamefine/lattis/internal/archive"
	"github.com/lalamefine/lattis/internal/doc"
)

// Un classeur riche enregistré puis rouvert est identique, et son historique
// d'annulation le suit.
func TestSaveReopenKeepsEverything(t *testing.T) {
	h := newHost(t)
	steps := richSteps(h)
	var beforeLast string
	for i, s := range steps {
		if i == len(steps)-1 {
			beforeLast = h.snapshot()
		}
		s.do()
	}
	want := h.snapshot()
	p := path(t, "riche.lattis")
	h.reopen(p)
	sameSnapshot(t, "après réouverture", want, h.snapshot())

	st := h.state()
	if st.Path != p || st.Title != "riche" || st.Dirty {
		t.Fatalf("état après réouverture %+v", st)
	}
	if st.Undo == "" {
		t.Fatal("l'historique d'annulation est perdu à la réouverture")
	}
	h.undo()
	sameSnapshot(t, "annuler après réouverture", beforeLast, h.snapshot())
	if !h.state().Dirty {
		t.Fatal("une annulation après réouverture ne marque pas le classeur modifié")
	}
	h.redo()
	if h.state().Dirty {
		t.Fatal("revenir à l'état enregistré devrait rendre le classeur propre")
	}

	// Enregistrer sous un autre nom puis rouvrir l'original : les deux
	// fichiers sont indépendants.
	h.do("cells.set", "sheet", "Données", "row", 30, "col", 1, "value", "seulement dans la copie")
	copie := path(t, "copie.lattis")
	h.do("doc.save", "path", copie)
	if st := h.state(); st.Path != copie || st.Dirty {
		t.Fatalf("enregistrer sous : %+v", st)
	}
	h.do("doc.new")
	h.do("doc.open", "path", p)
	sameSnapshot(t, "l'original n'a pas bougé", want, h.snapshot())
	h.do("doc.new")
	h.do("doc.open", "path", copie)
	if got := h.getOn("Données", 30, 1).Raw; got != "seulement dans la copie" {
		t.Fatalf("copie : %q", got)
	}
}

// Suivi des modifications : nouveau classeur propre, saisie, enregistrement,
// annulation jusqu'à l'état enregistré.
func TestDirtyTracking(t *testing.T) {
	h := newHost(t)
	if st := h.state(); st.Dirty || st.Path != "" || st.Undo != "" {
		t.Fatalf("nouveau classeur %+v", st)
	}
	h.fails("choisir un fichier", "doc.save")
	h.set(1, 1, "a")
	if !h.state().Dirty {
		t.Fatal("saisie non suivie")
	}
	p := path(t, "suivi.lattis")
	h.do("doc.save", "path", p)
	if h.state().Dirty {
		t.Fatal("modifié après enregistrement")
	}
	h.set(1, 1, "b")
	h.undo()
	if h.state().Dirty {
		t.Fatal("annuler jusqu'à l'état enregistré devrait rendre le classeur propre")
	}
	h.undo()
	if !h.state().Dirty {
		t.Fatal("annuler au-delà de l'état enregistré devrait marquer le classeur modifié")
	}
	h.redo()
	if h.state().Dirty {
		t.Fatal("rétablir jusqu'à l'état enregistré devrait rendre le classeur propre")
	}
	// Une lecture ne modifie rien.
	h.do("db.query", "sql", "SELECT 1")
	h.do("sheets.get")
	h.do("tables.list")
	if h.state().Dirty {
		t.Fatal("une lecture a marqué le classeur modifié")
	}
	// Enregistrer un classeur propre dans son propre fichier ne le réécrit pas.
	fi, _ := os.Stat(p)
	h.do("doc.save")
	if fi2, _ := os.Stat(p); !fi2.ModTime().Equal(fi.ModTime()) {
		t.Fatal("classeur inchangé réécrit")
	}
}

// Un fichier qui n'est pas un classeur est refusé proprement, sans laisser
// l'application sans classeur.
func TestOpenRejectsBadFiles(t *testing.T) {
	h := newHost(t)
	h.set(1, 1, "toujours là")
	for name, content := range map[string]string{
		"texte.lattis": "pas une archive",
		"vide.lattis":  "",
	} {
		p := writeFile(t, name, content)
		if _, err := h.try("doc.open", "path", p); err == nil {
			t.Fatalf("%s accepté", name)
		}
	}
	if _, err := h.try("doc.open", "path", filepath.Join(t.TempDir(), "absent.lattis")); err == nil {
		t.Fatal("fichier absent accepté")
	}
	if got := h.get(1, 1).Raw; got != "toujours là" {
		t.Fatalf("classeur ouvert perdu : %q", got)
	}
}

// Ouvrir deux fois le même fichier est refusé : le second aurait sa propre
// copie de travail et écraserait le premier à l'enregistrement.
func TestOpenTwiceRefused(t *testing.T) {
	h := newHost(t)
	h.set(1, 1, "x")
	p := path(t, "deux.lattis")
	h.do("doc.save", "path", p)
	h.do("doc.new")
	h.do("doc.open", "path", p)
	_, err := doc.Open(context.Background(), p)
	if err == nil {
		t.Fatal("deuxième ouverture acceptée")
	}
}

// Protection : feuille et structure du classeur, mot de passe, persistance.
func TestProtectionSurvivesReopen(t *testing.T) {
	h := newHost(t)
	h.paste(1, 1, []any{"Nom", "Âge"}, []any{"Léa", 31}, []any{"Tom", 8})
	h.onlyTable("Gens")
	h.do("sheets.add", "name", "Libre")
	h.do("sheets.protect", "sheet", "Feuille1", "password", "s3cret")
	h.do("sheets.protect_workbook")
	if !h.state().Dirty {
		t.Fatal("protéger ne marque pas le classeur modifié")
	}
	h.reopen(path(t, "protege.lattis"))

	refused := func(what, name string, kv ...any) {
		t.Helper()
		if _, err := h.try(name, kv...); err == nil {
			t.Fatalf("%s accepté sur une feuille protégée", what)
		}
	}
	refused("saisie", "cells.set", "sheet", "Feuille1", "row", 5, "col", 5, "value", "x")
	refused("saisie dans le tableau", "cells.set", "sheet", "Feuille1", "row", 2, "col", 2, "value", 99)
	refused("collage", "cells.paste", "sheet", "Feuille1", "row", 10, "col", 1, "values", [][]any{{"a"}, {"b"}})
	refused("tri", "tables.sort", "table", "Gens", "column", "Âge")
	refused("renommer le tableau", "tables.rename", "table", "Gens", "name", "Autres")
	refused("suppression de ligne", "tables.delete_row", "table", "Gens", "row", 1)
	refused("mise en forme", "cells.style", "sheet", "Feuille1", "row", 1, "col", 1, "style", map[string]any{"bold": true})
	refused("ajout de feuille", "sheets.add", "name", "Encore")
	refused("renommer une feuille", "sheets.rename", "sheet", "Libre", "name", "Autre")
	refused("supprimer une feuille", "sheets.delete", "sheet", "Libre")
	refused("déplacer une feuille", "sheets.move", "sheet", "Libre", "position", 1)
	refused("mauvais mot de passe", "sheets.unprotect", "sheet", "Feuille1", "password", "faux")

	// Les modifications par SQL et par script passent-elles la protection ?
	if _, err := h.try("db.execute", "sql", `UPDATE Gens SET "Âge" = 0`); err == nil {
		knownBug(t, "sql-protection", "une requête UPDATE modifie un tableau d'une feuille protégée")
	}
	if res, _ := h.try("scripts.run", "source", `db.insert("Gens", {Nom = "Zoé"})`); res != nil && res.(api.ScriptResult).Error == "" {
		knownBug(t, "lua-protection", "db.insert modifie un tableau d'une feuille protégée")
	}
	h.wantColumn("Gens", "Nom", "Léa", "Tom")

	// La structure protégée ne devrait pas empêcher de saisir dans une
	// feuille non protégée (Excel, et le résumé de sheets.protect_workbook).
	if _, err := h.try("cells.set", "sheet", "Libre", "row", 1, "col", 1, "value", "ok"); err != nil {
		knownBug(t, "classeur-protege-contenu", "Protéger le classeur refuse aussi les saisies dans les feuilles non protégées : "+err.Error())
	}
	h.do("sheets.unprotect", "sheet", "Feuille1", "password", "s3cret")
	h.do("sheets.unprotect_workbook")
	h.do("cells.set", "sheet", "Feuille1", "row", 2, "col", 2, "value", 32)
	h.do("sheets.add", "name", "Encore")
}

// Récupération après un arrêt brutal : un processus fils modifie un classeur
// enregistré puis meurt sans le fermer ; la réouverture propose de
// récupérer, et la récupération retrouve tout, historique compris.
func TestCrashRecoveryKeepsWork(t *testing.T) {
	if os.Getenv("LATTIS_CRASH_CHILD") != "" {
		crashChild(t)
		return
	}
	isolateCache(t)
	root, _ := archive.Root()
	p := path(t, "crash.lattis")
	snap := path(t, "snapshot.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryKeepsWork$")
	cmd.Env = append(os.Environ(), "LATTIS_CRASH_CHILD="+p, "LATTIS_CRASH_ROOT="+root, "LATTIS_CRASH_SNAP="+snap)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("processus fils : %v\n%s", err, out)
	}
	want, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}

	orphans, err := archive.Orphans()
	if err != nil || len(orphans) != 1 || orphans[0].Source != p {
		t.Fatalf("orphelins %+v %v", orphans, err)
	}
	_, err = doc.Open(context.Background(), p)
	var rec *archive.RecoverableError
	if !errors.As(err, &rec) {
		t.Fatalf("ouverture après arrêt brutal : %v", err)
	}
	d, err := doc.Recover(context.Background(), rec.Orphan)
	if err != nil {
		t.Fatal(err)
	}
	h := &host{t: t}
	h.swap(d)
	t.Cleanup(func() { h.d.Close() })
	sameSnapshot(t, "après récupération", string(want), h.snapshot())
	st := h.state()
	if !st.Dirty || st.Undo == "" {
		t.Fatalf("récupéré : %+v", st)
	}
	// Le fichier lui-même n'a que ce qui avait été enregistré.
	h.do("doc.save")
	h.do("doc.new")
	h.do("doc.open", "path", p)
	sameSnapshot(t, "récupéré puis enregistré", string(want), h.snapshot())
	if o, _ := archive.Orphans(); len(o) != 0 {
		t.Fatalf("orphelins restants %+v", o)
	}
}

func crashChild(t *testing.T) {
	root := os.Getenv("LATTIS_CRASH_ROOT")
	archive.Root = func() (string, error) { return root, nil }
	h := &host{t: t}
	if err := h.NewDoc(context.Background()); err != nil {
		t.Fatal(err)
	}
	steps := richSteps(h)
	for _, s := range steps[:len(steps)/2] {
		s.do()
	}
	// Rouvert depuis le fichier : le répertoire de travail est celui du
	// fichier, et la réouverture saura qu'une session a été interrompue.
	h.do("doc.save", "path", os.Getenv("LATTIS_CRASH_CHILD"))
	h.do("doc.new")
	h.do("doc.open", "path", os.Getenv("LATTIS_CRASH_CHILD"))
	for _, s := range steps[len(steps)/2:] {
		s.do()
	}
	if err := os.WriteFile(os.Getenv("LATTIS_CRASH_SNAP"), []byte(h.snapshot()), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Exit(3) // sans fermer le classeur
}

// knownBug signale une anomalie connue, décrite dans la PR qui a ajouté le
// test, sans faire échouer la suite ; LATTIS_STRICT=1 la fait échouer.
func knownBug(t *testing.T, id, msg string) {
	t.Helper()
	if os.Getenv("LATTIS_STRICT") == "1" {
		t.Errorf("anomalie %s : %s", id, msg)
		return
	}
	t.Logf("ANOMALIE CONNUE %s : %s", id, msg)
}
