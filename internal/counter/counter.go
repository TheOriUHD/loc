package counter

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"unicode/utf8"

	"github.com/philippb/loc/internal/lang"
)

const sniffSize = 8 * 1024
const readerBufferSize = 64 * 1024

func CountFile(path string) FileResult {
	file, err := os.Open(path)
	if err != nil {
		return FileResult{Path: path, Skipped: true, SkipReason: classifyOpenError(err), Err: err}
	}
	defer file.Close()

	var size int64
	if info, err := file.Stat(); err == nil {
		size = info.Size()
	}

	reader := bufio.NewReaderSize(file, readerBufferSize)
	sniff, readErr := reader.Peek(sniffSize)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return FileResult{Path: path, Size: size, Skipped: true, SkipReason: SkipOther, Err: readErr}
	}

	if isBinary(sniff) {
		return FileResult{Path: path, Size: size, Skipped: true, SkipReason: SkipBinary}
	}

	language := lang.Detect(path)
	counts, err := countReader(reader, language.Rule)
	if err != nil {
		return FileResult{Path: path, Size: size, Skipped: true, SkipReason: SkipOther, Err: err}
	}
	counts.Files = 1

	return FileResult{Path: path, Language: language.Name, Size: size, Counts: counts}
}

func isBinary(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	return !utf8.Valid(sample)
}

func countReader(reader *bufio.Reader, rule lang.CommentRule) (Counts, error) {
	state := commentState{}
	var counts Counts
	var longLine []byte

	for {
		fragment, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			longLine = append(longLine, fragment...)
			continue
		}

		if len(longLine) > 0 {
			longLine = append(longLine, fragment...)
			classifyAndAdd(longLine, rule, &state, &counts)
			longLine = longLine[:0]
		} else if len(fragment) > 0 {
			classifyAndAdd(fragment, rule, &state, &counts)
		}

		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return counts, nil
		}
		return counts, err
	}
}

func classifyAndAdd(line []byte, rule lang.CommentRule, state *commentState, counts *Counts) {
	counts.Total++
	switch classifyLine(trimRight(line), rule, state) {
	case lineBlank:
		counts.Blanks++
	case lineComment:
		counts.Comments++
	case lineCode:
		counts.Code++
	}
}

func trimRight(line []byte) []byte {
	for len(line) > 0 {
		switch line[len(line)-1] {
		case ' ', '\t', '\r', '\n':
			line = line[:len(line)-1]
		default:
			return line
		}
	}
	return line
}

func classifyOpenError(err error) SkipReason {
	if errors.Is(err, os.ErrPermission) {
		return SkipPermission
	}
	return SkipOther
}

type lineType int

const (
	lineBlank lineType = iota
	lineComment
	lineCode
)

type commentState struct {
	inBlock  bool
	blockEnd []byte
}

func classifyLine(line []byte, rule lang.CommentRule, state *commentState) lineType {
	if len(line) == 0 {
		return lineBlank
	}

	if hasCodeOutsideComments(line, rule, state) {
		return lineCode
	}
	return lineComment
}

func hasCodeOutsideComments(line []byte, rule lang.CommentRule, state *commentState) bool {
	remainder := line
	sawCode := false

	for {
		if state.inBlock {
			endIndex := bytes.Index(remainder, state.blockEnd)
			if endIndex == -1 {
				return sawCode
			}
			remainder = remainder[endIndex+len(state.blockEnd):]
			state.inBlock = false
			continue
		}

		trimmedLeft := trimLeft(remainder)
		if len(trimmedLeft) == 0 {
			return sawCode
		}

		marker, ok := nextMarker(trimmedLeft, rule)
		if !ok {
			return true
		}

		if marker.index > 0 && len(bytes.TrimSpace(trimmedLeft[:marker.index])) != 0 {
			sawCode = true
		}

		if marker.singleLine {
			return sawCode
		}

		afterStart := trimmedLeft[marker.index+len(marker.start):]
		endIndex := bytes.Index(afterStart, marker.end)
		if endIndex == -1 {
			state.inBlock = true
			state.blockEnd = marker.end
			return sawCode
		}

		remainder = afterStart[endIndex+len(marker.end):]
	}
}

func trimLeft(line []byte) []byte {
	for len(line) > 0 {
		switch line[0] {
		case ' ', '\t':
			line = line[1:]
		default:
			return line
		}
	}
	return line
}

type markerMatch struct {
	index      int
	start      []byte
	end        []byte
	singleLine bool
}

func nextMarker(line []byte, rule lang.CommentRule) (markerMatch, bool) {
	best := markerMatch{index: -1}

	for _, marker := range rule.SingleLine {
		index := bytes.Index(line, marker)
		if index >= 0 && (best.index == -1 || index < best.index) {
			best = markerMatch{index: index, start: marker, singleLine: true}
		}
	}

	for _, block := range rule.Blocks {
		index := bytes.Index(line, block.Start)
		if index >= 0 && (best.index == -1 || index < best.index) {
			best = markerMatch{index: index, start: block.Start, end: block.End}
		}
	}

	if best.index == -1 {
		return markerMatch{}, false
	}
	return best, true
}
