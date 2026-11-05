package wiki

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

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
	Authors      []string `json:"authors,omitempty"`
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
func (s *AliasStore) Resolve(ownerID string, meta models.PaperMetadata, record *literature.Record) (string, string, error) {
	paperID, canonicalKey, _, err := s.ResolveWithReason(ownerID, meta, record)
	return paperID, canonicalKey, err
}

// ResolveWithReason applies the documented identity rules and returns a short
// audit reason: alias:doi, alias:arxiv, alias:library, alias:title,
// title_year_author, canonical, or new.
func (s *AliasStore) ResolveWithReason(ownerID string, meta models.PaperMetadata, record *literature.Record) (string, string, string, error) {
	canonicalKey := CanonicalKey(meta, record)
	if canonicalKey == "" {
		return "", "", "", fmt.Errorf("paper identity requires a DOI, arXiv id or title")
	}
	aliases := BuildAliases(meta, record)
	title := literature.NormalizeTitle(firstNonEmpty(meta.Title, recordTitle(record)))
	year := meta.Year
	if year == 0 {
		year = recordYear(record)
	}
	authors := normalizeAuthors(append(append([]string{}, meta.Authors...), recordAuthors(record)...))
	candidateStrong := strings.HasPrefix(canonicalKey, "doi:") || strings.HasPrefix(canonicalKey, "arxiv:")

	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := s.loadLocked(ownerID)
	if err != nil {
		return "", "", "", err
	}
	paperID := ""
	reason := ""
	for _, alias := range aliases {
		existing, ok := payload.Aliases[alias]
		if !ok || existing == "" {
			continue
		}
		entry := payload.Papers[existing]
		if strings.HasPrefix(alias, "title:") {
			if !titleAliasCompatible(canonicalKey, candidateStrong, entry, authors) {
				continue
			}
		}
		paperID = existing
		switch {
		case strings.HasPrefix(alias, "doi:"):
			reason = "alias:doi"
		case strings.HasPrefix(alias, "arxiv:"):
			reason = "alias:arxiv"
		case strings.HasPrefix(alias, "library:"):
			reason = "alias:library"
		default:
			reason = "alias:title"
		}
		break
	}
	if paperID == "" && title != "" {
		for existing, entry := range payload.Papers {
			if literature.NormalizeTitle(entry.Title) != title {
				continue
			}
			if year == 0 || entry.Year == 0 || year != entry.Year {
				continue
			}
			entryStrong := entryHasStrongIdentity(entry)
			entryAuthors := normalizeAuthors(entry.Authors)
			intersect := authorsIntersect(authors, entryAuthors)
			if !intersect {
				if !candidateStrong && !entryStrong {
					continue
				}
				if candidateStrong && entryStrong && !arxivDOIPair(canonicalKey, entry.CanonicalKey) {
					continue
				}
			}
			paperID = existing
			reason = "title_year_author"
			break
		}
	}
	if paperID == "" && (strings.HasPrefix(canonicalKey, "doi:") || strings.HasPrefix(canonicalKey, "arxiv:")) {
		for existing, entry := range payload.Papers {
			if entry.CanonicalKey == canonicalKey {
				paperID = existing
				reason = "canonical"
				break
			}
		}
	}
	if paperID == "" {
		ambiguousDuplicate := false
		if !candidateStrong && title != "" {
			for _, entry := range payload.Papers {
				if literature.NormalizeTitle(entry.Title) == title && entry.Year == year && !entryHasStrongIdentity(entry) {
					ambiguousDuplicate = true
					break
				}
			}
		}
		if ambiguousDuplicate {
			// Two bare title+year records with no author evidence are treated as
			// distinct papers (CP-03). A deterministic id would collapse them, so
			// add a one-time disambiguator.
			paperID = PaperID(ownerID, canonicalKey+"|ambiguous:"+uuid.NewString())
		} else {
			paperID = PaperID(ownerID, canonicalKey)
		}
		reason = "new"
	}
	entry := payload.Papers[paperID]
	if entry.CanonicalKey == "" || identityRank(canonicalKey) < identityRank(entry.CanonicalKey) {
		entry.CanonicalKey = canonicalKey
	}
	entry.Title = firstNonEmpty(entry.Title, meta.Title, recordTitle(record))
	if entry.Year == 0 {
		entry.Year = year
	}
	entry.Authors = mergeStrings(entry.Authors, authors)
	entry.Aliases = mergeStrings(entry.Aliases, aliases)
	for _, alias := range aliases {
		payload.Aliases[alias] = paperID
	}
	payload.Papers[paperID] = entry
	if err := s.saveLocked(ownerID, payload); err != nil {
		return "", "", "", err
	}
	log.Printf("[wiki] alias resolve owner=%s paper=%s reason=%s key=%s", ownerID, paperID, reason, entry.CanonicalKey)
	return paperID, entry.CanonicalKey, reason, nil
}

func entryHasStrongIdentity(entry AliasEntry) bool {
	if strings.HasPrefix(entry.CanonicalKey, "doi:") || strings.HasPrefix(entry.CanonicalKey, "arxiv:") {
		return true
	}
	for _, alias := range entry.Aliases {
		if strings.HasPrefix(alias, "doi:") || strings.HasPrefix(alias, "arxiv:") || strings.HasPrefix(alias, "library:") {
			return true
		}
	}
	return false
}

func titleAliasCompatible(canonicalKey string, candidateStrong bool, entry AliasEntry, authors []string) bool {
	if !candidateStrong {
		return true
	}
	if !entryHasStrongIdentity(entry) {
		return true
	}
	if authorsIntersect(authors, normalizeAuthors(entry.Authors)) {
		return true
	}
	return arxivDOIPair(canonicalKey, entry.CanonicalKey)
}

func arxivDOIPair(left, right string) bool {
	return (strings.HasPrefix(left, "arxiv:") && strings.HasPrefix(right, "doi:")) ||
		(strings.HasPrefix(left, "doi:") && strings.HasPrefix(right, "arxiv:"))
}

func normalizeAuthors(authors []string) []string {
	out := make([]string, 0, len(authors))
	for _, author := range authors {
		normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(author)), " "))
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return dedupeStrings(out)
}

func authorsIntersect(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	keys := map[string]bool{}
	for _, author := range left {
		keys[author] = true
		fields := strings.Fields(author)
		if len(fields) > 1 {
			keys[fields[len(fields)-1]] = true
		}
	}
	for _, author := range right {
		if keys[author] {
			return true
		}
		fields := strings.Fields(author)
		if len(fields) > 1 && keys[fields[len(fields)-1]] {
			return true
		}
	}
	return false
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
	hasStrongIdentity := normalizeDOI(firstNonEmpty(recordDOI(record), meta.DOI)) != "" ||
		strings.TrimSpace(paperid.StripVersion(firstNonEmpty(recordArxiv(record), meta.ArxivID))) != "" ||
		(record != nil && strings.TrimSpace(record.ID) != "")
	if title := literature.NormalizeTitle(firstNonEmpty(meta.Title, recordTitle(record))); title != "" && hasStrongIdentity {
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
