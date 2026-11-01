// Package logging provides a size-based rotating log writer. Desktop builds
// must not truncate logs on every start; the server keeps the same behavior
// for consistency.
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter writes to path and rotates to path.1 .. path.N when the file
// exceeds MaxBytes. MaxFiles includes the current file (5 means path plus four
// rotated files).
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	file     *os.File
	size     int64
}

// Open creates or appends to path, creating parent directories as needed.
func Open(path string, maxBytes int64, maxFiles int) (*RotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}
	if maxFiles <= 0 {
		maxFiles = 5
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	size := int64(0)
	if info, statErr := file.Stat(); statErr == nil {
		size = info.Size()
	}
	return &RotatingWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles, file: file, size: size}, nil
}

// Write implements io.Writer.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.maxBytes > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the underlying file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if w.maxFiles <= 1 {
		file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		w.file = file
		w.size = 0
		return nil
	}
	for i := w.maxFiles - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", w.path, i)
		if i == w.maxFiles-1 {
			_ = os.Remove(oldPath)
			continue
		}
		newPath := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Remove(newPath)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
		}
	}
	newest := w.path + ".1"
	_ = os.Remove(newest)
	if err := os.Rename(w.path, newest); err != nil {
		return err
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}
