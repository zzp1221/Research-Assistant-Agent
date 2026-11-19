//go:build windows

package store

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
	"research-assistant-backend/internal/models"
)

func TestFilePlanStoreWindowsCaseAndInvalidIdentities(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("Case")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	before := readStoreFile(t, s.snapshotPath(plan.ID))
	for _, id := range []string{"case", "CASE", "CON", "NUL.txt", "COM1", "bad:identity", "bad*identity", "bad?identity", "bad\x00identity", "bad.", "bad "} {
		if err := s.SavePlan(testPlan(id)); err == nil {
			t.Fatalf("accepted unsafe Windows identity: %q", id)
		}
	}
	if !bytes.Equal(before, readStoreFile(t, s.snapshotPath(plan.ID))) {
		t.Fatal("unsafe identity overwrote plan")
	}
	s = reopenFileStore(t, path)
	plans, err := s.ListPlans()
	if err != nil || len(plans) != 1 || plans[0].ID != plan.ID {
		t.Fatalf("case identity lost on reopen: %+v %v", plans, err)
	}
}

func TestFilePlanStoreWindowsOccupiedSnapshotPreservesOldState(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("occupied")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(s.snapshotPath(plan.ID))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = s.UpdatePlan(plan.ID, func(p *models.PlanGraph) error { p.Nodes[0].Name = "changed"; return nil })
	if !errors.Is(err, ErrNotCommitted) {
		_ = windows.CloseHandle(handle)
		t.Fatalf("occupied replace: %v", err)
	}
	assertPlanName(t, s, plan.ID, plan.Nodes[0].Name)
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	assertPlanName(t, reopenFileStore(t, path), plan.ID, plan.Nodes[0].Name)
	if err := s.UpdatePlan(plan.ID, func(p *models.PlanGraph) error { p.Nodes[0].Name = "changed"; return nil }); err != nil {
		t.Fatal(err)
	}
	assertPlanName(t, reopenFileStore(t, path), plan.ID, "changed")
}

func TestFilePlanStoreWindowsReadonlySnapshotFailure(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("readonly")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	name := s.snapshotPath(plan.ID)
	if err := os.Chmod(name, 0o400); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(name, 0o600)
	err := s.UpdatePlan(plan.ID, func(p *models.PlanGraph) error { p.Nodes[0].Name = "changed"; return nil })
	if !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("read-only replace: %v", err)
	}
	assertPlanName(t, s, plan.ID, plan.Nodes[0].Name)
	assertPlanName(t, reopenFileStore(t, path), plan.ID, plan.Nodes[0].Name)
}
