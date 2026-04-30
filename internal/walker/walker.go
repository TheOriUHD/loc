package walker

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/philippb/loc/internal/lang"
)

var defaultIgnoredDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	"dist":         {},
	"build":        {},
	"target":       {},
	"__pycache__":  {},
	".venv":        {},
	"venv":         {},
	".next":        {},
	"out":          {},
	".cache":       {},
	".idea":        {},
	".vscode":      {},
}

type FilterMode int

const (
	FilterAll FilterMode = iota
	FilterInclude
	FilterExclude
)

type Options struct {
	Mode      FilterMode
	Languages map[string]struct{}
}

type IssueReason string

const (
	IssuePermission IssueReason = "permission"
	IssueOther      IssueReason = "other"
)

type Issue struct {
	Path   string
	Reason IssueReason
	Err    error
}

func Walk(ctx context.Context, root string, options Options, files chan<- string, issues chan<- Issue, onDispatch func(string)) {
	defer close(files)
	defer close(issues)

	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			issues <- Issue{Path: path, Reason: classifyWalkError(err), Err: err}
			return nil
		}

		if entry.IsDir() {
			if shouldSkipDir(path, root, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		if !languageAllowed(lang.Detect(path).Name, options) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case files <- path:
			if onDispatch != nil {
				onDispatch(path)
			}
			return nil
		}
	})
}

func PreScan(root string) ([]string, []Issue) {
	var paths []string
	var issues []Issue
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			issues = append(issues, Issue{Path: path, Reason: classifyWalkError(err), Err: err})
			return nil
		}

		if entry.IsDir() {
			if shouldSkipDir(path, root, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}

		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return paths, issues
}

func FilterPaths(paths []string, options Options) []string {
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if languageAllowed(lang.Detect(path).Name, options) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func NormalizeLanguageSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		language := strings.TrimSpace(value)
		if language == "" {
			continue
		}
		set[strings.ToLower(language)] = struct{}{}
	}
	return set
}

func languageAllowed(language string, options Options) bool {
	if options.Mode == FilterAll || len(options.Languages) == 0 {
		return true
	}

	_, selected := options.Languages[strings.ToLower(language)]
	if options.Mode == FilterInclude {
		return selected
	}
	return !selected
}

func shouldSkipDir(path, root, name string) bool {
	if path == root {
		return false
	}
	_, skip := defaultIgnoredDirs[name]
	return skip
}

func classifyWalkError(err error) IssueReason {
	if errors.Is(err, os.ErrPermission) {
		return IssuePermission
	}
	return IssueOther
}
