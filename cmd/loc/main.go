package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"

	"golang.org/x/term"

	"github.com/philippb/loc/internal/counter"
	"github.com/philippb/loc/internal/picker"
	"github.com/philippb/loc/internal/report"
	"github.com/philippb/loc/internal/tui"
	"github.com/philippb/loc/internal/walker"
)

var version = "dev"

type cliOptions struct {
	path          string
	include       string
	exclude       string
	hasPath       bool
	json          bool
	legacy        bool
	noInteractive bool
	jobs          int
	profile       string
	showVersion   bool
}

func main() {
	_ = picker.CleanHistory()
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWithTerminal(args, stdout, stderr, term.IsTerminal(int(os.Stdin.Fd())))
}

func runWithTerminal(args []string, stdout, stderr io.Writer, stdinIsTTY bool) error {
	options, err := parseFlags(args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if options.showVersion {
		fmt.Fprintf(stdout, "loc %s\n", version)
		return nil
	}

	filter, err := buildFilter(options.include, options.exclude)
	if err != nil {
		return err
	}

	if options.json || options.legacy || options.noInteractive || !stdinIsTTY {
		root, err := normalizeDir(options.path)
		if err != nil {
			return err
		}
		return runStatic(root, filter, options, stdout, stderr)
	}

	root := ""
	if options.hasPath {
		root, err = normalizeDir(options.path)
		if err != nil {
			return err
		}
	}
	return runInteractive(root, options.hasPath, filter, options.jobs, options.profile)
}

func runStatic(root string, filter walker.Options, options cliOptions, stdout, stderr io.Writer) error {
	finishProfile, err := startProfile(options.profile)
	if err != nil {
		return err
	}

	totals, err := counter.Scan(context.Background(), root, filter, options.jobs, nil)
	profileErr := finishProfile()
	if err != nil {
		return err
	}
	if profileErr != nil {
		return profileErr
	}

	if options.json {
		return report.WriteJSON(stdout, totals)
	}

	fmt.Fprintln(stdout, report.RenderTable(totals))
	_ = stderr
	return nil
}

func runInteractive(root string, hasRoot bool, filter walker.Options, jobs int, profile string) error {
	return tui.Run(tui.Config{
		Root:          root,
		HasRoot:       hasRoot,
		Version:       version,
		InitialFilter: filter,
		Scan: func(ctx context.Context, root string, filter walker.Options, onProgress func(counter.FileResult)) (counter.Totals, error) {
			finishProfile, err := startProfile(profile)
			if err != nil {
				return counter.Totals{}, err
			}
			totals, err := counter.Scan(ctx, root, filter, jobs, onProgress)
			profileErr := finishProfile()
			if err != nil {
				return totals, err
			}
			if profileErr != nil {
				return totals, profileErr
			}
			_ = picker.RecordHistory(root)
			return totals, nil
		},
	})
}

func parseFlags(args []string, output io.Writer) (cliOptions, error) {
	options := cliOptions{path: ".", jobs: defaultJobs()}
	flags := flag.NewFlagSet("loc", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.include, "include", "", "comma-separated language names to include")
	flags.StringVar(&options.exclude, "exclude", "", "comma-separated language names to exclude")
	flags.BoolVar(&options.json, "json", false, "print machine-readable JSON output")
	flags.BoolVar(&options.legacy, "legacy", false, "print the static table instead of opening the TUI")
	flags.BoolVar(&options.noInteractive, "no-interactive", false, "never prompt")
	flags.IntVar(&options.jobs, "jobs", defaultJobs(), "parallel worker count")
	flags.StringVar(&options.profile, "profile", "", "write pprof profile: cpu or mem")
	flags.BoolVar(&options.showVersion, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: loc [path] [flags]\n\n")
		fmt.Fprintf(flags.Output(), "Counts code, comment, and blank lines in a directory tree.\n\n")
		flags.PrintDefaults()
	}

	reordered, err := reorderFlagArgs(args)
	if err != nil {
		return options, err
	}

	if err := flags.Parse(reordered); err != nil {
		return options, err
	}

	if flags.NArg() > 1 {
		return options, fmt.Errorf("expected at most one path, got %d", flags.NArg())
	}
	if flags.NArg() == 1 {
		options.path = flags.Arg(0)
		options.hasPath = true
	}
	if options.jobs < 1 {
		return options, fmt.Errorf("--jobs must be at least 1")
	}
	return options, nil
}

func reorderFlagArgs(args []string) ([]string, error) {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, 1)

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			positionals = append(positionals, args[index+1:]...)
			break
		}

		if strings.HasPrefix(arg, "-") && arg != "-" {
			flagArgs = append(flagArgs, arg)
			if flagNeedsValue(arg) && !strings.Contains(arg, "=") {
				if index+1 >= len(args) {
					return nil, fmt.Errorf("%s requires a value", arg)
				}
				index++
				flagArgs = append(flagArgs, args[index])
			}
			continue
		}

		positionals = append(positionals, arg)
	}

	return append(flagArgs, positionals...), nil
}

func flagNeedsValue(arg string) bool {
	name := strings.TrimLeft(arg, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		name = before
	}

	switch name {
	case "include", "exclude", "jobs", "profile":
		return true
	default:
		return false
	}
}

func defaultJobs() int {
	jobs := runtime.NumCPU() * 2
	if jobs < 1 {
		return 1
	}
	return jobs
}

func startProfile(kind string) (func() error, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "":
		return func() error { return nil }, nil
	case "cpu":
		file, err := os.Create("loc-cpu.pprof")
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			_ = file.Close()
			return nil, err
		}
		return func() error {
			pprof.StopCPUProfile()
			return file.Close()
		}, nil
	case "mem":
		file, err := os.Create("loc-mem.pprof")
		if err != nil {
			return nil, err
		}
		return func() error {
			runtime.GC()
			writeErr := pprof.WriteHeapProfile(file)
			closeErr := file.Close()
			if writeErr != nil {
				return writeErr
			}
			return closeErr
		}, nil
	default:
		return nil, fmt.Errorf("--profile must be cpu or mem")
	}
}

func buildFilter(include, exclude string) (walker.Options, error) {
	includeValues := splitCSV(include)
	excludeValues := splitCSV(exclude)
	if len(includeValues) > 0 && len(excludeValues) > 0 {
		return walker.Options{}, fmt.Errorf("--include and --exclude are mutually exclusive")
	}
	if len(includeValues) > 0 {
		return walker.Options{Mode: walker.FilterInclude, Languages: walker.NormalizeLanguageSet(includeValues)}, nil
	}
	if len(excludeValues) > 0 {
		return walker.Options{Mode: walker.FilterExclude, Languages: walker.NormalizeLanguageSet(excludeValues)}, nil
	}
	return walker.Options{Mode: walker.FilterAll}, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func normalizeDir(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		trimmed = "."
	}

	expanded, err := expandHome(trimmed)
	if err != nil {
		return "", err
	}

	absPath, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absPath)
	}
	return absPath, nil
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}
