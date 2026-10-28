package webread

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Extracted is the readable portion of one HTML page.
type Extracted struct {
	Title string
	Text  string
	Links []string
}

var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "main": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "ul": true, "ol": true, "pre": true, "blockquote": true,
	"table": true, "tr": true, "td": true, "th": true, "dt": true, "dd": true,
	"br": true, "figure": true, "figcaption": true,
}

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "iframe": true, "svg": true,
	"form": true, "nav": true, "header": true, "footer": true, "aside": true,
	"button": true, "input": true, "select": true, "textarea": true,
}

// ExtractHTML applies a readability-style scoring pass to an HTML document.
func ExtractHTML(raw []byte, base *url.URL) Extracted {
	document, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return Extracted{Text: cleanExtractedText(string(raw)), Links: []string{}}
	}
	title := findTitle(document)
	stripUnwanted(document)

	best := bestCandidate(document)
	text := ""
	if best != nil {
		text = renderText(best)
	}
	if utf8.RuneCountInString(strings.TrimSpace(text)) < 80 {
		if body := findFirst(document, "body"); body != nil {
			text = renderText(body)
		}
	}
	text = cleanExtractedText(text)
	links := collectLinks(best, base)
	if best == nil {
		links = collectLinks(document, base)
	}
	if title == "" {
		if h1 := findFirst(document, "h1"); h1 != nil {
			title = cleanExtractedText(textContent(h1))
		}
	}
	return Extracted{Title: strings.TrimSpace(title), Text: text, Links: links}
}

func findTitle(root *html.Node) string {
	if node := findFirst(root, "title"); node != nil {
		if text := strings.TrimSpace(textContent(node)); text != "" {
			return text
		}
	}
	// og:title meta
	var result string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if result != "" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "meta" {
			property := ""
			content := ""
			for _, attr := range node.Attr {
				switch strings.ToLower(attr.Key) {
				case "property", "name":
					property = strings.ToLower(attr.Val)
				case "content":
					content = attr.Val
				}
			}
			if property == "og:title" && strings.TrimSpace(content) != "" {
				result = strings.TrimSpace(content)
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return result
}

func stripUnwanted(node *html.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && skipTags[strings.ToLower(child.Data)] {
			node.RemoveChild(child)
		} else {
			stripUnwanted(child)
		}
		child = next
	}
}

func findFirst(root *html.Node, tag string) *html.Node {
	if root == nil {
		return nil
	}
	if root.Type == html.ElementNode && strings.ToLower(root.Data) == tag {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, tag); found != nil {
			return found
		}
	}
	return nil
}

// bestCandidate scores every element by text length, punctuation and link
// density, then returns the highest-scoring readable container.
func bestCandidate(root *html.Node) *html.Node {
	bestScore := 0.0
	var best *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			text := textContent(node)
			runes := utf8.RuneCountInString(strings.TrimSpace(text))
			if runes >= 80 {
				linkRunes := utf8.RuneCountInString(linkText(node))
				density := 0.0
				if runes > 0 {
					density = float64(linkRunes) / float64(runes)
				}
				score := float64(runes)/100.0 + float64(strings.Count(text, ","))*3.0 + tagWeight(node.Data)
				score *= (1 - density)
				if density > 0.6 {
					score *= 0.3
				}
				if score > bestScore {
					bestScore = score
					best = node
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return best
}

func tagWeight(tag string) float64 {
	switch strings.ToLower(tag) {
	case "article", "main":
		return 30
	case "section", "div":
		return 5
	case "td", "blockquote", "pre":
		return 3
	case "body":
		return 1
	default:
		return 0
	}
}

func textContent(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
			return
		}
		if current.Type == html.ElementNode && skipTags[strings.ToLower(current.Data)] {
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func linkText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && strings.ToLower(current.Data) == "a" {
			builder.WriteString(textContent(current))
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func renderText(root *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text == "" {
				return
			}
			if builder.Len() > 0 && !strings.HasSuffix(builder.String(), " ") && !strings.HasSuffix(builder.String(), "\n") {
				builder.WriteByte(' ')
			}
			builder.WriteString(text)
			return
		}
		if node.Type != html.ElementNode {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			return
		}
		tag := strings.ToLower(node.Data)
		if skipTags[tag] {
			return
		}
		if blockTags[tag] {
			builder.WriteString("\n\n")
			if tag == "li" {
				builder.WriteString("• ")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if blockTags[tag] {
			builder.WriteString("\n\n")
		}
	}
	walk(root)
	return builder.String()
}

func collectLinks(root *html.Node, base *url.URL) []string {
	if root == nil {
		return []string{}
	}
	seen := map[string]bool{}
	links := make([]string, 0, 32)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && strings.ToLower(node.Data) == "a" {
			for _, attr := range node.Attr {
				if strings.EqualFold(attr.Key, "href") {
					href := strings.TrimSpace(attr.Val)
					if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
						continue
					}
					resolved, err := url.Parse(href)
					if err != nil {
						continue
					}
					if base != nil {
						resolved = base.ResolveReference(resolved)
					}
					if resolved.Scheme != "http" && resolved.Scheme != "https" {
						continue
					}
					key := resolved.String()
					if seen[key] {
						continue
					}
					seen[key] = true
					links = append(links, key)
					if len(links) >= 100 {
						return
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return links
}

// cleanExtractedText normalizes whitespace while preserving paragraph breaks.
func cleanExtractedText(text string) string {
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		line = strings.TrimLeftFunc(line, unicode.IsSpace)
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
			cleaned = append(cleaned, "")
			continue
		}
		blank = 0
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
