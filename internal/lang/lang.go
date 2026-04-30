package lang

import (
	"path/filepath"
	"sort"
	"strings"
)

type CommentRule struct {
	SingleLine [][]byte
	Blocks     []BlockRule
}

type BlockRule struct {
	Start []byte
	End   []byte
}

type Language struct {
	Name string
	Rule CommentRule
}

var extensionMap = map[string]Language{
	".go":     {Name: "Go", Rule: slashRule()},
	".py":     {Name: "Python", Rule: CommentRule{SingleLine: markers("#"), Blocks: blocks("\"\"\"", "\"\"\"", "'''", "'''")}},
	".ts":     {Name: "TypeScript", Rule: slashRule()},
	".tsx":    {Name: "TSX", Rule: slashRule()},
	".js":     {Name: "JavaScript", Rule: slashRule()},
	".jsx":    {Name: "JSX", Rule: slashRule()},
	".rs":     {Name: "Rust", Rule: slashRule()},
	".c":      {Name: "C", Rule: slashRule()},
	".h":      {Name: "C-Header", Rule: slashRule()},
	".cpp":    {Name: "C++", Rule: slashRule()},
	".hpp":    {Name: "C++-Header", Rule: slashRule()},
	".java":   {Name: "Java", Rule: slashRule()},
	".kt":     {Name: "Kotlin", Rule: slashRule()},
	".swift":  {Name: "Swift", Rule: slashRule()},
	".rb":     {Name: "Ruby", Rule: CommentRule{SingleLine: markers("#")}},
	".php":    {Name: "PHP", Rule: slashRule()},
	".cs":     {Name: "C#", Rule: slashRule()},
	".lua":    {Name: "Lua", Rule: CommentRule{SingleLine: markers("--")}},
	".sh":     {Name: "Shell", Rule: CommentRule{SingleLine: markers("#")}},
	".css":    {Name: "CSS", Rule: CommentRule{Blocks: blocks("/*", "*/")}},
	".scss":   {Name: "SCSS", Rule: slashRule()},
	".html":   {Name: "HTML", Rule: CommentRule{Blocks: blocks("<!--", "-->")}},
	".vue":    {Name: "Vue", Rule: CommentRule{SingleLine: markers("//"), Blocks: blocks("/*", "*/", "<!--", "-->")}},
	".svelte": {Name: "Svelte", Rule: CommentRule{SingleLine: markers("//"), Blocks: blocks("/*", "*/", "<!--", "-->")}},
	".sql":    {Name: "SQL", Rule: CommentRule{SingleLine: markers("--"), Blocks: blocks("/*", "*/")}},
	".yaml":   {Name: "YAML", Rule: CommentRule{SingleLine: markers("#")}},
	".yml":    {Name: "YAML", Rule: CommentRule{SingleLine: markers("#")}},
	".json":   {Name: "JSON", Rule: CommentRule{}},
	".md":     {Name: "Markdown", Rule: CommentRule{Blocks: blocks("<!--", "-->")}},
}

func slashRule() CommentRule {
	return CommentRule{SingleLine: markers("//"), Blocks: blocks("/*", "*/")}
}

func markers(values ...string) [][]byte {
	items := make([][]byte, 0, len(values))
	for _, value := range values {
		items = append(items, []byte(value))
	}
	return items
}

func blocks(values ...string) []BlockRule {
	items := make([]BlockRule, 0, len(values)/2)
	for index := 0; index+1 < len(values); index += 2 {
		items = append(items, BlockRule{Start: []byte(values[index]), End: []byte(values[index+1])})
	}
	return items
}

func Detect(path string) Language {
	extension := strings.ToLower(filepath.Ext(path))
	if language, ok := extensionMap[extension]; ok {
		return language
	}

	return Language{Name: "Other", Rule: CommentRule{}}
}

func KnownLanguagesFor(paths []string) []string {
	seen := make(map[string]struct{})
	for _, path := range paths {
		seen[Detect(path).Name] = struct{}{}
	}

	languages := make([]string, 0, len(seen))
	for language := range seen {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}
