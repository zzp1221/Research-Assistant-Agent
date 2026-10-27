// Package audit records security-relevant actions (who did what, when and with
// which result) in an append-only JSONL stream. The API exposes a filtered
// export so operators can inspect activity without grepping logs.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event is one audit record.
type Event struct {
	Time     time.Time      `json:"time"`
	Action   string         `json:"action"`
	Actor    string         `json:"actor,omitempty"`
	Target   string         `json:"target,omitempty"`
	Result   string         `json:"result,omitempty"`
	Detail   map[string]any `json:"detail,omitempty"`
	RemoteIP string         `json:"remote_ip,omitempty"`
}

const ringLimit = 10000

type Logger struct {
	mu   sync.Mutex
	ring []Event
	file *os.File
	path string
}

var defaultLogger = &Logger{}

func Default() *Logger { return defaultLogger }

// Configure opens AUDIT_LOG_PATH when set. An empty value keeps the ring
// buffer only, which is the safe default for single-node deployments.
func (l *Logger) Configure(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	l.path = strings.TrimSpace(path)
	if l.path == "" {
		return nil
	}
	if dir := filepath.Dir(l.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.file = file
	return nil
}

// Record appends one event to the ring and the optional file.
func (l *Logger) Record(event Event) {
	if l == nil {
		return
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = append(l.ring, event)
	if len(l.ring) > ringLimit {
		copy(l.ring, l.ring[len(l.ring)-ringLimit:])
		l.ring = l.ring[:ringLimit]
	}
	if l.file != nil {
		if encoded, err := json.Marshal(event); err == nil {
			_, _ = l.file.Write(append(encoded, '\n'))
		}
	}
}

// Events returns up to limit events, newest last. actor filters by principal;
// an empty actor returns all events.
func (l *Logger) Events(limit int, actor string) []Event {
	if l == nil {
		return nil
	}
	if limit <= 0 || limit > ringLimit {
		limit = 500
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, 0, limit)
	for i := len(l.ring) - 1; i >= 0 && len(out) < limit; i-- {
		if actor != "" && l.ring[i].Actor != actor {
			continue
		}
		out = append(out, l.ring[i])
	}
	// Reverse to chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Record appends an event using the default logger.
func Record(action, actor, target string, detail map[string]any) {
	defaultLogger.Record(Event{Action: action, Actor: actor, Target: target, Detail: detail})
}

// Path returns the configured file path (empty when ring-only).
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path
}

// Reset clears the ring (tests).
func (l *Logger) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = nil
}
