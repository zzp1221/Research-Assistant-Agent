package scheduler

import (
	"errors"
	"fmt"

	"research-assistant-backend/internal/store"
)

// errEventPersistence prevents a successfully committed task result from being
// reclassified as an executor failure when only its subsequent event failed.
var errEventPersistence = errors.New("scheduler event persistence failed")

func (s *Scheduler) recordEventPersistenceFailure(planID string, cause error) error {
	err := fmt.Errorf("%w: %w", errEventPersistence, cause)
	s.persistenceMu.Lock()
	if s.persistenceErrors == nil {
		s.persistenceErrors = make(map[string]error)
	}
	if s.persistenceErrors[planID] == nil {
		s.persistenceErrors[planID] = err
	}
	s.persistenceMu.Unlock()
	s.notifyProgress()
	return err
}

// persistenceError is checked before dispatch, including after asynchronous
// task-log failures. Health is optional so alternative PlanStore implementations
// can keep the interface small; a sealed FilePlanStore supplies it.
func (s *Scheduler) persistenceError(planID string) error {
	if health, ok := s.store.(interface{ Health() error }); ok {
		if err := health.Health(); err != nil {
			return err
		}
	}
	s.persistenceMu.Lock()
	defer s.persistenceMu.Unlock()
	return s.persistenceErrors[planID]
}

func (s *Scheduler) clearPersistenceError(planID string) {
	s.persistenceMu.Lock()
	delete(s.persistenceErrors, planID)
	s.persistenceMu.Unlock()
}

func isPersistenceFailure(err error) bool {
	return errors.Is(err, errEventPersistence) || errors.Is(err, store.ErrNotCommitted) || errors.Is(err, store.ErrCommitUnknown) || errors.Is(err, store.ErrStoreRecoveryRequired)
}
