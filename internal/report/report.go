package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"github.com/theoriuhd/loc/internal/counter"
)

type LanguageRow struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
	Code     int    `json:"code"`
	Comments int    `json:"comments"`
	Blanks   int    `json:"blanks"`
	Total    int    `json:"total"`
}

type JSONReport struct {
	Languages []LanguageRow  `json:"languages"`
	Overall   counter.Counts `json:"overall"`
	Skipped   map[string]int `json:"skipped"`
}

func WriteJSON(writer io.Writer, totals counter.Totals) error {
	report := JSONReport{
		Languages: sortedRows(totals),
		Overall:   totals.Overall,
		Skipped:   skippedMap(totals),
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func RenderTable(totals counter.Totals) string {
	columns := []table.Column{
		{Title: "Language", Width: 16},
		{Title: "Files", Width: 8},
		{Title: "Code", Width: 10},
		{Title: "Comments", Width: 10},
		{Title: "Blanks", Width: 10},
		{Title: "Total", Width: 10},
	}

	rows := make([]table.Row, 0, len(totals.ByLanguage)+1)
	for _, row := range sortedRows(totals) {
		rows = append(rows, table.Row{
			row.Language,
			strconv.Itoa(row.Files),
			strconv.Itoa(row.Code),
			strconv.Itoa(row.Comments),
			strconv.Itoa(row.Blanks),
			strconv.Itoa(row.Total),
		})
	}
	rows = append(rows, table.Row{
		"TOTAL",
		strconv.Itoa(totals.Overall.Files),
		strconv.Itoa(totals.Overall.Code),
		strconv.Itoa(totals.Overall.Comments),
		strconv.Itoa(totals.Overall.Blanks),
		strconv.Itoa(totals.Overall.Total),
	})

	model := table.New(table.WithColumns(columns), table.WithRows(rows), table.WithHeight(len(rows)+1))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240")).BorderBottom(true).Bold(true)
	styles.Selected = lipgloss.NewStyle()
	model.SetStyles(styles)

	var builder strings.Builder
	builder.WriteString(model.View())
	builder.WriteString("\n")
	builder.WriteString(RenderSkipped(totals))
	return builder.String()
}

func RenderSkipped(totals counter.Totals) string {
	binary := totals.Skipped[counter.SkipBinary]
	permission := totals.Skipped[counter.SkipPermission]
	other := totals.Skipped[counter.SkipOther]
	return fmt.Sprintf("Skipped files: %d (binary: %d, permission: %d, other: %d)", binary+permission+other, binary, permission, other)
}

func sortedRows(totals counter.Totals) []LanguageRow {
	rows := make([]LanguageRow, 0, len(totals.ByLanguage))
	for language, counts := range totals.ByLanguage {
		rows = append(rows, LanguageRow{
			Language: language,
			Files:    counts.Files,
			Code:     counts.Code,
			Comments: counts.Comments,
			Blanks:   counts.Blanks,
			Total:    counts.Total,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Code == rows[j].Code {
			return rows[i].Language < rows[j].Language
		}
		return rows[i].Code > rows[j].Code
	})
	return rows
}

func skippedMap(totals counter.Totals) map[string]int {
	return map[string]int{
		string(counter.SkipBinary):     totals.Skipped[counter.SkipBinary],
		string(counter.SkipPermission): totals.Skipped[counter.SkipPermission],
		string(counter.SkipOther):      totals.Skipped[counter.SkipOther],
	}
}
