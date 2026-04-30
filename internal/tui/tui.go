package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/theoriuhd/loc/internal/counter"
	"github.com/theoriuhd/loc/internal/lang"
	"github.com/theoriuhd/loc/internal/picker"
	"github.com/theoriuhd/loc/internal/walker"
)

type ScanFunc func(context.Context, string, walker.Options, func(counter.FileResult)) (counter.Totals, error)

type Config struct {
	Root          string
	HasRoot       bool
	Version       string
	InitialFilter walker.Options
	ExpectedFiles int
	Scan          ScanFunc
}

func Run(config Config) error {
	program := tea.NewProgram(New(config), tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return err
	}

	model, ok := finalModel.(Model)
	if ok {
		return model.err
	}
	return nil
}

type screen int

const (
	screenPicker screen = iota
	screenFilter
	screenLoading
	screenResults
)

type sortField int

const (
	sortCode sortField = iota
	sortFiles
	sortComments
	sortBlanks
	sortTotal
	sortLanguage
)

const (
	aboutCopyright        = "© 2026 theoriuhd"
	aboutMadeWith         = "Made with ♥ by theoriuhd"
	aboutRepository       = "github.com/theoriuhd/loc"
	statusAttributionText = "Made with ♥ by theoriuhd · github.com/theoriuhd/loc"
)

type Model struct {
	screen  screen
	root    string
	home    string
	version string
	scan    ScanFunc
	err     error
	filter  walker.Options

	width  int
	height int

	picker       picker.Model
	filterScreen filterScreen

	spinner    spinner.Model
	progress   progress.Model
	progressCh chan progressMsg
	completed  int
	expected   int

	totals   counter.Totals
	rows     []languageRow
	selected int
	offset   int
	sortBy   sortField
	reverse  bool

	searchInput  textinput.Model
	searchActive bool
	filters      filterState
	status       string

	menuOpen     bool
	openMenu     string
	menuIndex    int
	menuItem     int
	menuHitboxes []menuHitbox
	modal        modalState
}

type filterScreen struct {
	detecting bool
	root      string
	paths     []string
	languages []string
	mode      walker.FilterMode
	selected  map[string]bool
	cursor    int
	status    string
}

type progressMsg struct {
	completed int
}

type scanDoneMsg struct {
	totals    counter.Totals
	err       error
	completed int
}

type preScanDoneMsg struct {
	root  string
	paths []string
	err   error
}

type languageRow struct {
	Language string         `json:"language"`
	Counts   counter.Counts `json:"counts"`
}

type filterState struct {
	Languages     map[string]bool
	PathPattern   string
	MinSizeKB     int
	HasCommentMin bool
	CommentMin    float64
	HasCommentMax bool
	CommentMax    float64
	MinLines      int
}

type modalKind int

const (
	modalNone modalKind = iota
	modalLanguages
	modalText
	modalInfo
	modalSkipped
	modalExport
)

type modalState struct {
	kind             modalKind
	title            string
	message          []string
	inputs           []textinput.Model
	activeInput      int
	languages        []string
	languageSelected map[string]bool
	cursor           int
	exportFormat     string
	apply            func(*Model) tea.Cmd
}

func New(config Config) Model {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = string(os.PathSeparator)
	}
	search := textinput.New()
	search.Prompt = "Search: "
	search.CharLimit = 120
	search.Width = 36

	spin := spinner.New()
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))

	model := Model{
		screen:      screenPicker,
		root:        filepath.Clean(config.Root),
		home:        filepath.Clean(home),
		version:     config.Version,
		scan:        config.Scan,
		filter:      config.InitialFilter,
		picker:      picker.New(config.Root),
		spinner:     spin,
		progress:    progress.New(progress.WithDefaultGradient(), progress.WithWidth(44)),
		progressCh:  make(chan progressMsg, 256),
		expected:    config.ExpectedFiles,
		sortBy:      sortCode,
		searchInput: search,
	}
	model.menuHitboxes = buildMenuHitboxes()
	if config.HasRoot {
		model.screen = screenLoading
	}
	return model
}

func (model Model) Init() tea.Cmd {
	switch model.screen {
	case screenPicker:
		return model.picker.Init()
	case screenLoading:
		return tea.Batch(model.spinner.Tick, waitForProgress(model.progressCh), model.scanCmd())
	default:
		return nil
	}
}

func waitForProgress(channel <-chan progressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-channel
		if !ok {
			return nil
		}
		return msg
	}
}

func (model Model) scanCmd() tea.Cmd {
	scan := model.scan
	progressCh := model.progressCh
	root := model.root
	filter := model.filter
	return func() tea.Msg {
		if scan == nil {
			return scanDoneMsg{err: fmt.Errorf("no scan function configured")}
		}

		completed := 0
		lastSentCount := 0
		lastSentTime := time.Now()
		totals, err := scan(context.Background(), root, filter, func(counter.FileResult) {
			completed++
			if completed-lastSentCount < 256 && time.Since(lastSentTime) < 50*time.Millisecond {
				return
			}
			select {
			case progressCh <- progressMsg{completed: completed}:
			default:
			}
			lastSentCount = completed
			lastSentTime = time.Now()
		})

		select {
		case progressCh <- progressMsg{completed: completed}:
		default:
		}
		close(progressCh)
		return scanDoneMsg{totals: totals, err: err, completed: completed}
	}
}

func (model Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		model.width = msg.Width
		model.height = msg.Height
		model.picker.SetSize(msg.Width, msg.Height)
		model.menuHitboxes = buildMenuHitboxes()
		return model, nil
	case picker.SelectedMsg:
		model.root = filepath.Clean(msg.Path)
		return model.startDetectingLanguages(model.root)
	case picker.CancelledMsg:
		return model, tea.Quit
	case preScanDoneMsg:
		return model.finishLanguageDetection(msg)
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return model, tea.Quit
		}
	}

	switch model.screen {
	case screenPicker:
		return model.updatePicker(msg)
	case screenFilter:
		return model.updateFilter(msg)
	case screenLoading:
		return model.updateLoading(msg)
	case screenResults:
		return model.updateReady(msg)
	default:
		return model, nil
	}
}

func (model Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := model.picker.Update(msg)
	if pickerModel, ok := updated.(picker.Model); ok {
		model.picker = pickerModel
	}
	return model, cmd
}

func (model Model) startPicker() (Model, tea.Cmd) {
	initial := model.root
	if initial == "" || initial == "." {
		if cwd, err := os.Getwd(); err == nil {
			initial = cwd
		}
	}
	model.screen = screenPicker
	model.picker = picker.New(initial)
	model.picker.SetSize(model.width, model.height)
	model.closeMenu()
	model.modal = modalState{}
	model.status = ""
	return model, model.picker.Init()
}

func (model Model) startDetectingLanguages(root string) (Model, tea.Cmd) {
	model.screen = screenFilter
	model.filterScreen = filterScreen{detecting: true, root: root, mode: walker.FilterAll, selected: make(map[string]bool)}
	return model, tea.Batch(model.spinner.Tick, preScanCmd(root))
}

func preScanCmd(root string) tea.Cmd {
	return func() tea.Msg {
		paths, _ := walker.PreScan(root)
		return preScanDoneMsg{root: root, paths: paths}
	}
}

func (model Model) finishLanguageDetection(msg preScanDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		model.err = msg.err
		return model, tea.Quit
	}
	languages := lang.KnownLanguagesFor(msg.paths)
	if len(languages) == 0 {
		return model.startLoading(msg.root, walker.Options{Mode: walker.FilterAll}, len(msg.paths))
	}
	selected := make(map[string]bool, len(languages))
	model.screen = screenFilter
	model.filterScreen = filterScreen{
		root:      msg.root,
		paths:     msg.paths,
		languages: languages,
		mode:      walker.FilterAll,
		selected:  selected,
	}
	return model, nil
}

func (model Model) updateFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	if model.filterScreen.detecting {
		switch msg := msg.(type) {
		case spinner.TickMsg:
			var cmd tea.Cmd
			model.spinner, cmd = model.spinner.Update(msg)
			return model, cmd
		case tea.KeyMsg:
			if msg.String() == "q" || msg.String() == "esc" {
				return model, tea.Quit
			}
		}
		return model, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "q", "esc":
		return model, tea.Quit
	case "left", "shift+tab":
		model.cycleFilterMode(-1)
	case "right", "tab":
		model.cycleFilterMode(1)
	case "up", "k":
		if len(model.filterScreen.languages) > 0 {
			model.filterScreen.cursor = (model.filterScreen.cursor - 1 + len(model.filterScreen.languages)) % len(model.filterScreen.languages)
		}
	case "down", "j":
		if len(model.filterScreen.languages) > 0 {
			model.filterScreen.cursor = (model.filterScreen.cursor + 1) % len(model.filterScreen.languages)
		}
	case " ", "space":
		if model.filterScreen.mode != walker.FilterAll && len(model.filterScreen.languages) > 0 {
			language := model.filterScreen.languages[model.filterScreen.cursor]
			model.filterScreen.selected[language] = !model.filterScreen.selected[language]
		}
	case "enter":
		filter, ok := model.filterFromScreen()
		if !ok {
			model.filterScreen.status = "Select at least one language or choose All languages."
			return model, nil
		}
		expected := len(walker.FilterPaths(model.filterScreen.paths, filter))
		return model.startLoading(model.filterScreen.root, filter, expected)
	}
	return model, nil
}

func (model *Model) cycleFilterMode(delta int) {
	modes := []walker.FilterMode{walker.FilterAll, walker.FilterInclude, walker.FilterExclude}
	current := 0
	for index, mode := range modes {
		if mode == model.filterScreen.mode {
			current = index
			break
		}
	}
	model.filterScreen.mode = modes[(current+delta+len(modes))%len(modes)]
	model.filterScreen.status = ""
}

func (model Model) filterFromScreen() (walker.Options, bool) {
	if model.filterScreen.mode == walker.FilterAll {
		return walker.Options{Mode: walker.FilterAll}, true
	}
	selected := make([]string, 0, len(model.filterScreen.selected))
	for _, language := range model.filterScreen.languages {
		if model.filterScreen.selected[language] {
			selected = append(selected, language)
		}
	}
	if len(selected) == 0 {
		return walker.Options{}, false
	}
	return walker.Options{Mode: model.filterScreen.mode, Languages: walker.NormalizeLanguageSet(selected)}, true
}

func (model Model) startLoading(root string, filter walker.Options, expectedFiles int) (Model, tea.Cmd) {
	model.screen = screenLoading
	model.root = filepath.Clean(root)
	model.filter = filter
	model.expected = expectedFiles
	model.completed = 0
	model.totals = counter.Totals{}
	model.rows = nil
	model.selected = 0
	model.offset = 0
	model.progressCh = make(chan progressMsg, 256)
	model.progress = progress.New(progress.WithDefaultGradient(), progress.WithWidth(44))
	model.closeMenu()
	model.modal = modalState{}
	model.searchActive = false
	model.status = ""
	return model, tea.Batch(model.spinner.Tick, waitForProgress(model.progressCh), model.scanCmd())
}

func (model Model) updateLoading(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		model.spinner, cmd = model.spinner.Update(msg)
		return model, cmd
	case progressMsg:
		model.completed = msg.completed
		return model, waitForProgress(model.progressCh)
	case scanDoneMsg:
		model.completed = msg.completed
		if msg.err != nil {
			model.err = msg.err
			return model, tea.Quit
		}
		model.totals = msg.totals
		model.screen = screenResults
		model.recomputeRows()
		model.status = "Scan complete"
		return model, nil
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "esc" || msg.String() == "ctrl+c" {
			return model, tea.Quit
		}
	}
	return model, nil
}

func (model Model) updateReady(msg tea.Msg) (tea.Model, tea.Cmd) {
	if model.modal.kind != modalNone {
		return model.updateModal(msg)
	}
	if model.menuOpen {
		return model.updateMenu(msg)
	}
	if model.searchActive {
		return model.updateSearch(msg)
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		return model.handleMouse(msg)
	case tea.KeyMsg:
		return model.handleKey(msg)
	}
	return model, nil
}

func (model Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Alt && len(msg.Runes) > 0 {
		model.openMenuByAlt(string(msg.Runes[0]))
		return model, nil
	}
	switch msg.String() {
	case "q", "esc", "ctrl+c", "ctrl+q":
		return model, tea.Quit
	case "up", "k":
		model.moveSelection(-1)
	case "down", "j":
		model.moveSelection(1)
	case "pgup":
		model.moveSelection(-model.tableBodyHeight())
	case "pgdown":
		model.moveSelection(model.tableBodyHeight())
	case "/", "ctrl+f":
		model.searchActive = true
		model.searchInput.Focus()
		model.status = "Search mode"
	case "f":
		model.openMenuByName("Filters")
	case "e":
		model.openMenuByName("Export")
	case "?":
		model.openHelpModal()
	case "1":
		model.setSort(sortCode)
	case "2":
		model.setSort(sortFiles)
	case "3":
		model.setSort(sortComments)
	case "4":
		model.setSort(sortBlanks)
	case "5":
		model.setSort(sortTotal)
	case "6":
		model.setSort(sortLanguage)
	case "ctrl+r":
		model.reverse = !model.reverse
		model.recomputeRows()
	case "ctrl+n", "ctrl+o":
		return model.startPicker()
	case "ctrl+s":
		model.openExportModal("json")
	case "ctrl+i":
		model.openSkippedModal()
	case "l":
		model.openLanguageModal()
	case "p":
		model.openTextModal("Path Pattern", []string{"Glob pattern"}, []string{model.filters.PathPattern}, model.applyPathPattern)
	case "s":
		model.openTextModal("Min File Size", []string{"KB"}, []string{emptyIfZero(model.filters.MinSizeKB)}, model.applyMinSize)
	case "c":
		model.openTextModal("Comment Ratio Range", []string{"Min %", "Max %"}, []string{floatText(model.filters.HasCommentMin, model.filters.CommentMin), floatText(model.filters.HasCommentMax, model.filters.CommentMax)}, model.applyCommentRatio)
	case "m":
		model.openTextModal("Min Lines per File", []string{"Lines"}, []string{emptyIfZero(model.filters.MinLines)}, model.applyMinLines)
	case "ctrl+backspace", "ctrl+delete":
		model.filters = filterState{}
		model.recomputeRows()
		model.status = "Filters cleared"
	}
	return model, nil
}

func (model Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		if msg.Button == tea.MouseButtonWheelUp {
			model.moveSelection(-3)
		} else if msg.Button == tea.MouseButtonWheelDown {
			model.moveSelection(3)
		}
		return model, nil
	}

	if msg.Y == 0 {
		for index, hitbox := range model.currentMenuHitboxes() {
			if hitbox.contains(msg.X) {
				model.openMenuAtIndex(index)
				return model, nil
			}
		}
	}
	return model, nil
}

func (model Model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter":
			model.searchActive = false
			model.searchInput.Blur()
			model.status = "Search applied"
			return model, nil
		case "ctrl+c":
			return model, tea.Quit
		}
		before := model.searchInput.Value()
		var cmd tea.Cmd
		model.searchInput, cmd = model.searchInput.Update(msg)
		if model.searchInput.Value() != before {
			model.recomputeRows()
		}
		return model, cmd
	}
	return model, nil
}

func (model Model) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Alt && len(msg.Runes) > 0 {
			model.openMenuByAlt(string(msg.Runes[0]))
			return model, nil
		}
		switch msg.String() {
		case "esc":
			model.closeMenu()
		case "left":
			model.openMenuAtIndex((model.menuIndex - 1 + len(menus)) % len(menus))
		case "right":
			model.openMenuAtIndex((model.menuIndex + 1) % len(menus))
		case "up":
			model.moveMenuItem(-1)
		case "down":
			model.moveMenuItem(1)
		case "enter":
			return model.activateMenuItem()
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return model, nil
		}
		if msg.Y == 0 {
			return model.handleMouse(msg)
		}
		if itemIndex, ok := model.menuItemAtMouse(msg.X, msg.Y); ok {
			model.menuItem = itemIndex
			return model.activateMenuItem()
		}
		model.closeMenu()
	}
	return model, nil
}

func (model Model) updateModal(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch model.modal.kind {
	case modalLanguages:
		return model.updateLanguageModal(msg)
	case modalText, modalExport:
		return model.updateInputModal(msg)
	case modalInfo, modalSkipped:
		return model.updateInfoModal(msg)
	}
	return model, nil
}

func (model Model) updateLanguageModal(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "esc":
		model.closeModal()
	case "up":
		model.modal.cursor = (model.modal.cursor - 1 + len(model.modal.languages)) % len(model.modal.languages)
	case "down":
		model.modal.cursor = (model.modal.cursor + 1) % len(model.modal.languages)
	case " ", "space":
		language := model.modal.languages[model.modal.cursor]
		model.modal.languageSelected[language] = !model.modal.languageSelected[language]
	case "enter":
		selected := make(map[string]bool)
		for _, language := range model.modal.languages {
			if model.modal.languageSelected[language] {
				selected[language] = true
			}
		}
		if len(selected) == len(model.modal.languages) || len(selected) == 0 {
			model.filters.Languages = nil
		} else {
			model.filters.Languages = selected
		}
		model.closeModal()
		model.recomputeRows()
		model.status = "Language filter updated"
	}
	return model, nil
}

func (model Model) updateInputModal(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "esc":
		model.closeModal()
		return model, nil
	case "tab", "shift+tab":
		model.modal.inputs[model.modal.activeInput].Blur()
		delta := 1
		if key.String() == "shift+tab" {
			delta = -1
		}
		model.modal.activeInput = (model.modal.activeInput + delta + len(model.modal.inputs)) % len(model.modal.inputs)
		model.modal.inputs[model.modal.activeInput].Focus()
		return model, nil
	case "enter":
		if model.modal.apply != nil {
			return model, model.modal.apply(&model)
		}
		model.closeModal()
		return model, nil
	}
	var cmd tea.Cmd
	model.modal.inputs[model.modal.activeInput], cmd = model.modal.inputs[model.modal.activeInput].Update(key)
	return model, cmd
}

func (model Model) updateInfoModal(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "esc", "enter":
		model.closeModal()
	case "up":
		if model.modal.cursor > 0 {
			model.modal.cursor--
		}
	case "down":
		if model.modal.cursor < len(model.modal.message)-1 {
			model.modal.cursor++
		}
	}
	return model, nil
}

func (model Model) View() string {
	if model.width == 0 {
		model.width = 96
	}
	if model.height == 0 {
		model.height = 30
	}
	switch model.screen {
	case screenPicker:
		return model.pickerView()
	case screenFilter:
		return model.filterView()
	case screenLoading:
		return model.loadingView()
	case screenResults:
		return model.resultsView()
	default:
		return ""
	}

}

func (model Model) resultsView() string {
	parts := []string{
		model.menuBarView(),
		model.headerView(),
		model.tableView(),
		model.filtersView(),
		model.statusView(),
	}
	view := strings.Join(parts, "\n")
	if model.menuOpen {
		hitbox := model.currentMenuHitboxes()[model.menuIndex]
		view = overlayAt(view, hitbox.xMin, 1, model.menuDropdownView())
	}
	if model.modal.kind != modalNone {
		modal := model.modalView()
		x := max(0, (model.width-lipgloss.Width(modal))/2)
		y := max(2, model.height/5)
		view = overlayAt(view, x, y, modal)
	}
	return view
}

func (model Model) pickerView() string {
	pickerModel := model.picker
	pickerModel.SetSize(model.width, model.height)
	return pickerModel.View()
}

func (model Model) filterView() string {
	contentWidth := max(20, model.width-6)
	panelStyle := fullScreenPaneStyle(model.width, model.height)
	if model.filterScreen.detecting {
		path := abbreviatePath(model.filterScreen.root, model.home)
		lines := []string{
			lipgloss.NewStyle().Bold(true).Render("Language filter"),
			"",
			model.spinner.View() + " Detecting languages in " + fit(path, max(8, contentWidth-24)),
		}
		return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, panelStyle.Render(fillLines(lines, max(3, model.height-4))))
	}

	lines := []string{lipgloss.NewStyle().Bold(true).Render("Language filter")}
	modeLine := fmt.Sprintf("Mode: %s", filterModeLabel(model.filterScreen.mode))
	lines = append(lines, modeLine, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Tab/←→: mode   Space: toggle   Enter: scan   Esc: quit"), "")
	listHeight := max(1, model.height-10)
	start, end := visibleRange(len(model.filterScreen.languages), model.filterScreen.cursor, listHeight)
	for index := start; index < end; index++ {
		language := model.filterScreen.languages[index]
		mark := "[ ]"
		if model.filterScreen.mode == walker.FilterAll {
			mark = "[·]"
		} else if model.filterScreen.selected[language] {
			mark = "[x]"
		}
		line := fmt.Sprintf("%s %s", mark, language)
		if index == model.filterScreen.cursor {
			line = lipgloss.NewStyle().Background(lipgloss.Color("238")).Foreground(lipgloss.Color("15")).Render(line)
		}
		lines = append(lines, line)
	}
	for len(lines) < listHeight+4 {
		lines = append(lines, "")
	}
	if model.filterScreen.status != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fit(model.filterScreen.status, contentWidth)))
	} else {
		lines = append(lines, "")
	}
	return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, panelStyle.Render(fillLines(lines, max(3, model.height-4))))
}

func filterModeLabel(mode walker.FilterMode) string {
	switch mode {
	case walker.FilterInclude:
		return "Only include selected languages"
	case walker.FilterExclude:
		return "Exclude selected languages"
	default:
		return "All languages"
	}
}

func (model Model) loadingView() string {
	contentWidth := max(20, model.width-6)
	percent := model.progressPercent()
	path := abbreviatePath(model.root, model.home)
	progressModel := model.progress
	progressModel.Width = clamp(contentWidth-lipgloss.Width(progressText(model.completed, model.expected))-2, 12, max(12, contentWidth-2))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("Scanning"),
		"",
		model.spinner.View() + " " + fit(path, max(8, contentWidth-4)),
		fmt.Sprintf("%s %s", progressModel.ViewAs(percent), progressText(model.completed, model.expected)),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(fit("Results will open when the scan completes.", contentWidth)),
	}
	panel := fullScreenPaneStyle(model.width, model.height).Render(fillLines(lines, max(3, model.height-4)))
	return lipgloss.Place(model.width, model.height, lipgloss.Center, lipgloss.Center, panel)
}

func (model Model) progressPercent() float64 {
	if model.expected > 0 {
		return minFloat(1, float64(model.completed)/float64(model.expected))
	}
	if model.completed == 0 {
		return 0.05
	}
	return minFloat(0.95, float64(model.completed)/float64(model.completed+1000))
}

func progressText(completed, expected int) string {
	if expected > 0 {
		return fmt.Sprintf("%s/%s files", formatInt(completed), formatInt(expected))
	}
	return fmt.Sprintf("%s files", formatInt(completed))
}

func (model Model) menuBarView() string {
	model.menuHitboxes = buildMenuHitboxes()
	pieces := make([]string, 0, len(menus))
	for index, menu := range menus {
		style := lipgloss.NewStyle().Padding(0, 1)
		if model.menuOpen && model.menuIndex == index {
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("15"))
		}
		pieces = append(pieces, style.Render(underlinedMenuLabel(menu.label, menu.trigger)))
	}
	return lipgloss.NewStyle().Width(model.width).Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Render(strings.Join(pieces, ""))
}

func (model Model) headerView() string {
	counts := sumRows(model.rows)
	pathLine := "📁 " + abbreviatePath(model.root, model.home)
	summary := fmt.Sprintf("%s files · %s code · %s comments · %s blanks", formatInt(counts.Files), formatInt(counts.Code), formatInt(counts.Comments), formatInt(counts.Blanks))
	return paneStyle(model.width).Render(pathLine + "\n" + summary)
}

func (model Model) tableView() string {
	bodyHeight := model.tableBodyHeight()
	model.ensureVisible(bodyHeight)
	head := lipgloss.NewStyle().Bold(true).Render(formatRow(languageRow{Language: "Language", Counts: counter.Counts{Files: -1}}, true))
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", max(20, model.width-8)))
	visible := model.rows
	end := min(len(visible), model.offset+bodyHeight)
	lines := []string{head, separator}
	for index := model.offset; index < end; index++ {
		prefix := "  "
		style := lipgloss.NewStyle()
		if index == model.selected {
			prefix = "❯ "
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("15"))
		}
		rowText := fit(prefix+formatRow(visible[index], false), max(20, model.width-8))
		lines = append(lines, style.Render(rowText))
	}
	for len(lines) < bodyHeight+2 {
		lines = append(lines, "")
	}
	if len(model.rows) > bodyHeight {
		lines[0] = fit(lines[0], model.width-7) + " ▲"
	}
	return paneStyle(model.width).Render(strings.Join(lines, "\n"))
}

func (model Model) filtersView() string {
	text := "Filters: " + strings.Join(model.filterChips(), " · ")
	if len(model.filterChips()) == 0 {
		text = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Filters: none")
	}
	return paneStyle(model.width).Render(text)
}

func (model Model) statusView() string {
	text := "↑↓: navigate  /: search  f: filters  e: export  q: quit"
	if model.searchActive {
		text = model.searchInput.View() + "  Enter/Esc: close search"
	} else if model.status != "" {
		text = text + "  ·  " + model.status
	}
	text = statusLineWithAttribution(text, model.width)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Padding(0, 1).Width(model.width).Render(text)
}

func statusLineWithAttribution(text string, width int) string {
	contentWidth := width - 2
	if contentWidth <= 0 {
		return text
	}
	textWidth := lipgloss.Width(text)
	attributionWidth := lipgloss.Width(statusAttributionText)
	if textWidth+1+attributionWidth > contentWidth {
		return text
	}
	attribution := attributionStyle().Render(statusAttributionText)
	return text + strings.Repeat(" ", contentWidth-textWidth-attributionWidth) + attribution
}

func paneStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(max(40, width-2))
}

func fullScreenPaneStyle(width, height int) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(max(1, width-2)).Height(max(1, height-2))
}

func formatRow(row languageRow, header bool) string {
	if header {
		return fmt.Sprintf("%-16s %7s %10s %10s %8s %8s", "Language", "Files", "Code", "Comments", "Blanks", "Total")
	}
	return fmt.Sprintf("%-16s %7s %10s %10s %8s %8s",
		fit(row.Language, 16),
		formatInt(row.Counts.Files),
		formatInt(row.Counts.Code),
		formatInt(row.Counts.Comments),
		formatInt(row.Counts.Blanks),
		formatInt(row.Counts.Total),
	)
}

func (model Model) tableBodyHeight() int {
	return max(5, model.height-15)
}

func (model *Model) moveSelection(delta int) {
	if len(model.rows) == 0 {
		model.selected = 0
		model.offset = 0
		return
	}
	model.selected = clamp(model.selected+delta, 0, len(model.rows)-1)
	model.ensureVisible(model.tableBodyHeight())
}

func (model *Model) ensureVisible(height int) {
	if model.selected < model.offset {
		model.offset = model.selected
	}
	if model.selected >= model.offset+height {
		model.offset = model.selected - height + 1
	}
	model.offset = clamp(model.offset, 0, max(0, len(model.rows)-height))
}

func (model *Model) recomputeRows() {
	aggregates := make(map[string]counter.Counts)
	for _, file := range model.totals.Files {
		if !model.fileVisible(file) {
			continue
		}
		counts := aggregates[file.Language]
		counts.Files++
		counts.Code += file.Counts.Code
		counts.Comments += file.Counts.Comments
		counts.Blanks += file.Counts.Blanks
		counts.Total += file.Counts.Total
		aggregates[file.Language] = counts
	}

	rows := make([]languageRow, 0, len(aggregates))
	for language, counts := range aggregates {
		rows = append(rows, languageRow{Language: language, Counts: counts})
	}
	model.sortRows(rows)
	model.rows = rows
	if model.selected >= len(model.rows) {
		model.selected = max(0, len(model.rows)-1)
	}
	model.ensureVisible(model.tableBodyHeight())
}

func (model Model) fileVisible(file counter.FileResult) bool {
	if len(model.filters.Languages) > 0 && !model.filters.Languages[file.Language] {
		return false
	}
	if model.filters.PathPattern != "" && model.filters.PathPattern != "*" {
		if !pathMatches(model.filters.PathPattern, model.root, file.Path) {
			return false
		}
	}
	if model.filters.MinSizeKB > 0 && file.Size < int64(model.filters.MinSizeKB)*1024 {
		return false
	}
	if model.filters.MinLines > 0 && file.Counts.Total < model.filters.MinLines {
		return false
	}
	if model.filters.HasCommentMin || model.filters.HasCommentMax {
		ratio := 0.0
		if file.Counts.Total > 0 {
			ratio = float64(file.Counts.Comments) * 100 / float64(file.Counts.Total)
		}
		if model.filters.HasCommentMin && ratio < model.filters.CommentMin {
			return false
		}
		if model.filters.HasCommentMax && ratio > model.filters.CommentMax {
			return false
		}
	}
	query := strings.TrimSpace(strings.ToLower(model.searchInput.Value()))
	if query != "" && !strings.Contains(strings.ToLower(file.Language), query) && !strings.Contains(strings.ToLower(file.Path), query) {
		return false
	}
	return true
}

func pathMatches(pattern, root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(path)
	if matched, err := filepath.Match(pattern, rel); err == nil && matched {
		return true
	}
	if matched, err := filepath.Match(pattern, base); err == nil && matched {
		return true
	}
	return strings.Contains(strings.ToLower(rel), strings.ToLower(pattern))
}

func (model Model) sortRows(rows []languageRow) {
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		comparison := 0
		switch model.sortBy {
		case sortFiles:
			comparison = compareDesc(left.Counts.Files, right.Counts.Files)
		case sortComments:
			comparison = compareDesc(left.Counts.Comments, right.Counts.Comments)
		case sortBlanks:
			comparison = compareDesc(left.Counts.Blanks, right.Counts.Blanks)
		case sortTotal:
			comparison = compareDesc(left.Counts.Total, right.Counts.Total)
		case sortLanguage:
			comparison = strings.Compare(left.Language, right.Language)
		default:
			comparison = compareDesc(left.Counts.Code, right.Counts.Code)
		}
		if comparison == 0 && model.sortBy != sortLanguage {
			comparison = strings.Compare(left.Language, right.Language)
		}
		if model.reverse {
			comparison = -comparison
		}
		return comparison < 0
	})
}

func compareDesc(left, right int) int {
	if left > right {
		return -1
	}
	if left < right {
		return 1
	}
	return 0
}

func (model *Model) setSort(field sortField) {
	if model.sortBy == field {
		model.reverse = !model.reverse
	} else {
		model.sortBy = field
		model.reverse = false
	}
	model.recomputeRows()
	model.status = "Sorted by " + sortName(field)
}

func sortName(field sortField) string {
	switch field {
	case sortFiles:
		return "files"
	case sortComments:
		return "comments"
	case sortBlanks:
		return "blanks"
	case sortTotal:
		return "total"
	case sortLanguage:
		return "language"
	default:
		return "code"
	}
}

func (model Model) filterChips() []string {
	var chips []string
	if len(model.filters.Languages) > 0 {
		chips = append(chips, fmt.Sprintf("langs(%d)", len(model.filters.Languages)))
	}
	if model.filters.PathPattern != "" {
		chips = append(chips, "path("+model.filters.PathPattern+")")
	}
	if model.filters.MinSizeKB > 0 {
		chips = append(chips, fmt.Sprintf("size:>%dKB", model.filters.MinSizeKB))
	}
	if model.filters.HasCommentMin || model.filters.HasCommentMax {
		comment := "comments:"
		if model.filters.HasCommentMin {
			comment += fmt.Sprintf(">%.0f%%", model.filters.CommentMin)
		}
		if model.filters.HasCommentMax {
			comment += fmt.Sprintf("<%.0f%%", model.filters.CommentMax)
		}
		chips = append(chips, comment)
	}
	if model.filters.MinLines > 0 {
		chips = append(chips, fmt.Sprintf("lines:>%d", model.filters.MinLines))
	}
	return chips
}

func (model Model) allLanguages() []string {
	seen := make(map[string]struct{})
	for _, file := range model.totals.Files {
		seen[file.Language] = struct{}{}
	}
	languages := make([]string, 0, len(seen))
	for language := range seen {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

func sumRows(rows []languageRow) counter.Counts {
	var counts counter.Counts
	for _, row := range rows {
		counts.Files += row.Counts.Files
		counts.Code += row.Counts.Code
		counts.Comments += row.Counts.Comments
		counts.Blanks += row.Counts.Blanks
		counts.Total += row.Counts.Total
	}
	return counts
}

type menuDefinition struct {
	label   string
	trigger string
	items   []menuItem
}

type menuItem struct {
	label     string
	shortcut  string
	action    string
	separator bool
}

var menus = []menuDefinition{
	{label: "File", trigger: "F", items: []menuItem{
		{label: "New Scan...", shortcut: "⌘N", action: "new"},
		{label: "Open Folder...", shortcut: "⌘O", action: "open"},
		{label: "Save As...", shortcut: "⌘S", action: "save"},
		{separator: true},
		{label: "Quit", shortcut: "⌘Q", action: "quit"},
	}},
	{label: "Edit", trigger: "E", items: []menuItem{
		{label: "Copy Selected Row", shortcut: "⌘C", action: "copy-row"},
		{label: "Copy All Visible", shortcut: "⇧⌘C", action: "copy-all"},
		{separator: true},
		{label: "Find...", shortcut: "⌘F", action: "find"},
	}},
	{label: "View", trigger: "V", items: []menuItem{
		{label: "Sort by Code", shortcut: "1", action: "sort-code"},
		{label: "Sort by Files", shortcut: "2", action: "sort-files"},
		{label: "Sort by Comments", shortcut: "3", action: "sort-comments"},
		{label: "Sort by Blanks", shortcut: "4", action: "sort-blanks"},
		{label: "Sort by Total", shortcut: "5", action: "sort-total"},
		{label: "Sort by Language Name", shortcut: "6", action: "sort-language"},
		{separator: true},
		{label: "Reverse Order", shortcut: "⌘R", action: "reverse"},
		{separator: true},
		{label: "Show Skipped Files", shortcut: "⌘I", action: "skipped"},
	}},
	{label: "Filters", trigger: "I", items: []menuItem{
		{label: "Languages...", shortcut: "L", action: "filter-languages"},
		{label: "Path Pattern...", shortcut: "P", action: "filter-path"},
		{label: "Min File Size...", shortcut: "S", action: "filter-size"},
		{label: "Comment Ratio Range...", shortcut: "C", action: "filter-comments"},
		{label: "Min Lines per File", shortcut: "M", action: "filter-lines"},
		{separator: true},
		{label: "Clear All Filters", shortcut: "⌘⌫", action: "clear-filters"},
	}},
	{label: "Export", trigger: "X", items: []menuItem{
		{label: "Save as JSON...", shortcut: "⇧⌘J", action: "export-json"},
		{label: "Save as CSV...", shortcut: "⇧⌘V", action: "export-csv"},
		{label: "Save as Markdown...", shortcut: "⇧⌘M", action: "export-markdown"},
		{label: "Save as HTML...", shortcut: "⇧⌘H", action: "export-html"},
		{label: "Save as TXT...", shortcut: "⇧⌘T", action: "export-txt"},
		{separator: true},
		{label: "Copy as JSON", shortcut: "", action: "copy-json"},
		{label: "Copy as Markdown", shortcut: "", action: "copy-markdown"},
	}},
	{label: "Help", trigger: "H", items: []menuItem{
		{label: "Keyboard Shortcuts", shortcut: "?", action: "help"},
		{label: "About loc", shortcut: "", action: "about"},
	}},
}

type menuHitbox struct {
	name string
	xMin int
	xMax int
}

func (hitbox menuHitbox) contains(x int) bool {
	return x >= hitbox.xMin && x <= hitbox.xMax
}

func buildMenuHitboxes() []menuHitbox {
	hitboxes := make([]menuHitbox, 0, len(menus))
	x := 0
	for _, menu := range menus {
		width := len(menu.label) + 2
		hitboxes = append(hitboxes, menuHitbox{name: menu.label, xMin: x, xMax: x + width - 1})
		x += width
	}
	return hitboxes
}

func (model Model) currentMenuHitboxes() []menuHitbox {
	if len(model.menuHitboxes) == len(menus) {
		return model.menuHitboxes
	}
	return buildMenuHitboxes()
}

func underlinedMenuLabel(label, trigger string) string {
	index := strings.Index(strings.ToLower(label), strings.ToLower(trigger))
	if index < 0 {
		return label
	}
	return label[:index] + lipgloss.NewStyle().Underline(true).Render(label[index:index+1]) + label[index+1:]
}

func (model *Model) openMenuByName(name string) {
	for index, menu := range menus {
		if menu.label == name {
			model.openMenuAtIndex(index)
			return
		}
	}
}

func (model *Model) openMenuByAlt(trigger string) {
	for index, menu := range menus {
		if strings.EqualFold(menu.trigger, trigger) {
			model.openMenuAtIndex(index)
			return
		}
	}
}

func (model *Model) openMenuAtIndex(index int) {
	if index < 0 || index >= len(menus) {
		return
	}
	model.menuOpen = true
	model.openMenu = menus[index].label
	model.menuIndex = index
	model.menuItem = firstMenuItem(index)
}

func (model *Model) closeMenu() {
	model.menuOpen = false
	model.openMenu = ""
}

func firstMenuItem(menuIndex int) int {
	for index, item := range menus[menuIndex].items {
		if !item.separator {
			return index
		}
	}
	return 0
}

func (model *Model) moveMenuItem(delta int) {
	items := menus[model.menuIndex].items
	for attempt := 0; attempt < len(items); attempt++ {
		model.menuItem = (model.menuItem + delta + len(items)) % len(items)
		if !items[model.menuItem].separator {
			return
		}
	}
}

func (model Model) menuDropdownView() string {
	items := menus[model.menuIndex].items
	lines := make([]string, 0, len(items))
	for index, item := range items {
		if item.separator {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", 30)))
			continue
		}
		line := fmt.Sprintf("%-24s %5s", item.label, item.shortcut)
		if index == model.menuItem {
			line = lipgloss.NewStyle().Background(lipgloss.Color("39")).Foreground(lipgloss.Color("15")).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (model Model) menuItemAtMouse(x, y int) (int, bool) {
	if !model.menuOpen || model.menuIndex < 0 || model.menuIndex >= len(menus) {
		return 0, false
	}
	hitbox := model.currentMenuHitboxes()[model.menuIndex]
	if x < hitbox.xMin || x > hitbox.xMin+33 || y < 2 {
		return 0, false
	}
	itemIndex := y - 2
	items := menus[model.menuIndex].items
	if itemIndex < 0 || itemIndex >= len(items) || items[itemIndex].separator {
		return 0, false
	}
	return itemIndex, true
}

func (model Model) activateMenuItem() (tea.Model, tea.Cmd) {
	item := menus[model.menuIndex].items[model.menuItem]
	model.closeMenu()
	return model.runAction(item.action)
}

func (model Model) runAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "new", "open":
		return model.startPicker()
	case "save":
		model.openMenuByName("Export")
	case "quit":
		return model, tea.Quit
	case "copy-row":
		model.copySelectedRow()
	case "copy-all":
		model.copyAllVisible()
	case "find":
		model.searchActive = true
		model.searchInput.Focus()
	case "sort-code":
		model.setSort(sortCode)
	case "sort-files":
		model.setSort(sortFiles)
	case "sort-comments":
		model.setSort(sortComments)
	case "sort-blanks":
		model.setSort(sortBlanks)
	case "sort-total":
		model.setSort(sortTotal)
	case "sort-language":
		model.setSort(sortLanguage)
	case "reverse":
		model.reverse = !model.reverse
		model.recomputeRows()
	case "skipped":
		model.openSkippedModal()
	case "filter-languages":
		model.openLanguageModal()
	case "filter-path":
		model.openTextModal("Path Pattern", []string{"Glob pattern"}, []string{model.filters.PathPattern}, model.applyPathPattern)
	case "filter-size":
		model.openTextModal("Min File Size", []string{"KB"}, []string{emptyIfZero(model.filters.MinSizeKB)}, model.applyMinSize)
	case "filter-comments":
		model.openTextModal("Comment Ratio Range", []string{"Min %", "Max %"}, []string{floatText(model.filters.HasCommentMin, model.filters.CommentMin), floatText(model.filters.HasCommentMax, model.filters.CommentMax)}, model.applyCommentRatio)
	case "filter-lines":
		model.openTextModal("Min Lines per File", []string{"Lines"}, []string{emptyIfZero(model.filters.MinLines)}, model.applyMinLines)
	case "clear-filters":
		model.filters = filterState{}
		model.recomputeRows()
		model.status = "Filters cleared"
	case "export-json":
		model.openExportModal("json")
	case "export-csv":
		model.openExportModal("csv")
	case "export-markdown":
		model.openExportModal("markdown")
	case "export-html":
		model.openExportModal("html")
	case "export-txt":
		model.openExportModal("txt")
	case "copy-json":
		model.copyExport("json")
	case "copy-markdown":
		model.copyExport("markdown")
	case "help":
		model.openHelpModal()
	case "about":
		model.openAboutModal()
	}
	return model, nil
}

func (model *Model) closeModal() {
	model.modal = modalState{}
}

func (model *Model) openLanguageModal() {
	languages := model.allLanguages()
	selected := make(map[string]bool, len(languages))
	for _, language := range languages {
		if len(model.filters.Languages) == 0 || model.filters.Languages[language] {
			selected[language] = true
		}
	}
	model.modal = modalState{kind: modalLanguages, title: "Languages", languages: languages, languageSelected: selected}
}

func (model *Model) openTextModal(title string, labels []string, values []string, apply func(*Model) tea.Cmd) {
	inputs := make([]textinput.Model, 0, len(labels))
	for index, label := range labels {
		input := textinput.New()
		input.Prompt = label + ": "
		input.Width = 34
		input.SetValue(values[index])
		if index == 0 {
			input.Focus()
		}
		inputs = append(inputs, input)
	}
	model.modal = modalState{kind: modalText, title: title, inputs: inputs, apply: apply}
}

func (model *Model) openExportModal(format string) {
	input := textinput.New()
	input.Prompt = "Path: "
	input.Width = 42
	input.SetValue("loc-results." + extensionFor(format))
	input.Focus()
	model.modal = modalState{kind: modalExport, title: "Save as " + strings.ToUpper(format), inputs: []textinput.Model{input}, exportFormat: format, apply: func(model *Model) tea.Cmd {
		path := strings.TrimSpace(model.modal.inputs[0].Value())
		if path == "" {
			model.status = "Export path is empty"
			return nil
		}
		if err := os.WriteFile(path, []byte(model.exportString(model.modal.exportFormat)), 0o600); err != nil {
			model.status = "Export failed: " + err.Error()
			return nil
		}
		model.status = "Saved " + path
		model.closeModal()
		return nil
	}}
}

func (model *Model) openSkippedModal() {
	lines := make([]string, 0, len(model.totals.SkippedFiles))
	if len(model.totals.SkippedFiles) == 0 {
		lines = append(lines, "No skipped files.")
	} else {
		for _, skipped := range model.totals.SkippedFiles {
			lines = append(lines, fmt.Sprintf("%s  %s", skipped.SkipReason, abbreviatePath(skipped.Path, model.home)))
		}
	}
	model.modal = modalState{kind: modalSkipped, title: "Skipped Files", message: lines}
}

func (model *Model) openHelpModal() {
	model.modal = modalState{kind: modalInfo, title: "Keyboard Shortcuts", message: []string{
		"↑↓ / PgUp/PgDn  Navigate rows",
		"/ or Ctrl+F    Search visible paths and languages",
		"f / e          Open Filters or Export menus",
		"1-6            Sort by Code, Files, Comments, Blanks, Total, Language",
		"Ctrl+R         Reverse order",
		"L P S C M      Language, path, size, comment ratio, min lines filters",
		"Ctrl+I         Show skipped files",
		"Ctrl+N/O       New scan / open folder",
		"q              Quit",
	}}
}

func (model *Model) openAboutModal() {
	model.modal = modalState{kind: modalInfo, title: "About loc", message: []string{
		"loc " + model.version,
		copyrightStyle().Render(aboutCopyright),
		attributionStyle().Render(aboutMadeWith),
		attributionStyle().Render(aboutRepository),
		"Fast line counting for source trees.",
	}}
}

func (model Model) modalView() string {
	switch model.modal.kind {
	case modalLanguages:
		return model.languageModalView()
	case modalText, modalExport:
		return model.inputModalView()
	case modalSkipped, modalInfo:
		return model.infoModalView()
	default:
		return ""
	}
}

func (model Model) languageModalView() string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render(model.modal.title)}
	for index, language := range model.modal.languages {
		mark := "[ ]"
		if model.modal.languageSelected[language] {
			mark = "[x]"
		}
		line := fmt.Sprintf("%s %s", mark, language)
		if index == model.modal.cursor {
			line = lipgloss.NewStyle().Background(lipgloss.Color("238")).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "Space: toggle  Enter: apply  Esc: cancel")
	return modalStyle().Render(strings.Join(lines, "\n"))
}

func (model Model) inputModalView() string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render(model.modal.title)}
	for _, input := range model.modal.inputs {
		lines = append(lines, input.View())
	}
	lines = append(lines, "", "Tab: next  Enter: apply  Esc: cancel")
	return modalStyle().Render(strings.Join(lines, "\n"))
}

func (model Model) infoModalView() string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render(model.modal.title)}
	start := model.modal.cursor
	if start > 0 {
		start--
	}
	end := min(len(model.modal.message), start+12)
	lines = append(lines, model.modal.message[start:end]...)
	lines = append(lines, "", "Enter/Esc: close")
	return modalStyle().Render(strings.Join(lines, "\n"))
}

func modalStyle() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(64).Background(lipgloss.Color("235"))
}

func copyrightStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
}

func attributionStyle() lipgloss.Style {
	return lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
}

func (model *Model) applyPathPattern(*Model) tea.Cmd {
	model.filters.PathPattern = strings.TrimSpace(model.modal.inputs[0].Value())
	model.closeModal()
	model.recomputeRows()
	model.status = "Path filter updated"
	return nil
}

func (model *Model) applyMinSize(*Model) tea.Cmd {
	value, err := optionalInt(model.modal.inputs[0].Value())
	if err != nil {
		model.status = "Size must be a number"
		return nil
	}
	model.filters.MinSizeKB = value
	model.closeModal()
	model.recomputeRows()
	model.status = "Size filter updated"
	return nil
}

func (model *Model) applyCommentRatio(*Model) tea.Cmd {
	minValue, hasMin, err := optionalFloat(model.modal.inputs[0].Value())
	if err != nil {
		model.status = "Min comment ratio must be a number"
		return nil
	}
	maxValue, hasMax, err := optionalFloat(model.modal.inputs[1].Value())
	if err != nil {
		model.status = "Max comment ratio must be a number"
		return nil
	}
	model.filters.CommentMin = minValue
	model.filters.HasCommentMin = hasMin
	model.filters.CommentMax = maxValue
	model.filters.HasCommentMax = hasMax
	model.closeModal()
	model.recomputeRows()
	model.status = "Comment ratio filter updated"
	return nil
}

func (model *Model) applyMinLines(*Model) tea.Cmd {
	value, err := optionalInt(model.modal.inputs[0].Value())
	if err != nil {
		model.status = "Line filter must be a number"
		return nil
	}
	model.filters.MinLines = value
	model.closeModal()
	model.recomputeRows()
	model.status = "Line filter updated"
	return nil
}

func optionalInt(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid number")
	}
	return parsed, nil
}

func optionalFloat(value string) (float64, bool, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || parsed < 0 {
		return 0, false, fmt.Errorf("invalid number")
	}
	return parsed, true, nil
}

func (model *Model) copySelectedRow() {
	if len(model.rows) == 0 || model.selected >= len(model.rows) {
		model.status = "No selected row"
		return
	}
	if err := clipboard.WriteAll(rowTSV(model.rows[model.selected])); err != nil {
		model.status = "Clipboard failed: " + err.Error()
		return
	}
	model.status = "Copied selected row"
}

func (model *Model) copyAllVisible() {
	var builder strings.Builder
	builder.WriteString("Language\tFiles\tCode\tComments\tBlanks\tTotal\n")
	for _, row := range model.rows {
		builder.WriteString(rowTSV(row))
		builder.WriteByte('\n')
	}
	if err := clipboard.WriteAll(builder.String()); err != nil {
		model.status = "Clipboard failed: " + err.Error()
		return
	}
	model.status = "Copied visible rows"
}

func (model *Model) copyExport(format string) {
	if err := clipboard.WriteAll(model.exportString(format)); err != nil {
		model.status = "Clipboard failed: " + err.Error()
		return
	}
	model.status = "Copied " + format
}

func rowTSV(row languageRow) string {
	return fmt.Sprintf("%s\t%d\t%d\t%d\t%d\t%d", row.Language, row.Counts.Files, row.Counts.Code, row.Counts.Comments, row.Counts.Blanks, row.Counts.Total)
}

func (model Model) exportString(format string) string {
	switch format {
	case "json":
		data, _ := json.MarshalIndent(model.rows, "", "  ")
		return string(data) + "\n"
	case "csv":
		var builder strings.Builder
		builder.WriteString("Language,Files,Code,Comments,Blanks,Total\n")
		for _, row := range model.rows {
			builder.WriteString(fmt.Sprintf("%q,%d,%d,%d,%d,%d\n", row.Language, row.Counts.Files, row.Counts.Code, row.Counts.Comments, row.Counts.Blanks, row.Counts.Total))
		}
		return builder.String()
	case "markdown":
		var builder strings.Builder
		builder.WriteString("| Language | Files | Code | Comments | Blanks | Total |\n")
		builder.WriteString("|---|---:|---:|---:|---:|---:|\n")
		for _, row := range model.rows {
			builder.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %d |\n", row.Language, row.Counts.Files, row.Counts.Code, row.Counts.Comments, row.Counts.Blanks, row.Counts.Total))
		}
		return builder.String()
	case "html":
		var builder strings.Builder
		builder.WriteString("<table><thead><tr><th>Language</th><th>Files</th><th>Code</th><th>Comments</th><th>Blanks</th><th>Total</th></tr></thead><tbody>")
		for _, row := range model.rows {
			builder.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td></tr>", html.EscapeString(row.Language), row.Counts.Files, row.Counts.Code, row.Counts.Comments, row.Counts.Blanks, row.Counts.Total))
		}
		builder.WriteString("</tbody></table>\n")
		return builder.String()
	default:
		var builder strings.Builder
		for _, row := range model.rows {
			builder.WriteString(formatRow(row, false))
			builder.WriteByte('\n')
		}
		return builder.String()
	}
}

func extensionFor(format string) string {
	if format == "markdown" {
		return "md"
	}
	return format
}

func overlayAt(base string, x, y int, overlay string) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	for len(baseLines) < y+len(overlayLines) {
		baseLines = append(baseLines, "")
	}
	for index, line := range overlayLines {
		target := y + index
		baseLines[target] = strings.Repeat(" ", max(0, x)) + line
	}
	return strings.Join(baseLines, "\n")
}

func fillLines(lines []string, height int) string {
	if height < 1 {
		height = 1
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func visibleRange(total, cursor, height int) (int, int) {
	if total <= 0 || height <= 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	start := 0
	if cursor >= height {
		start = cursor - height + 1
	}
	end := min(total, start+height)
	return start, end
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

func formatInt(value int) string {
	text := strconv.Itoa(value)
	if len(text) <= 3 {
		return text
	}
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	parts = append([]string{text}, parts...)
	return strings.Join(parts, ",")
}

func fit(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return value[:0]
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:width-1]) + "…"
}

func padRight(value string, width int) string {
	if lipgloss.Width(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-lipgloss.Width(value))
}

func emptyIfZero(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func floatText(enabled bool, value float64) string {
	if !enabled {
		return ""
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
