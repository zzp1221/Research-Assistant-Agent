package wiki

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/paperid"
)

// CanonicalKey returns the documented identity priority:
// DOI > arXiv > normalized title + year.
func CanonicalKey(meta models.PaperMetadata, record *literature.Record) string {
	doi := normalizeDOI(firstNonEmpty(recordDOI(record), meta.DOI))
	if doi != "" {
		return "doi:" + doi
	}
	arxiv := strings.ToLower(paperid.StripVersion(firstNonEmpty(recordArxiv(record), meta.ArxivID)))
	if arxiv != "" {
		return "arxiv:" + arxiv
	}
	title := literature.NormalizeTitle(firstNonEmpty(meta.Title, recordTitle(record)))
	if title == "" {
		return ""
	}
	year := meta.Year
	if year == 0 {
		year = recordYear(record)
	}
	return fmt.Sprintf("title:%s:%d", title, year)
}

// PaperID derives a stable owner-scoped id. owner_id participates so two owners
// uploading the same paper never share a canonical id.
func PaperID(ownerID, canonicalKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ownerID) + "|" + strings.TrimSpace(canonicalKey)))
	return "paper-" + hex.EncodeToString(sum[:])[:24]
}

func normalizeDOI(raw string) string {
	doi := strings.TrimSpace(raw)
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/", "doi:"} {
		doi = strings.TrimPrefix(strings.ToLower(doi), prefix)
	}
	return strings.TrimSpace(doi)
}

func recordDOI(record *literature.Record) string {
	if record == nil {
		return ""
	}
	return record.DOI
}

func recordArxiv(record *literature.Record) string {
	if record == nil {
		return ""
	}
	return record.ArxivID
}

func recordTitle(record *literature.Record) string {
	if record == nil {
		return ""
	}
	return record.Title
}

func recordYear(record *literature.Record) int {
	if record == nil {
		return 0
	}
	return record.Year
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// AliasEntry is one paper's alias record.
type AliasEntry struct {
	CanonicalKey string   `json:"canonical_key"`
	Aliases      []string `json:"aliases"`
	Title        string   `json:"title,omitempty"`
	Year         int      `json:"year,omitempty"`
	MergedFrom   []string `json:"merged_from,omitempty"`
}

type aliasFile struct {
	Version int                   `json:"version"`
	Aliases map[string]string     `json:"aliases"`
	Papers  map[string]AliasEntry `json:"papers"`
}

// AliasStore persists the owner-scoped multi-to-one alias map. It is the first
// data source for paper identity together with literature.LibraryStore.
type AliasStore struct {
	mu   sync.Mutex
	root string
}

// NewAliasStore opens (or creates) the alias root.
func NewAliasStore(root string) *AliasStore {
	return &AliasStore{root: strings.TrimSpace(root)}
}

func (s *AliasStore) ownerDir(ownerID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ownerID)))
	return filepath.Join(s.root, "owners", hex.EncodeToString(sum[:16]))
}

func (s *AliasStore) ownerFile(ownerID string) string {
	return filepath.Join(s.ownerDir(ownerID), "aliases.json")
}

func (s *AliasStore) loadLocked(ownerID string) (aliasFile, error) {
	payload := aliasFile{Version: 1, Aliases: map[string]string{}, Papers: map[string]AliasEntry{}}
	raw, err := os.ReadFile(s.ownerFile(ownerID))
	if err != nil {
		if os.IsNotExist(err) {
			return payload, nil
		}
		return payload, err
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("decode wiki aliases: %w", err)
	}
	if payload.Aliases == nil {
		payload.Aliases = map[string]string{}
	}
	if payload.Papers == nil {
		payload.Papers = map[string]AliasEntry{}
	}
	return payload, nil
}

func (s *AliasStore) saveLocked(ownerID string, payload aliasFile) error {
	directory := s.ownerDir(ownerID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	target := s.ownerFile(ownerID)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, target)
}

// Resolve returns the paper id for a metadata record, registering all aliases.
// It merges into an existing paper when a DOI/arXiv alias matches, or when the
// normalized title matches and the year differs by at most one. A larger year
// gap keeps the papers separate so distinct editions do not silently merge.
func (s *AliasStore) Resolve(ownerID string, meta models.PaperMetadata, record *literature.Record) (string, string, error) {
	canonicalKey := CanonicalKey(meta, record)
	if canonicalKey == "" {
		return "", "", fmt.Errorf("paper identity requires a DOI, arXiv id or title")
	}
	aliases := BuildAliases(meta, record)
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := s.loadLocked(ownerID)
	if err != nil {
		return "", "", err
	}
	paperID := ""
	for _, alias := range aliases {
		if existing, ok := payload.Aliases[alias]; ok && existing != "" {
			paperID = existing
			break
		}
	}
	year := meta.Year
	if year == 0 {
		year = recordYear(record)
	}
	if paperID == "" {
		// Title+year candidate merge: only when the same normalized title exists
		// and the years are compatible.
		titleAlias := "title:" + literature.NormalizeTitle(firstNonEmpty(meta.Title, recordTitle(record)))
		for alias, existing := range payload.Aliases {
			if !strings.HasPrefix(alias, titleAlias+":") {
				continue
			}
			entry := payload.Papers[existing]
			if entry.Year == 0 || year == 0 || absInt(entry.Year-year) <= 1 {
				paperID = existing
				break
			}
		}
	}
	if paperID == "" {
		for existing, entry := range payload.Papers {
			if entry.CanonicalKey == canonicalKey {
				paperID = existing
				break
			}
		}
	}
	if paperID == "" {
		paperID = PaperID(ownerID, canonicalKey)
	}
	entry := payload.Papers[paperID]
	if entry.CanonicalKey == "" || identityRank(canonicalKey) < identityRank(entry.CanonicalKey) {
		entry.CanonicalKey = canonicalKey
	}
	entry.Title = firstNonEmpty(entry.Title, meta.Title, recordTitle(record))
	if entry.Year == 0 {
		entry.Year = year
	}
	entry.Aliases = mergeStrings(entry.Aliases, aliases)
	for _, alias := range aliases {
		payload.Aliases[alias] = paperID
	}
	payload.Papers[paperID] = entry
	if err := s.saveLocked(ownerID, payload); err != nil {
		return "", "", err
	}
	return paperID, entry.CanonicalKey, nil
}

// BuildAliases returns every key that should map to the same paper.
func BuildAliases(meta models.PaperMetadata, record *literature.Record) []string {
	aliases := make([]string, 0, 6)
	if doi := normalizeDOI(firstNonEmpty(recordDOI(record), meta.DOI)); doi != "" {
		aliases = append(aliases, "doi:"+doi)
	}
	if arxiv := strings.ToLower(paperid.StripVersion(firstNonEmpty(recordArxiv(record), meta.ArxivID))); arxiv != "" {
		aliases = append(aliases, "arxiv:"+arxiv)
	}
	if title := literature.NormalizeTitle(firstNonEmpty(meta.Title, recordTitle(record))); title != "" {
		year := meta.Year
		if year == 0 {
			year = recordYear(record)
		}
		aliases = append(aliases, fmt.Sprintf("title:%s:%d", title, year))
	}
	if record != nil && strings.TrimSpace(record.ID) != "" {
		aliases = append(aliases, "library:"+strings.TrimSpace(record.ID))
	}
	return dedupeStrings(aliases)
}

// identityRank prefers DOI over arXiv over title when upgrading an entry key.
func identityRank(canonicalKey string) int {
	switch {
	case strings.HasPrefix(canonicalKey, "doi:"):
		return 0
	case strings.HasPrefix(canonicalKey, "arxiv:"):
		return 1
	default:
		return 2
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func mergeStrings(existing, additions []string) []string {
	seen := make(map[string]bool, len(existing)+len(additions))
	out := make([]string, 0, len(existing)+len(additions))
	for _, value := range append(append([]string{}, existing...), additions...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
