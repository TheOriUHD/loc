package picker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTabCompletesCommonPrefix(test *testing.T) {
	home := test.TempDir()
	cwd := home
	model := newModelWithEnv("", home, cwd)
	current := filepath.Join(home, "Doc")
	model.input.SetValue(current)
	model.candidates = []candidate{
		{Path: filepath.Join(home, "Documents"), Display: "~/Documents"},
		{Path: filepath.Join(home, "Documentary"), Display: "~/Documentary"},
	}
	model.selected = 0

	_ = model.completeTab()

	expected := filepath.Join(home, "Document")
	if model.input.Value() != expected {
		test.Fatalf("expected %q, got %q", expected, model.input.Value())
	}
}

func TestFuzzyMatchOrdering(test *testing.T) {
	home := test.TempDir()
	cwd := home
	lineCounter := filepath.Join(home, "Documents", "LineCounter")
	paths := []string{
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "Desktop"),
		lineCounter,
	}

	ranked := rankCandidates("lc", paths, home, cwd)
	if len(ranked) == 0 || ranked[0].Path != lineCounter {
		test.Fatalf("expected LineCounter first, got %+v", ranked)
	}
}

func TestRecordHistoryDedupesAndCaps(test *testing.T) {
	historyFile := filepath.Join(test.TempDir(), "history")
	test.Setenv(historyFileEnv, historyFile)
	configuredHistoryFile, err := historyPath()
	if err != nil {
		test.Fatal(err)
	}
	if configuredHistoryFile != historyFile {
		test.Fatalf("expected isolated history file %q, got %q", historyFile, configuredHistoryFile)
	}
	first := filepath.Join(test.TempDir(), "first")
	second := filepath.Join(test.TempDir(), "second")

	if err := recordHistoryAt(historyFile, first); err != nil {
		test.Fatal(err)
	}
	if err := recordHistoryAt(historyFile, second); err != nil {
		test.Fatal(err)
	}
	if err := recordHistoryAt(historyFile, first); err != nil {
		test.Fatal(err)
	}

	paths, err := readHistoryAt(historyFile)
	if err != nil {
		test.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != first || paths[1] != second {
		test.Fatalf("unexpected deduped history: %+v", paths)
	}

	for index := 0; index < historyLimit+10; index++ {
		path := filepath.Join(test.TempDir(), fmt.Sprintf("path-%02d", index))
		if err := recordHistoryAt(historyFile, path); err != nil {
			test.Fatal(err)
		}
	}

	paths, err = readHistoryAt(historyFile)
	if err != nil {
		test.Fatal(err)
	}
	if len(paths) != historyLimit {
		test.Fatalf("expected capped history length %d, got %d", historyLimit, len(paths))
	}
}

func TestCleanHistoryDropsTempTestAndMissingPaths(test *testing.T) {
	historyFile := filepath.Join(test.TempDir(), "history")
	test.Setenv(historyFileEnv, historyFile)

	valid, err := os.Getwd()
	if err != nil {
		test.Fatal(err)
	}
	invalidTestPath := filepath.Join(test.TempDir(), "T", "TestFixture")
	if err := os.MkdirAll(invalidTestPath, 0o700); err != nil {
		test.Fatal(err)
	}
	missing := filepath.Join(test.TempDir(), "missing")
	content := fmt.Sprintf("%s\n/tmp/loc-fixture\n/private/var/folders/abc\n%s\n%s\n", valid, invalidTestPath, missing)
	if err := os.MkdirAll(filepath.Dir(historyFile), 0o700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(historyFile, []byte(content), 0o600); err != nil {
		test.Fatal(err)
	}

	if err := CleanHistory(); err != nil {
		test.Fatal(err)
	}
	paths, err := readHistoryAt(historyFile)
	if err != nil {
		test.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != filepath.Clean(valid) {
		test.Fatalf("unexpected cleaned history: %+v", paths)
	}
}

func TestShouldRecordRejectsTestsAndTempPaths(test *testing.T) {
	valid, err := os.Getwd()
	if err != nil {
		test.Fatal(err)
	}
	if shouldRecordPath(valid, true) {
		test.Fatal("expected testing mode to disable history recording")
	}
	if !shouldRecordPath(valid, false) {
		test.Fatalf("expected existing workspace path to be recordable: %s", valid)
	}
	if shouldRecordPath(test.TempDir(), false) {
		test.Fatal("expected temp directory to be rejected")
	}
	if shouldRecordPath(filepath.Join(test.TempDir(), "T", "TestFixture"), false) {
		test.Fatal("expected Go test fixture path to be rejected")
	}
}

func TestRecordHistorySkipsDuringTests(test *testing.T) {
	historyFile := filepath.Join(test.TempDir(), "history")
	test.Setenv(historyFileEnv, historyFile)
	valid, err := os.Getwd()
	if err != nil {
		test.Fatal(err)
	}
	if err := RecordHistory(valid); err != nil {
		test.Fatal(err)
	}
	paths, err := readHistoryAt(historyFile)
	if err != nil {
		test.Fatal(err)
	}
	if len(paths) != 0 {
		test.Fatalf("expected no test-time history writes, got %+v", paths)
	}
}
