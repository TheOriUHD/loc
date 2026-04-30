package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/theoriuhd/loc/internal/counter"
	"github.com/theoriuhd/loc/internal/walker"
)

func TestUnifiedModelStartsLoadingTransitionsToResultsAndQuits(test *testing.T) {
	model := New(Config{
		Root:          test.TempDir(),
		HasRoot:       true,
		InitialFilter: walker.Options{Mode: walker.FilterAll},
	})
	if model.screen != screenLoading {
		test.Fatalf("expected screenLoading, got %v", model.screen)
	}

	totals := counter.NewTotals()
	totals.Add(counter.FileResult{
		Path:     "main.go",
		Language: "Go",
		Counts:   counter.Counts{Files: 1, Code: 1, Total: 1},
	})

	updated, _ := model.Update(scanDoneMsg{totals: totals, completed: 1})
	model, ok := updated.(Model)
	if !ok {
		test.Fatalf("expected Model, got %T", updated)
	}
	if model.screen != screenResults {
		test.Fatalf("expected screenResults, got %v", model.screen)
	}

	_, cmd := model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'q'}}))
	if cmd == nil {
		test.Fatal("expected q to return tea.Quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		test.Fatalf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestMenuOpensFromMouseClick(test *testing.T) {
	model := readyTestModel(test)
	updated, _ := model.Update(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	model, ok := updated.(Model)
	if !ok {
		test.Fatalf("expected Model, got %T", updated)
	}
	if !model.menuOpen || model.openMenu != "File" {
		test.Fatalf("expected File menu open, got open=%v name=%q", model.menuOpen, model.openMenu)
	}
}

func TestMenuOpensFromAltMnemonic(test *testing.T) {
	model := readyTestModel(test)
	updated, _ := model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true}))
	model, ok := updated.(Model)
	if !ok {
		test.Fatalf("expected Model, got %T", updated)
	}
	if !model.menuOpen || model.openMenu != "File" {
		test.Fatalf("expected File menu open, got open=%v name=%q", model.menuOpen, model.openMenu)
	}

	updated, _ = model.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'i'}, Alt: true}))
	model, ok = updated.(Model)
	if !ok {
		test.Fatalf("expected Model, got %T", updated)
	}
	if !model.menuOpen || model.openMenu != "Filters" {
		test.Fatalf("expected Filters menu open, got open=%v name=%q", model.menuOpen, model.openMenu)
	}
}

func TestStatusAttributionUsesAvailableSpace(test *testing.T) {
	wide := readyTestModel(test)
	wide.width = 120
	view := wide.statusView()
	if !strings.Contains(view, statusAttributionText) {
		test.Fatalf("expected wide status bar to include attribution, got %q", view)
	}
	if !strings.Contains(view, "Made with ♥") {
		test.Fatalf("expected rendered status bar to preserve heart glyph, got %q", view)
	}
	if strings.Contains(view, misspelledUsername()) {
		test.Fatalf("expected corrected username in status bar, got %q", view)
	}

	narrow := readyTestModel(test)
	narrow.width = 70
	if view := narrow.statusView(); strings.Contains(view, statusAttributionText) {
		test.Fatalf("expected narrow status bar to drop attribution, got %q", view)
	}
}

func TestAboutModalIncludesCopyright(test *testing.T) {
	model := readyTestModel(test)
	model.openAboutModal()
	view := model.infoModalView()
	if !strings.Contains(view, aboutCopyright) {
		test.Fatalf("expected About modal to include copyright, got %q", view)
	}
	if !strings.Contains(view, aboutMadeWith) {
		test.Fatalf("expected About modal to include made-with attribution, got %q", view)
	}
	if !strings.Contains(view, aboutRepository) {
		test.Fatalf("expected About modal to include repository URL, got %q", view)
	}
	if !strings.Contains(view, "Made with ♥") {
		test.Fatalf("expected rendered About modal to preserve heart glyph, got %q", view)
	}
	if strings.Contains(view, misspelledUsername()) {
		test.Fatalf("expected corrected username in About modal, got %q", view)
	}
}

func misspelledUsername() string {
	return "theorig" + "inuhd"
}

func readyTestModel(test *testing.T) Model {
	test.Helper()
	model := New(Config{
		Root:          test.TempDir(),
		HasRoot:       true,
		InitialFilter: walker.Options{Mode: walker.FilterAll},
	})
	model.width = 100
	model.height = 32
	model.screen = screenResults
	model.menuHitboxes = buildMenuHitboxes()
	return model
}
