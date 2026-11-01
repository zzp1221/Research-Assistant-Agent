package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
)

// InstanceLock is a cross-platform exclusive lock file used for single
// instance startup. The file is removed on Release.
type InstanceLock struct {
	path string
	file *os.File
}

func AcquireInstanceLock(path string) (*InstanceLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("another ResearchAssistant instance is already running")
		}
		return nil, err
	}
	_, _ = file.WriteString(fmt.Sprintf("%d", os.Getpid()))
	return &InstanceLock{path: path, file: file}, nil
}

func (l *InstanceLock) Release() error {
	if l == nil {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	err := os.Remove(l.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
