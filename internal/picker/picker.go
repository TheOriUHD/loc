package picker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

const (
	historyLimit = 50
	cacheLimit   = 8
)

var ErrCanceled = errors.New("path selection canceled")

type Model struct {
	input      textinput.Model
	candidates []candidate
	selected   int
	chosen     string
	canceled   bool
	flash      bool
	standalone bool

	home    string
	cwd     string
	width   int
	height  int
	history []string
	cache   *listingCache
}

type candidate struct {
	Path    string
	Display string
	Matches []int
}

type listingMsg struct {
	dir     string
	entries []string
	err     error
}

type clearFlashMsg struct{}

type SelectedMsg struct {
	Path string
}

type CancelledMsg struct{}

func Pick(initial string) (string, error) {
	model := New(initial)
	model.standalone = true

	final, err := tea.NewProgram(model).Run()
	if err != nil {
		return "", err
	}

	result, ok := final.(Model)
	if !ok || result.canceled {
		return "", ErrCanceled
	}
	if result.chosen == "" {
		return "", ErrCanceled
	}
	return result.chosen, nil
}

func New(initial string) Model {
	home, err := os.UserHomeDir()
	if err != nil {
		home = string(os.PathSeparator)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = home
	}
	return newModelWithEnv(initial, home, cwd)
}

func newModel(initial string) (Model, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Model{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Model{}, err
	}
	return newModelWithEnv(initial, home, cwd), nil
}

func newModelWithEnv(initial, home, cwd string) Model {
	input := textinput.New()
	input.Focus()
	input.Prompt = ""
	input.Width = 46
	if strings.TrimSpace(initial) != "" {
		input.SetValue(abbreviatePath(expandPath(initial, home, cwd), home))
	}

	model := Model{
		input:    input,
		selected: 0,
		home:     filepath.Clean(home),
		cwd:      filepath.Clean(cwd),
		history:  readHistory(),
		cache:    newListingCache(cacheLimit),
	}
	model.refreshCandidates()
	return model
}

func (model Model) Init() tea.Cmd {
	return model.queueListings(model.queryDirs())
}

func (model *Model) SetSize(width, height int) {
	model.width = width
	model.height = height
}

func (model Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			model.canceled = true
			return model, func() tea.Msg { return CancelledMsg{} }
		case "enter":
			return model, model.confirm()
		case "up", "shift+tab":
			model.moveSelection(-1)
			return model, nil
		case "down":
			model.moveSelection(1)
			return model, nil
		case "tab":
			return model, model.completeTab()
		}

		before := model.input.Value()
		var cmd tea.Cmd
		model.input, cmd = model.input.Update(msg)
		if model.input.Value() != before {
			model.flash = false
			return model, tea.Batch(cmd, model.refreshCandidates())
		}
		return model, cmd

	case listingMsg:
		if msg.err == nil {
			model.cache.set(msg.dir, msg.entries)
		}
		return model, model.refreshCandidates()

	case clearFlashMsg:
		model.flash = false
		return model, nil

	case SelectedMsg:
		model.chosen = msg.Path
		if model.standalone {
			return model, tea.Quit
		}
		return model, nil

	case CancelledMsg:
		model.canceled = true
		if model.standalone {
			return model, tea.Quit
		}
		return model, nil
	}

	return model, nil
}

func (model Model) View() string {
	screenWidth := model.width
	if screenWidth <= 0 {
		screenWidth = 80
	}
	screenHeight := model.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	panelWidth := screenWidth - 2
	if panelWidth < 24 {
		panelWidth = screenWidth
	}
	panelHeight := screenHeight - 2
	if panelHeight < 8 {
		panelHeight = screenHeight
	}
	if panelWidth < 1 {
		panelWidth = 1
	}
	if panelHeight < 1 {
		panelHeight = 1
	}

	contentWidth := panelWidth - 4
	if contentWidth < 12 {
		contentWidth = panelWidth - 2
	}
	if contentWidth < 1 {
		contentWidth = 1
	}
	contentHeight := panelHeight - 2
	if contentHeight < 3 {
		contentHeight = 3
	}
	listHeight := contentHeight - 3
	if listHeight < 1 {
		listHeight = 1
	}

	borderStyle := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(panelWidth).Height(panelHeight)
	labelStyle := lipgloss.NewStyle().Bold(true)
	inputStyle := lipgloss.NewStyle()
	if model.flash {
		inputStyle = inputStyle.Foreground(lipgloss.Color("9"))
	}
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", contentWidth))

	input := model.input
	input.Width = maxInt(1, contentWidth-lipgloss.Width("Search: "))

	lines := []string{labelStyle.Render("Search: ") + inputStyle.Render(input.View()), divider}
	visible := model.visibleCandidatesWithLimit(listHeight)
	for index, item := range visible {
		prefix := "  "
		if index == model.selected {
			prefix = "❯ "
		}
		lines = append(lines, prefix+renderCandidate(item, maxInt(1, contentWidth-lipgloss.Width(prefix))))
	}
	for len(lines) < listHeight+2 {
		lines = append(lines, "")
	}

	help := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Tab: complete   ↑↓: navigate   Enter: select   Esc: cancel")
	lines = append(lines, truncateRight(help, contentWidth))
	return lipgloss.Place(screenWidth, screenHeight, lipgloss.Center, lipgloss.Center, borderStyle.Render(strings.Join(lines, "\n")))
}

func (model *Model) refreshCandidates() tea.Cmd {
	dirs := model.queryDirs()
	paths := []string{model.home, model.cwd}
	paths = append(paths, model.history...)
	paths = append(paths, model.ancestorPaths()...)

	for _, dir := range dirs {
		entries, ok := model.cache.get(dir)
		if ok {
			paths = append(paths, entries...)
		}
	}

	model.candidates = rankCandidates(model.input.Value(), paths, model.home, model.cwd)
	if len(model.candidates) == 0 {
		model.selected = -1
	} else if model.selected < 0 || model.selected >= len(model.visibleCandidates()) {
		model.selected = 0
	}

	return model.queueListings(dirs)
}

func (model Model) queryDirs() []string {
	dirs := []string{model.cwd, model.home}
	typed := expandPath(model.input.Value(), model.home, model.cwd)
	if typed == "" {
		return uniquePaths(dirs)
	}

	if info, err := os.Stat(typed); err == nil && info.IsDir() {
		dirs = append(dirs, typed)
	}

	parent := typed
	if !strings.HasSuffix(model.input.Value(), string(os.PathSeparator)) {
		parent = filepath.Dir(typed)
	}
	if dirExists(parent) {
		dirs = append(dirs, parent)
	}

	for _, ancestor := range ancestors(typed) {
		if dirExists(ancestor) {
			dirs = append(dirs, ancestor)
		}
	}

	return uniquePaths(dirs)
}

func (model Model) ancestorPaths() []string {
	return ancestors(expandPath(model.input.Value(), model.home, model.cwd))
}

func (model Model) queueListings(dirs []string) tea.Cmd {
	commands := make([]tea.Cmd, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, ok := model.cache.get(dir); ok {
			continue
		}
		commands = append(commands, listDirCmd(dir))
	}
	return tea.Batch(commands...)
}

func listDirCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := listDirectories(dir)
		return listingMsg{dir: dir, entries: entries, err: err}
	}
}

func listDirectories(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (model *Model) completeTab() tea.Cmd {
	if len(model.candidates) == 0 {
		return nil
	}

	current := model.input.Value()
	completionValues := make([]string, 0, len(model.candidates))
	for _, item := range model.candidates {
		completionValues = append(completionValues, completionText(item.Path, model.home, current))
	}

	if len(completionValues) == 1 && completionValues[0] != current {
		model.input.SetValue(completionValues[0])
		model.input.CursorEnd()
		return model.refreshCandidates()
	}

	if prefix := commonPrefix(completionValues); len(prefix) > len(current) && strings.HasPrefix(prefix, current) {
		model.input.SetValue(prefix)
		model.input.CursorEnd()
		return model.refreshCandidates()
	}

	if selected := model.selectedCandidate(); selected != nil {
		completion := completionText(selected.Path, model.home, current)
		if completion != current {
			model.input.SetValue(completion)
			model.input.CursorEnd()
			return model.refreshCandidates()
		}
	}

	model.moveSelection(1)
	return nil
}

func (model *Model) confirm() tea.Cmd {
	choice := model.input.Value()
	if selected := model.selectedCandidate(); selected != nil {
		choice = selected.Path
	}

	path := expandPath(choice, model.home, model.cwd)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		model.flash = true
		return func() tea.Msg {
			time.Sleep(160 * time.Millisecond)
			return clearFlashMsg{}
		}
	}

	model.chosen = filepath.Clean(path)
	return func() tea.Msg { return SelectedMsg{Path: model.chosen} }
}

func (model *Model) moveSelection(delta int) {
	visibleCount := len(model.visibleCandidates())
	if visibleCount == 0 {
		model.selected = -1
		return
	}
	model.selected = (model.selected + delta + visibleCount) % visibleCount
}

func (model Model) selectedCandidate() *candidate {
	visible := model.visibleCandidates()
	if model.selected < 0 || model.selected >= len(visible) {
		return nil
	}
	return &visible[model.selected]
}

func (model Model) visibleCandidates() []candidate {
	return model.visibleCandidatesWithLimit(model.visibleCandidateLimit())
}

func (model Model) visibleCandidatesWithLimit(limit int) []candidate {
	if limit < 1 {
		limit = 1
	}
	if len(model.candidates) <= limit {
		return model.candidates
	}
	return model.candidates[:limit]
}

func (model Model) visibleCandidateLimit() int {
	if model.height <= 0 {
		return 10
	}
	limit := model.height - 7
	if limit < 1 {
		return 1
	}
	return limit
}

func rankCandidates(query string, paths []string, home, cwd string) []candidate {
	paths = uniquePaths(paths)
	query = strings.TrimSpace(query)
	if query == "" {
		return buildPlainCandidates(paths, home)
	}

	queryLower := strings.ToLower(query)
	expandedLower := strings.ToLower(expandPath(query, home, cwd))
	seen := make(map[string]struct{}, len(paths))
	var ranked []candidate

	for _, path := range paths {
		display := abbreviatePath(path, home)
		if strings.HasPrefix(strings.ToLower(display), queryLower) || strings.HasPrefix(strings.ToLower(path), expandedLower) {
			ranked = append(ranked, candidate{Path: path, Display: display})
			seen[path] = struct{}{}
		}
	}

	displays := make([]string, 0, len(paths))
	indexToPath := make([]string, 0, len(paths))
	for _, path := range paths {
		displays = append(displays, abbreviatePath(path, home))
		indexToPath = append(indexToPath, path)
	}

	for _, match := range fuzzy.Find(query, displays) {
		path := indexToPath[match.Index]
		if _, ok := seen[path]; ok {
			continue
		}
		ranked = append(ranked, candidate{Path: path, Display: match.Str, Matches: match.MatchedIndexes})
		seen[path] = struct{}{}
	}

	if len(ranked) == 0 {
		return buildPlainCandidates(paths, home)
	}
	return ranked
}

func buildPlainCandidates(paths []string, home string) []candidate {
	candidates := make([]candidate, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, candidate{Path: path, Display: abbreviatePath(path, home)})
	}
	return candidates
}

func renderCandidate(item candidate, width int) string {
	display, offset := truncateLeft(item.Display, width)
	if len(item.Matches) == 0 {
		return display
	}

	matched := make(map[int]struct{}, len(item.Matches))
	for _, index := range item.Matches {
		shifted := index - offset
		if offset > 0 {
			shifted++
		}
		if shifted >= 0 {
			matched[shifted] = struct{}{}
		}
	}

	bold := lipgloss.NewStyle().Bold(true)
	var builder strings.Builder
	for index, char := range display {
		piece := string(char)
		if _, ok := matched[index]; ok {
			builder.WriteString(bold.Render(piece))
			continue
		}
		builder.WriteString(piece)
	}
	return builder.String()
}

func truncateLeft(value string, width int) (string, int) {
	if width <= 0 {
		return "", 0
	}
	if lipgloss.Width(value) <= width {
		return value, 0
	}
	runes := []rune(value)
	if width == 1 {
		return "…", len(runes)
	}
	tailWidth := width - 1
	start := len(runes)
	currentWidth := 0
	for start > 0 {
		nextWidth := lipgloss.Width(string(runes[start-1]))
		if currentWidth+nextWidth > tailWidth {
			break
		}
		start--
		currentWidth += nextWidth
	}
	return "…" + string(runes[start:]), start
}

func truncateRight(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	if width == 1 {
		return "…"
	}
	var builder strings.Builder
	currentWidth := 0
	for _, char := range runes {
		charWidth := lipgloss.Width(string(char))
		if currentWidth+charWidth > width-1 {
			break
		}
		builder.WriteRune(char)
		currentWidth += charWidth
	}
	builder.WriteString("…")
	return builder.String()
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func expandPath(path, home, cwd string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if trimmed == "~" {
		return filepath.Clean(home)
	}
	if strings.HasPrefix(trimmed, "~/") {
		return filepath.Clean(filepath.Join(home, strings.TrimPrefix(trimmed, "~/")))
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(filepath.Join(cwd, trimmed))
}

func abbreviatePath(path, home string) string {
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)
	if cleanPath == cleanHome {
		return "~"
	}
	if strings.HasPrefix(cleanPath, cleanHome+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(cleanPath, cleanHome)
	}
	return cleanPath
}

func completionText(path, home, current string) string {
	if strings.HasPrefix(strings.TrimSpace(current), "~") {
		return abbreviatePath(path, home)
	}
	return path
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func ancestors(path string) []string {
	if path == "" {
		return nil
	}
	var values []string
	current := filepath.Clean(path)
	for {
		parent := filepath.Dir(current)
		if parent == current || parent == "." {
			break
		}
		values = append(values, parent)
		current = parent
	}
	return values
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		cleanPath := filepath.Clean(path)
		if _, ok := seen[cleanPath]; ok {
			continue
		}
		seen[cleanPath] = struct{}{}
		unique = append(unique, cleanPath)
	}
	return unique
}

type listingCache struct {
	limit  int
	values map[string][]string
	order  []string
}

func newListingCache(limit int) *listingCache {
	return &listingCache{limit: limit, values: make(map[string][]string)}
}

func (cache *listingCache) get(dir string) ([]string, bool) {
	dir = filepath.Clean(dir)
	entries, ok := cache.values[dir]
	if !ok {
		return nil, false
	}
	cache.touch(dir)
	return entries, true
}

func (cache *listingCache) set(dir string, entries []string) {
	dir = filepath.Clean(dir)
	cache.values[dir] = entries
	cache.touch(dir)
	for len(cache.order) > cache.limit {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		delete(cache.values, oldest)
	}
}

func (cache *listingCache) touch(dir string) {
	for index, value := range cache.order {
		if value == dir {
			copy(cache.order[index:], cache.order[index+1:])
			cache.order = cache.order[:len(cache.order)-1]
			break
		}
	}
	cache.order = append(cache.order, dir)
}

func (model Model) String() string {
	return fmt.Sprintf("picker.Model(%q)", model.input.Value())
}
