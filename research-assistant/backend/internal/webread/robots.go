package webread

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type robotsRule struct {
	allow   bool
	pattern string
}

// allowedByRobots fetches and caches robots.txt for the URL's origin.
func (f *Fetcher) allowedByRobots(ctx context.Context, target *url.URL) (bool, error) {
	origin := target.Scheme + "://" + target.Host
	now := time.Now()
	f.mu.Lock()
	if entry, ok := f.robots[origin]; ok && now.Before(entry.expires) {
		rules := entry.rules
		f.mu.Unlock()
		return evaluateRobots(rules, target.Path), nil
	}
	f.mu.Unlock()

	robotsURL := origin + "/robots.txt"
	request, err := f.httpRequest(ctx, robotsURL)
	if err != nil {
		return true, err
	}
	response, err := f.client.Do(request)
	if err != nil {
		return true, err
	}
	defer response.Body.Close()
	if response.StatusCode == 404 || response.StatusCode == 410 {
		f.mu.Lock()
		f.robots[origin] = robotsEntry{rules: nil, expires: now.Add(10 * time.Minute)}
		f.mu.Unlock()
		return true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return true, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return true, err
	}
	rules := parseRobots(string(body), f.config.UserAgent)
	f.mu.Lock()
	f.robots[origin] = robotsEntry{rules: rules, expires: now.Add(10 * time.Minute)}
	f.mu.Unlock()
	return evaluateRobots(rules, target.Path), nil
}

func (f *Fetcher) httpRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", f.config.UserAgent)
	return request, nil
}

// parseRobots returns the rules of the most specific matching group.
func parseRobots(raw, userAgent string) []robotsRule {
	groups := make(map[string][]robotsRule)
	currentAgents := []string{}
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			currentAgents = append(currentAgents, strings.ToLower(value))
		case "disallow", "allow":
			rule := robotsRule{allow: key == "allow", pattern: value}
			for _, agent := range currentAgents {
				groups[agent] = append(groups[agent], rule)
			}
		}
	}
	ua := strings.ToLower(userAgent)
	best := ""
	for agent := range groups {
		if agent == "*" {
			if best == "" {
				best = agent
			}
			continue
		}
		if strings.Contains(ua, agent) && len(agent) > len(best) {
			best = agent
		}
	}
	if best == "" {
		return nil
	}
	return groups[best]
}

// evaluateRobots applies longest-match precedence between Allow and Disallow.
func evaluateRobots(rules []robotsRule, path string) bool {
	if path == "" {
		path = "/"
	}
	bestLength := -1
	allowed := true
	for _, rule := range rules {
		if rule.pattern == "" {
			continue
		}
		if !robotsPatternMatches(rule.pattern, path) {
			continue
		}
		if len(rule.pattern) > bestLength || (len(rule.pattern) == bestLength && rule.allow) {
			bestLength = len(rule.pattern)
			allowed = rule.allow
		}
	}
	return allowed
}

func robotsPatternMatches(pattern, path string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "$") {
		return path == strings.TrimSuffix(pattern, "$")
	}
	return strings.HasPrefix(path, pattern)
}
