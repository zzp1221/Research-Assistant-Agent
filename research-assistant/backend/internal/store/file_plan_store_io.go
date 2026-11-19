package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The per-instance operations allow deterministic faults without global hooks.
type durableFile interface {
	io.Writer
	io.Seeker
	Sync() error
	Close() error
	Truncate(int64) error
	Chmod(os.FileMode) error
	Name() string
}

type fileStoreIO struct {
	createTemp func(string, string) (durableFile, error)
	openLog    func(string) (durableFile, error)
	// applied reports that the target may have been replaced, even on error.
	replace func(string, string) (applied bool, err error)
	syncDir func(string) error
}

func defaultFileStoreIO() fileStoreIO {
	return fileStoreIO{
		createTemp: func(dir, pattern string) (durableFile, error) { return os.CreateTemp(dir, pattern) },
		openLog: func(path string) (durableFile, error) {
			// O_APPEND maps to append-only write rights on Windows, which cannot
			// truncate for rollback. The store lock and SeekEnd below implement
			// append for the explicitly supported single writer instead.
			return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		},
		replace: replacePlanStoreFile,
		syncDir: syncPlanStoreDirectory,
	}
}

func writeComplete(file io.Writer, raw []byte) error {
	n, err := file.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	return nil
}

func (s *FilePlanStore) writeEnvelopeLocked(meta planEnvelope, op string) error {
	if err := validateEnvelope(meta); err != nil {
		return err
	}
	if err := validateSnapshotFileID(meta.PlanID); err != nil {
		return err
	}
	// Reject colliding normalized ids before overwriting another plan's file.
	for id := range s.envelopes {
		if id != meta.PlanID && snapshotFileIDsCollide(id, meta.PlanID) {
			return fmt.Errorf("plan filename collision: %s", meta.PlanID)
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode plan snapshot: %w", err)
	}
	return s.replaceBytesLocked(s.snapshotPath(meta.PlanID), raw, op, meta.PlanID, meta.Generation, meta.EventSeqFloor)
}

func snapshotFileIDsCollide(first, second string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(safePlanFileID(first), safePlanFileID(second))
	}
	return safePlanFileID(first) == safePlanFileID(second)
}

func validateSnapshotFileID(id string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	name := safePlanFileID(id)
	if strings.ContainsAny(name, `<>:"|?*`) || strings.TrimRight(name, ". ") != name {
		return fmt.Errorf("plan id is not a valid Windows file identity")
	}
	for _, character := range name {
		if character < 32 {
			return fmt.Errorf("plan id contains a control character")
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		return fmt.Errorf("plan id is a reserved Windows file identity")
	}
	return nil
}

func (s *FilePlanStore) replaceBytesLocked(path string, raw []byte, op, id, generation string, seq int64) error {
	file, err := s.io.createTemp(filepath.Dir(path), ".plan-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create snapshot temp file: %w", ErrNotCommitted, err)
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: chmod snapshot: %w", ErrNotCommitted, err)
	}
	if err := writeComplete(file, raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: write snapshot: %w", ErrNotCommitted, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: sync snapshot: %w", ErrNotCommitted, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: close snapshot: %w", ErrNotCommitted, err)
	}
	applied, err := s.io.replace(name, path)
	if err != nil {
		if applied {
			return s.sealLocked(op, id, generation, seq, fmt.Errorf("replace snapshot: %w", err))
		}
		return fmt.Errorf("%w: replace snapshot: %w", ErrNotCommitted, err)
	}
	if err := s.io.syncDir(filepath.Dir(path)); err != nil {
		return s.sealLocked(op, id, generation, seq, fmt.Errorf("sync snapshot directory: %w", err))
	}
	return nil
}

func (s *FilePlanStore) appendRecordLocked(record fileLogRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	raw = append(raw, '\n')
	file, err := s.io.openLog(s.path)
	if err != nil {
		return fmt.Errorf("%w: open event log: %w", ErrNotCommitted, err)
	}
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: seek event log: %w", ErrNotCommitted, err)
	}
	if offset != s.logOffset {
		_ = file.Close()
		return s.sealLocked("append_event", record.PlanID, record.Generation, record.Event.Seq, fmt.Errorf("event log length changed outside this store"))
	}
	writeErr := writeComplete(file, raw)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr != nil {
		rollbackErr := file.Truncate(offset)
		if rollbackErr == nil {
			rollbackErr = file.Sync()
		}
		closeErr := file.Close()
		if rollbackErr != nil {
			return s.sealLocked("append_event", record.PlanID, record.Generation, record.Event.Seq, errors.Join(writeErr, fmt.Errorf("rollback event log: %w", rollbackErr), closeErr))
		}
		// Truncate + Sync confirmed the old durable prefix. A close error after
		// that confirmation cannot turn the failed append into a committed event.
		if closeErr != nil {
			log.Printf("plan store: close rolled-back event log: %v", closeErr)
		}
		return fmt.Errorf("%w: append event (rolled back): %w", ErrNotCommitted, writeErr)
	}
	s.logOffset = offset + int64(len(raw))
	// Sync is the commit confirmation. Close errors after it are diagnostics.
	if err := file.Close(); err != nil {
		log.Printf("plan store: close committed event log: %v", err)
	}
	return nil
}

func (s *FilePlanStore) initializeLog() error {
	file, err := s.io.openLog(s.path)
	if err != nil {
		return fmt.Errorf("initialize event log: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close event log: %w", err)
	}
	if err := s.io.syncDir(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("sync event log directory: %w", err)
	}
	return nil
}
