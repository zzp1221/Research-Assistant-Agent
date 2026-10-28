package documents

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	tableCaptionPattern = regexp.MustCompile(`(?i)^(table|表)\s*([0-9]+|[IVX]+)\s*[.:：、]?\s*(.*)$`)
	tableGapPattern     = regexp.MustCompile(`\s{2,}`)
)

// extractTablesFromPageText finds column-aligned blocks of text and turns
// them into structured tables. The heuristic is deliberately conservative:
// a table needs at least two rows and either three columns or three
// consistent two-column rows. Anything else stays plain text.
func extractTablesFromPageText(page int, text string) []Table {
	lines := strings.Split(text, "\n")
	tables := make([]Table, 0)
	for index := 0; index < len(lines); index++ {
		cells, ok := splitTableLine(lines[index])
		if !ok {
			continue
		}
		group := [][]string{cells}
		next := index + 1
		for next < len(lines) {
			nextCells, nextOK := splitTableLine(lines[next])
			if !nextOK || !compatibleTableColumns(group, nextCells) {
				break
			}
			group = append(group, nextCells)
			next++
		}
		if !qualifiesAsTable(group) {
			index = next - 1
			continue
		}
		width := modeColumnCount(group)
		rows := normalizeTableRows(group, width)
		confidence := tableConfidence(rows)
		caption := tableCaptionAbove(lines, index)
		id := fmt.Sprintf("table-p%d-%d", page, len(tables)+1)
		tables = append(tables, Table{
			ID:         id,
			Page:       page,
			Caption:    caption,
			Rows:       rows,
			Confidence: confidence,
		})
		index = next - 1
	}
	return tables
}

// splitTableLine splits one line on runs of two or more spaces. It rejects
// lines that are too short or contain no meaningful columns.
func splitTableLine(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if utf8.RuneCountInString(trimmed) < 6 {
		return nil, false
	}
	if !tableGapPattern.MatchString(trimmed) {
		return nil, false
	}
	parts := tableGapPattern.Split(trimmed, -1)
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cells = append(cells, part)
		}
	}
	if len(cells) < 2 {
		return nil, false
	}
	return cells, true
}

// compatibleTableColumns allows a row to drift by one column from the group.
func compatibleTableColumns(group [][]string, cells []string) bool {
	mode := modeColumnCount(group)
	if mode == 0 {
		mode = len(cells)
	}
	delta := len(cells) - mode
	if delta < 0 {
		delta = -delta
	}
	return delta <= 1
}

func modeColumnCount(rows [][]string) int {
	counts := make(map[int]int, 4)
	best, bestCount := 0, 0
	for _, row := range rows {
		counts[len(row)]++
		if counts[len(row)] > bestCount || (counts[len(row)] == bestCount && len(row) > best) {
			best, bestCount = len(row), counts[len(row)]
		}
	}
	return best
}

func qualifiesAsTable(group [][]string) bool {
	if len(group) < 2 {
		return false
	}
	mode := modeColumnCount(group)
	if mode < 2 {
		return false
	}
	if mode == 2 && len(group) < 3 {
		return false
	}
	consistent := 0
	for _, row := range group {
		if rowWidth(row) == mode {
			consistent++
		}
	}
	return consistent >= len(group)-1
}

func rowWidth(row []string) int { return len(row) }

func normalizeTableRows(group [][]string, width int) [][]string {
	rows := make([][]string, 0, len(group))
	for _, row := range group {
		cells := make([]string, width)
		for index := 0; index < width && index < len(row); index++ {
			cells[index] = strings.TrimSpace(row[index])
		}
		rows = append(rows, cells)
	}
	return rows
}

func tableConfidence(rows [][]string) float64 {
	if len(rows) == 0 {
		return 0
	}
	width := modeColumnCount(rows)
	if width == 0 {
		return 0
	}
	filled, total := 0, 0
	for _, row := range rows {
		for index := 0; index < width; index++ {
			total++
			if index < len(row) && strings.TrimSpace(row[index]) != "" {
				filled++
			}
		}
	}
	fill := 0.0
	if total > 0 {
		fill = float64(filled) / float64(total)
	}
	confidence := 0.45 + 0.3*1.0 + 0.25*fill
	if confidence > 0.9 {
		confidence = 0.9
	}
	return confidence
}

func tableCaptionAbove(lines []string, tableStart int) string {
	for index := tableStart - 1; index >= 0 && index >= tableStart-3; index-- {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "" {
			continue
		}
		if match := tableCaptionPattern.FindStringSubmatch(trimmed); match != nil {
			return trimmed
		}
		break
	}
	return ""
}
