package counter

type Counts struct {
	Files    int `json:"files"`
	Code     int `json:"code"`
	Comments int `json:"comments"`
	Blanks   int `json:"blanks"`
	Total    int `json:"total"`
}

type SkipReason string

const (
	SkipBinary     SkipReason = "binary"
	SkipPermission SkipReason = "permission"
	SkipOther      SkipReason = "other"
)

type FileResult struct {
	Path       string
	Language   string
	Size       int64
	Counts     Counts
	Skipped    bool
	SkipReason SkipReason
	Err        error
}

type Totals struct {
	ByLanguage   map[string]Counts       `json:"languages"`
	Overall      Counts                  `json:"overall"`
	Skipped      map[SkipReason]int      `json:"skipped"`
	Files        []FileResult            `json:"-"`
	SkippedFiles []FileResult            `json:"-"`
	Errors       map[SkipReason][]string `json:"-"`
}

func NewTotals() Totals {
	return Totals{
		ByLanguage: make(map[string]Counts),
		Skipped:    make(map[SkipReason]int),
		Errors:     make(map[SkipReason][]string),
	}
}

func (totals *Totals) Add(result FileResult) {
	if result.Skipped {
		totals.Skipped[result.SkipReason]++
		totals.SkippedFiles = append(totals.SkippedFiles, result)
		if result.Path != "" {
			totals.Errors[result.SkipReason] = append(totals.Errors[result.SkipReason], result.Path)
		}
		return
	}

	totals.Files = append(totals.Files, result)

	counts := totals.ByLanguage[result.Language]
	counts.Files += result.Counts.Files
	counts.Code += result.Counts.Code
	counts.Comments += result.Counts.Comments
	counts.Blanks += result.Counts.Blanks
	counts.Total += result.Counts.Total
	totals.ByLanguage[result.Language] = counts

	totals.Overall.Files += result.Counts.Files
	totals.Overall.Code += result.Counts.Code
	totals.Overall.Comments += result.Counts.Comments
	totals.Overall.Blanks += result.Counts.Blanks
	totals.Overall.Total += result.Counts.Total
}
