package counter

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theoriuhd/loc/internal/lang"
)

func TestCountReaderTracksBlockStartedAfterCode(test *testing.T) {
	source := strings.Join([]string{
		"package main",
		"func main() {",
		"value := 1 /* open",
		"comment only",
		"*/",
		"}",
	}, "\n")

	counts, err := countReader(bufio.NewReader(strings.NewReader(source)), lang.Detect("main.go").Rule)
	if err != nil {
		test.Fatal(err)
	}

	if counts.Code != 4 || counts.Comments != 2 || counts.Blanks != 0 || counts.Total != 6 {
		test.Fatalf("unexpected counts: %+v", counts)
	}
}

func TestCountReaderHandlesPythonTripleQuotes(test *testing.T) {
	source := strings.Join([]string{
		"'''docstring",
		"still a comment",
		"'''",
		"print('hello')",
	}, "\n")

	counts, err := countReader(bufio.NewReader(strings.NewReader(source)), lang.Detect("script.py").Rule)
	if err != nil {
		test.Fatal(err)
	}

	if counts.Code != 1 || counts.Comments != 3 || counts.Blanks != 0 || counts.Total != 4 {
		test.Fatalf("unexpected counts: %+v", counts)
	}
}

func TestCountFileHandlesOneMegabyteLine(test *testing.T) {
	directory := test.TempDir()
	path := filepath.Join(directory, "bundle.js")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 1<<20)), 0o600); err != nil {
		test.Fatal(err)
	}

	result := CountFile(path)
	if result.Skipped {
		test.Fatalf("did not expect long-line file to be skipped: %+v", result)
	}
	if result.Counts.Code != 1 || result.Counts.Total != 1 {
		test.Fatalf("unexpected counts: %+v", result.Counts)
	}
}

func TestCountReaderHandlesMixedCommentCodeLines(test *testing.T) {
	source := strings.Join([]string{
		"value := 1 // trailing comment",
		"// full-line comment",
		"/* block */ value := 2",
		"",
	}, "\n") + "\n"

	counts, err := countReader(bufio.NewReader(strings.NewReader(source)), lang.Detect("main.go").Rule)
	if err != nil {
		test.Fatal(err)
	}

	if counts.Code != 2 || counts.Comments != 1 || counts.Blanks != 1 || counts.Total != 4 {
		test.Fatalf("unexpected counts: %+v", counts)
	}
}

func TestCountFileSkipsBinaryFiles(test *testing.T) {
	directory := test.TempDir()
	path := filepath.Join(directory, "payload.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o600); err != nil {
		test.Fatal(err)
	}

	result := CountFile(path)
	if !result.Skipped || result.SkipReason != SkipBinary {
		test.Fatalf("expected binary skip, got %+v", result)
	}
}
