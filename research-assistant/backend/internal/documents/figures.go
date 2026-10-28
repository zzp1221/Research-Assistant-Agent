package documents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var figureCaptionPattern = regexp.MustCompile(`(?i)^(figure|fig\.?|图)\s*([0-9]+|[IVX]+)\s*[.:：、]?\s*\S`)

// extractFiguresFromPageText finds figure captions ("Figure 3: ...", "图 3 ...").
// Binding to an actual image happens later in attachFigureImages.
func extractFiguresFromPageText(page int, text string) []Figure {
	lines := strings.Split(text, "\n")
	figures := make([]Figure, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !figureCaptionPattern.MatchString(trimmed) {
			continue
		}
		figures = append(figures, Figure{
			ID:      fmt.Sprintf("figure-p%d-%d", page, len(figures)+1),
			Page:    page,
			Caption: trimmed,
		})
	}
	return figures
}

// attachFigureImages exports embedded images with pdfimages and binds them to
// captions page by page. When a page has captions but no extractable image the
// figure keeps its caption and records an explicit warning instead of
// inventing a region.
func attachFigureImages(pdfPath string, figures []Figure) []Figure {
	if len(figures) == 0 {
		return figures
	}
	byPage := make(map[int][]int)
	for index, figure := range figures {
		byPage[figure.Page] = append(byPage[figure.Page], index)
	}
	pages := make([]int, 0, len(byPage))
	for page := range byPage {
		pages = append(pages, page)
	}
	sort.Ints(pages)

	pdfimages, lookupErr := exec.LookPath("pdfimages")
	for _, page := range pages {
		indices := byPage[page]
		if lookupErr != nil {
			for _, index := range indices {
				figures[index].Warning = "pdfimages is unavailable; caption only"
			}
			continue
		}
		count := countPageImages(pdfimages, pdfPath, page)
		if count <= 0 {
			for _, index := range indices {
				figures[index].Warning = "no embedded image found for caption"
			}
			continue
		}
		directory := filepath.Join(filepath.Dir(pdfPath), "figures")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			for _, index := range indices {
				figures[index].Warning = "cannot create figure directory"
			}
			continue
		}
		prefix := filepath.Join(directory, fmt.Sprintf("fig-p%d", page))
		command := exec.Command(pdfimages, "-png", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), pdfPath, prefix)
		if err := command.Run(); err != nil {
			for _, index := range indices {
				figures[index].Warning = "pdfimages failed to export page images"
			}
			continue
		}
		files := listExportedImages(directory, fmt.Sprintf("fig-p%d", page))
		for offset, index := range indices {
			if offset < len(files) {
				name := filepath.Base(files[offset])
				figures[index].ImageRef = filepath.ToSlash(filepath.Join("figures", name))
			} else {
				figures[index].Warning = "fewer embedded images than figure captions on this page"
			}
		}
	}
	return figures
}

// countPageImages returns the number of image rows reported by pdfimages -list.
func countPageImages(pdfimages, pdfPath string, page int) int {
	output, err := exec.Command(pdfimages, "-list", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), pdfPath).Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(string(output), "\n")
	count := 0
	for index, line := range lines {
		if index < 2 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			if _, err := strconv.Atoi(fields[0]); err == nil {
				count++
			}
		}
	}
	return count
}

func listExportedImages(directory, prefix string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
			continue
		}
		files = append(files, filepath.Join(directory, entry.Name()))
	}
	sort.Strings(files)
	return files
}
