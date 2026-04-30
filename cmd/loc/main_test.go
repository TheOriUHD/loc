package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFlagsTracksPositionalPath(test *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		wantPath bool
	}{
		{name: "no args", wantPath: false},
		{name: "flag only", args: []string{"--profile", "cpu"}, wantPath: false},
		{name: "path", args: []string{"."}, wantPath: true},
		{name: "path after flag", args: []string{"--legacy", "."}, wantPath: true},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			options, err := parseFlags(testCase.args, io.Discard)
			if err != nil {
				test.Fatal(err)
			}
			if options.hasPath != testCase.wantPath {
				test.Fatalf("hasPath = %v, want %v", options.hasPath, testCase.wantPath)
			}
		})
	}
}

func TestNoInteractiveSkipsWizardEvenWhenTerminal(test *testing.T) {
	directory := test.TempDir()
	writeFile(test, filepath.Join(directory, "main.go"), "package main\n")
	chdir(test, directory)

	var stdout bytes.Buffer
	err := runWithTerminal([]string{"--no-interactive", "--json"}, &stdout, io.Discard, true)
	if err != nil {
		test.Fatal(err)
	}

	if !bytes.Contains(stdout.Bytes(), []byte(`"language": "Go"`)) {
		test.Fatalf("expected JSON scan output, got %s", stdout.String())
	}
}

func TestNonTTYNoArgsSkipsWizard(test *testing.T) {
	directory := test.TempDir()
	writeFile(test, filepath.Join(directory, "main.go"), "package main\n")
	chdir(test, directory)

	var stdout bytes.Buffer
	err := runWithTerminal(nil, &stdout, io.Discard, false)
	if err != nil {
		test.Fatal(err)
	}

	if !bytes.Contains(stdout.Bytes(), []byte("Go")) {
		test.Fatalf("expected table scan output, got %s", stdout.String())
	}
}

func writeFile(test *testing.T, path string, contents string) {
	test.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		test.Fatal(err)
	}
}

func chdir(test *testing.T, path string) {
	test.Helper()
	previous, err := os.Getwd()
	if err != nil {
		test.Fatal(err)
	}
	if err := os.Chdir(path); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			test.Fatal(err)
		}
	})
}
