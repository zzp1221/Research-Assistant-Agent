package scheduler

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"research-assistant-backend/internal/models"
)

const (
	datasetProbeMaxBytes = 5 << 20
	datasetProbeMaxRows  = 200000
)

// DatasetShapeProfile is the lightweight, explainable shape of one data file in
// a repository workspace. It exists so a label table can never be silently
// executed as an edge list again (D7).
type DatasetShapeProfile struct {
	File            string   `json:"file"`
	Rows            int      `json:"rows"`
	Cols            int      `json:"cols"`
	Col0UniqueRatio float64  `json:"col0_unique_ratio"`
	Col1Max         int      `json:"col1_max"`
	Col1Unique      int      `json:"col1_unique"`
	Shape           string   `json:"shape"` // edge_list / node_label / feature_matrix / unknown
	Sample          []string `json:"sample,omitempty"`
}

var datasetExtensions = map[string]struct{}{
	".txt": {}, ".csv": {}, ".tsv": {}, ".edges": {}, ".edgelist": {},
}

var graphDatasetSignals = []string{
	"louvain", "community detection", "community structure", "community_louvain",
	"modularity", "networkx", "edge list", "edge_list", "edgelist", "adjacency",
	"construct_graph", "graph clustering",
}

// probeWorkspaceDatasets scans bounded text data files under the workspace and
// classifies their shape. It is intentionally deterministic and cheap.
func probeWorkspaceDatasets(workspacePath string) []DatasetShapeProfile {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return nil
	}
	profiles := make([]DatasetShapeProfile, 0, 8)
	_ = filepath.WalkDir(workspacePath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", "node_modules", ".venv", "venv", "dist", "build", ".research-assistant":
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := datasetExtensions[strings.ToLower(filepath.Ext(name))]; !ok {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.Size() == 0 || info.Size() > datasetProbeMaxBytes {
			return nil
		}
		if profile, ok := probeDatasetFile(workspacePath, path); ok {
			profiles = append(profiles, profile)
		}
		return nil
	})
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].Rows > profiles[j].Rows })
	if len(profiles) > 20 {
		profiles = profiles[:20]
	}
	return profiles
}

func probeDatasetFile(workspacePath, path string) (DatasetShapeProfile, bool) {
	file, err := os.Open(path)
	if err != nil {
		return DatasetShapeProfile{}, false
	}
	defer file.Close()

	rows := make([][]string, 0, 1024)
	samples := make([]string, 0, 3)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "%") {
			continue
		}
		fields := splitDatasetFields(line)
		if len(fields) < 2 {
			continue
		}
		rows = append(rows, fields)
		if len(samples) < 3 {
			samples = append(samples, line)
		}
		if len(rows) >= datasetProbeMaxRows {
			break
		}
	}
	if len(rows) < 3 {
		return DatasetShapeProfile{}, false
	}
	relative, relErr := filepath.Rel(workspacePath, path)
	if relErr != nil {
		relative = path
	}
	profile := DatasetShapeProfile{
		File:   filepath.ToSlash(relative),
		Rows:   len(rows),
		Cols:   len(rows[0]),
		Sample: samples,
	}
	profile.Shape = classifyDatasetRows(rows, &profile)
	return profile, true
}

func splitDatasetFields(line string) []string {
	if strings.Contains(line, "\t") {
		return strings.Split(line, "\t")
	}
	if strings.Contains(line, ",") {
		return strings.Split(line, ",")
	}
	return strings.Fields(line)
}

func classifyDatasetRows(rows [][]string, profile *DatasetShapeProfile) string {
	cols := len(rows[0])
	allInt := true
	ints := make([][]int, 0, cols)
	for col := 0; col < cols; col++ {
		values := make([]int, 0, len(rows))
		for _, row := range rows {
			if col >= len(row) {
				allInt = false
				break
			}
			value, err := strconv.Atoi(strings.TrimSpace(row[col]))
			if err != nil {
				allInt = false
				break
			}
			values = append(values, value)
		}
		if !allInt {
			break
		}
		ints = append(ints, values)
	}
	if allInt && cols >= 2 && len(ints[0]) == len(rows) {
		col0 := ints[0]
		col1 := ints[1]
		col0Unique := uniqueInts(col0)
		col1Unique := uniqueInts(col1)
		profile.Col0UniqueRatio = float64(len(col0Unique)) / float64(len(col0))
		profile.Col1Unique = len(col1Unique)
		profile.Col1Max = maxInt(col1)
		if profile.Col0UniqueRatio == 1.0 && profile.Col1Unique <= 100 && profile.Col1Max < len(rows)/2 {
			return "node_label"
		}
		if looksLikeEdgeList(col0, col1) {
			return "edge_list"
		}
	}
	// Feature matrices have at least three numeric columns.
	if cols >= 3 && allFloats(rows) {
		return "feature_matrix"
	}
	return "unknown"
}

func looksLikeEdgeList(col0, col1 []int) bool {
	if len(col0) == 0 || len(col0) != len(col1) {
		return false
	}
	set0 := uniqueInts(col0)
	set1 := uniqueInts(col1)
	overlap := 0
	for value := range set0 {
		if _, ok := set1[value]; ok {
			overlap++
		}
	}
	if overlap == 0 {
		return false
	}
	// Edge lists usually repeat endpoints; label tables have a unique first column.
	if len(set0) == len(col0) {
		return false
	}
	strictlyIncreasing := true
	for i := 1; i < len(col0); i++ {
		if col0[i] <= col0[i-1] {
			strictlyIncreasing = false
			break
		}
	}
	return !strictlyIncreasing
}

func uniqueInts(values []int) map[int]struct{} {
	out := make(map[int]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func maxInt(values []int) int {
	maximum := 0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func allFloats(rows [][]string) bool {
	for _, row := range rows {
		for _, cell := range row {
			if _, err := strconv.ParseFloat(strings.TrimSpace(cell), 64); err != nil {
				return false
			}
		}
	}
	return true
}

// datasetShapeGate blocks execution when the selected entry code consumes a
// node->label table as if it were an edge list. It returns nil when the shape
// is compatible or the task is not a graph algorithm.
func datasetShapeGate(workspacePath, selectedCodeFile string, task *models.Task, profiles []DatasetShapeProfile) *models.MissingRequirement {
	if len(profiles) == 0 {
		return nil
	}
	codeText := readBoundedText(selectedCodeFile, 1<<20)
	contextText := strings.ToLower(codeText + "\n" + taskTextForDatasetGate(task))
	graphSignal := false
	for _, signal := range graphDatasetSignals {
		if strings.Contains(contextText, signal) {
			graphSignal = true
			break
		}
	}
	if !graphSignal {
		return nil
	}
	candidates := make([]DatasetShapeProfile, 0, len(profiles))
	codeLower := strings.ToLower(codeText)
	for _, profile := range profiles {
		base := strings.ToLower(filepath.Base(profile.File))
		if base != "" && strings.Contains(codeLower, base) {
			candidates = append(candidates, profile)
		}
	}
	if len(candidates) == 0 {
		candidates = profiles
	}
	for _, profile := range candidates {
		if profile.Shape != "node_label" {
			continue
		}
		return &models.MissingRequirement{
			Kind:        "dataset",
			Name:        profile.File,
			Detail:      fmt.Sprintf("%s 是节点→标签表（%d 行，第一列唯一，第二列仅 %d 个取值），不是边表", profile.File, profile.Rows, profile.Col1Unique),
			Remediation: "请提供边表（两列节点 ID），或改用带 edges.txt 的仓库。",
		}
	}
	return nil
}

func readBoundedText(path string, limit int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > int64(limit) {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func taskTextForDatasetGate(task *models.Task) string {
	if task == nil {
		return ""
	}
	parts := []string{task.Name, task.Description}
	for _, key := range []string{"paper_title", "repo_url"} {
		parts = append(parts, taskInputValue(task, key))
	}
	return strings.Join(parts, "\n")
}
