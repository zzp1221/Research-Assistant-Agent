package literature

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
	"time"
)

// Record is one saved literature entry in the owner-scoped library.
type Record struct {
	ID               string    `json:"id"`
	Source           string    `json:"source"`
	ExternalID       string    `json:"external_id,omitempty"`
	Title            string    `json:"title"`
	Authors          []string  `json:"authors,omitempty"`
	Year             int       `json:"year,omitempty"`
	Venue            string    `json:"venue,omitempty"`
	Abstract         string    `json:"abstract,omitempty"`
	DOI              string    `json:"doi,omitempty"`
	ArxivID          string    `json:"arxiv_id,omitempty"`
	URL              string    `json:"url,omitempty"`
	PDFURL           string    `json:"pdf_url,omitempty"`
	Citations        int       `json:"citations,omitempty"`
	OpenAccess       bool      `json:"open_access,omitempty"`
	License          string    `json:"license,omitempty"`
	Tags             []string  `json:"tags,omitempty"`
	UploadID         string    `json:"upload_id,omitempty"`
	AttachedSessions []string  `json:"attached_sessions,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type libraryFile struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// LibraryStore persists owner-scoped literature records in JSON files. The
// process-wide mutex serializes read-modify-write cycles; writes are atomic.
type LibraryStore struct {
	mu   sync.Mutex
	root string
}

// NewLibraryStore builds a store rooted at dir. An empty dir selects
// LITERATURE_LIBRARY_DIR or <PLAN_STORE_PATH directory>/literature-library.
func NewLibraryStore(dir string) *LibraryStore {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = DefaultLibraryDir()
	}
	return &LibraryStore{root: dir}
}

// DefaultLibraryDir returns the configured library directory.
func DefaultLibraryDir() string {
	if configured := strings.TrimSpace(os.Getenv("LITERATURE_LIBRARY_DIR")); configured != "" {
		return configured
	}
	if storePath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); storePath != "" {
		return filepath.Join(filepath.Dir(storePath), "literature-library")
	}
	return filepath.Join(os.TempDir(), "research-assistant-literature-library")
}

func (s *LibraryStore) ownerDir(ownerID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ownerID)))
	return filepath.Join(s.root, hex.EncodeToString(sum[:16]))
}

func (s *LibraryStore) ownerFile(ownerID string) string {
	return filepath.Join(s.ownerDir(ownerID), "records.json")
}

func (s *LibraryStore) loadLocked(ownerID string) ([]Record, error) {
	raw, err := os.ReadFile(s.ownerFile(ownerID))
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, err
	}
	var payload libraryFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode literature library: %w", err)
	}
	if payload.Records == nil {
		payload.Records = []Record{}
	}
	return payload.Records, nil
}

func (s *LibraryStore) saveLocked(ownerID string, records []Record) error {
	directory := s.ownerDir(ownerID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(libraryFile{Version: 1, Records: records})
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

// List returns owner records matching query (title/abstract/author) and tag,
// newest first. limit defaults to 50 and is capped at 200.
func (s *LibraryStore) List(ownerID, query, tag string, limit int) ([]Record, error) {
	return s.ListPage(ownerID, query, tag, limit, 0)
}

// ListPage is List with an offset cursor.
func (s *LibraryStore) ListPage(ownerID, query, tag string, limit, offset int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	tag = strings.ToLower(strings.TrimSpace(tag))
	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if query != "" && !recordMatches(record, query) {
			continue
		}
		if tag != "" && !recordHasTag(record, tag) {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].Title < filtered[j].Title
	})
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return []Record{}, nil
	}
	filtered = filtered[offset:]
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func recordMatches(record Record, query string) bool {
	haystack := strings.ToLower(record.Title + " " + record.Abstract + " " + strings.Join(record.Authors, " "))
	return strings.Contains(haystack, query)
}

func recordHasTag(record Record, tag string) bool {
	for _, existing := range record.Tags {
		if strings.EqualFold(strings.TrimSpace(existing), tag) {
			return true
		}
	}
	return false
}

// Get returns one record by ID.
func (s *LibraryStore) Get(ownerID, id string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, false, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, true, nil
		}
	}
	return Record{}, false, nil
}

// FindByDOI returns the owner's record for a DOI (case-insensitive).
func (s *LibraryStore) FindByDOI(ownerID, doi string) (Record, bool, error) {
	needle := strings.ToLower(normalizeDOI(doi))
	if needle == "" {
		return Record{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, false, err
	}
	for _, record := range records {
		if strings.ToLower(normalizeDOI(record.DOI)) == needle {
			return record, true, nil
		}
	}
	return Record{}, false, nil
}

// FindByArxiv returns the owner's record for an arXiv ID.
func (s *LibraryStore) FindByArxiv(ownerID, arxivID string) (Record, bool, error) {
	needle := strings.ToLower(strings.TrimSpace(arxivID))
	if needle == "" {
		return Record{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, false, err
	}
	for _, record := range records {
		if strings.ToLower(strings.TrimSpace(record.ArxivID)) == needle {
			return record, true, nil
		}
	}
	return Record{}, false, nil
}

// Upsert inserts or merges a record. It returns the stored record and whether
// it was newly created.
func (s *LibraryStore) Upsert(ownerID string, record Record) (Record, bool, error) {
	if strings.TrimSpace(record.Title) == "" {
		return Record{}, false, fmt.Errorf("literature record title is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, false, err
	}
	now := time.Now().UTC()
	record.DOI = strings.ToLower(normalizeDOI(record.DOI))
	if record.ID == "" {
		record.ID = recordID(record)
	}
	for index := range records {
		if records[index].ID != record.ID && !sameRecord(records[index], record) {
			continue
		}
		merged := mergeRecord(records[index], record)
		merged.UpdatedAt = now
		records[index] = merged
		if err := s.saveLocked(ownerID, records); err != nil {
			return Record{}, false, err
		}
		return merged, false, nil
	}
	record.CreatedAt = now
	record.UpdatedAt = now
	if record.Tags == nil {
		record.Tags = []string{}
	}
	records = append(records, record)
	if err := s.saveLocked(ownerID, records); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

// UpsertResult saves a search/import result, optionally linked to an upload.
func (s *LibraryStore) UpsertResult(ownerID string, result Result, uploadID string) (Record, bool, error) {
	record := Record{
		Source:     result.Source,
		ExternalID: firstNonEmpty(result.ArxivID, result.DOI, result.ID),
		Title:      result.Title,
		Authors:    append([]string(nil), result.Authors...),
		Year:       result.Year,
		Venue:      result.Venue,
		Abstract:   result.Abstract,
		DOI:        result.DOI,
		ArxivID:    result.ArxivID,
		URL:        result.URL,
		PDFURL:     result.PDFURL,
		Citations:  result.Citations,
		OpenAccess: result.OpenAccess,
		License:    result.License,
		UploadID:   uploadID,
	}
	return s.Upsert(ownerID, record)
}

// UpdateTags replaces a record's tag list.
func (s *LibraryStore) UpdateTags(ownerID, id string, tags []string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, err
	}
	for index := range records {
		if records[index].ID != id {
			continue
		}
		records[index].Tags = normalizeTags(tags)
		records[index].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(ownerID, records); err != nil {
			return Record{}, err
		}
		return records[index], nil
	}
	return Record{}, ErrRecordNotFound
}

// AttachSession records that a record was added to a chat session.
func (s *LibraryStore) AttachSession(ownerID, id, sessionID string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	for index := range records {
		if records[index].ID != id {
			continue
		}
		if sessionID != "" {
			found := false
			for _, existing := range records[index].AttachedSessions {
				if existing == sessionID {
					found = true
					break
				}
			}
			if !found {
				records[index].AttachedSessions = append(records[index].AttachedSessions, sessionID)
			}
		}
		records[index].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(ownerID, records); err != nil {
			return Record{}, err
		}
		return records[index], nil
	}
	return Record{}, ErrRecordNotFound
}

// DetachSession removes a chat session from a record's attachment list.
func (s *LibraryStore) DetachSession(ownerID, id, sessionID string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return Record{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	for index := range records {
		if records[index].ID != id {
			continue
		}
		if sessionID != "" {
			kept := make([]string, 0, len(records[index].AttachedSessions))
			for _, existing := range records[index].AttachedSessions {
				if existing != sessionID {
					kept = append(kept, existing)
				}
			}
			records[index].AttachedSessions = kept
		}
		records[index].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(ownerID, records); err != nil {
			return Record{}, err
		}
		return records[index], nil
	}
	return Record{}, ErrRecordNotFound
}

// Delete removes a record from the owner library.
func (s *LibraryStore) Delete(ownerID, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked(ownerID)
	if err != nil {
		return false, err
	}
	for index := range records {
		if records[index].ID != id {
			continue
		}
		records = append(records[:index], records[index+1:]...)
		if err := s.saveLocked(ownerID, records); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// ErrRecordNotFound is returned when a library record does not exist.
var ErrRecordNotFound = fmt.Errorf("literature record not found")

func recordID(record Record) string {
	key := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(record.Source),
		strings.TrimSpace(record.ExternalID),
		normalizeDOI(record.DOI),
		strings.ToLower(strings.TrimSpace(record.ArxivID)),
		normalizeTitle(record.Title),
	}, "|"))
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:24]
}

// sameRecord reports whether two records describe the same paper.
func sameRecord(left, right Record) bool {
	if left.ID != "" && right.ID != "" && left.ID == right.ID {
		return true
	}
	if doi := normalizeDOI(left.DOI); doi != "" && doi == normalizeDOI(right.DOI) {
		return true
	}
	if arxiv := strings.ToLower(strings.TrimSpace(left.ArxivID)); arxiv != "" && arxiv == strings.ToLower(strings.TrimSpace(right.ArxivID)) {
		return true
	}
	leftTitle := normalizeTitle(left.Title)
	rightTitle := normalizeTitle(right.Title)
	return leftTitle != "" && leftTitle == rightTitle
}

func mergeRecord(existing, incoming Record) Record {
	merged := existing
	if incoming.Source != "" {
		merged.Source = incoming.Source
	}
	if incoming.ExternalID != "" {
		merged.ExternalID = incoming.ExternalID
	}
	if incoming.Title != "" {
		merged.Title = incoming.Title
	}
	if len(incoming.Authors) > 0 {
		merged.Authors = append([]string(nil), incoming.Authors...)
	}
	if incoming.Year > 0 {
		merged.Year = incoming.Year
	}
	if incoming.Venue != "" {
		merged.Venue = incoming.Venue
	}
	if len(incoming.Abstract) > len(merged.Abstract) {
		merged.Abstract = incoming.Abstract
	}
	if incoming.DOI != "" {
		merged.DOI = incoming.DOI
	}
	if incoming.ArxivID != "" {
		merged.ArxivID = incoming.ArxivID
	}
	if incoming.URL != "" {
		merged.URL = incoming.URL
	}
	if incoming.PDFURL != "" {
		merged.PDFURL = incoming.PDFURL
	}
	if incoming.Citations > merged.Citations {
		merged.Citations = incoming.Citations
	}
	if incoming.OpenAccess {
		merged.OpenAccess = true
	}
	if incoming.License != "" {
		merged.License = incoming.License
	}
	if incoming.UploadID != "" {
		merged.UploadID = incoming.UploadID
	}
	merged.Tags = normalizeTags(append(append([]string(nil), merged.Tags...), incoming.Tags...))
	merged.DOI = strings.ToLower(normalizeDOI(merged.DOI))
	return merged
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > 40 {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, tag)
	}
	return normalized
}

// BibTeX renders one record as a BibTeX entry.
func BibTeX(record Record) string {
	key := record.ID
	if len(key) > 12 {
		key = key[:12]
	}
	if key == "" {
		key = "record"
	}
	builder := strings.Builder{}
	builder.WriteString("@article{")
	builder.WriteString(key)
	builder.WriteString(",\n")
	writeBibField := func(name, value string, last bool) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		value = strings.ReplaceAll(value, "{", "")
		value = strings.ReplaceAll(value, "}", "")
		fmt.Fprintf(&builder, "  %s = {%s},\n", name, value)
	}
	writeBibField("title", record.Title, false)
	if len(record.Authors) > 0 {
		writeBibField("author", strings.Join(record.Authors, " and "), false)
	}
	if record.Year > 0 {
		writeBibField("year", fmt.Sprint(record.Year), false)
	}
	writeBibField("journal", record.Venue, false)
	writeBibField("doi", record.DOI, false)
	writeBibField("url", firstNonEmpty(record.URL, record.PDFURL), false)
	builder.WriteString("}\n")
	return builder.String()
}
