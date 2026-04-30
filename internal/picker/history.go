package picker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const historyFileEnv = "LOC_HISTORY_FILE"

func CleanHistory() error {
	historyFile, err := historyPath()
	if err != nil {
		return err
	}
	paths, err := readHistoryAt(historyFile)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}

	cleaned := make([]string, 0, len(paths))
	for _, path := range paths {
		if shouldDropHistoryPath(path) {
			continue
		}
		cleaned = append(cleaned, path)
		if len(cleaned) == historyLimit {
			break
		}
	}
	return writeHistoryAt(historyFile, cleaned)
}

func RecordHistory(path string) error {
	if !shouldRecord(path) {
		return nil
	}
	historyFile, err := historyPath()
	if err != nil {
		return err
	}
	return recordHistoryAt(historyFile, path)
}

func shouldRecord(path string) bool {
	return shouldRecordPath(path, testing.Testing())
}

func shouldRecordPath(path string, testingMode bool) bool {
	if testingMode {
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absolute = filepath.Clean(absolute)
	if shouldDropHistoryPath(absolute) {
		return false
	}
	info, err := os.Stat(absolute)
	return err == nil && info.IsDir()
}

func shouldDropHistoryPath(path string) bool {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return true
	}
	if strings.HasPrefix(cleanPath, "/private/var/folders/") || strings.HasPrefix(cleanPath, "/var/folders/") || strings.HasPrefix(cleanPath, "/tmp/") {
		return true
	}
	if strings.Contains(cleanPath, "/T/Test") {
		return true
	}
	tempDir := filepath.Clean(os.TempDir())
	if cleanPath == tempDir || strings.HasPrefix(cleanPath, tempDir+string(os.PathSeparator)) {
		return true
	}
	info, err := os.Stat(cleanPath)
	return err != nil || !info.IsDir()
}

func historyPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(historyFileEnv)); override != "" {
		return override, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "loc", "history"), nil
}

func readHistory() []string {
	historyFile, err := historyPath()
	if err != nil {
		return nil
	}
	paths, _ := readHistoryAt(historyFile)
	return paths
}

func readHistoryAt(historyFile string) ([]string, error) {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		path := strings.TrimSpace(line)
		if path != "" {
			paths = append(paths, filepath.Clean(path))
		}
	}
	return paths, nil
}

func recordHistoryAt(historyFile, path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absolute = filepath.Clean(absolute)

	paths, err := readHistoryAt(historyFile)
	if err != nil {
		return err
	}

	updated := []string{absolute}
	for _, existing := range paths {
		if existing != absolute {
			updated = append(updated, existing)
		}
		if len(updated) == historyLimit {
			break
		}
	}
	return writeHistoryAt(historyFile, updated)
}

func writeHistoryAt(historyFile string, paths []string) error {
	if err := os.MkdirAll(filepath.Dir(historyFile), 0o700); err != nil {
		return err
	}
	content := ""
	if len(paths) > 0 {
		content = strings.Join(paths, "\n") + "\n"
	}
	return os.WriteFile(historyFile, []byte(content), 0o600)
}
