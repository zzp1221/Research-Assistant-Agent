package main

import "testing"

func TestSandboxLabelsRequireTaskAndBindTenant(t *testing.T) {
	if _, err := sandboxLabels(CreateRequest{}); err == nil {
		t.Fatal("expected missing task_id to be rejected")
	}
	labels, err := sandboxLabels(CreateRequest{TaskID: "task-1", TenantID: "tenant-1", PlanID: "plan-1"})
	if err != nil {
		t.Fatal(err)
	}
	if labels[sandboxLabelKey] != sandboxLabelValue {
		t.Fatalf("missing sandbox label: %#v", labels)
	}
	if labels[tenantLabelKey] != "tenant-1" || labels[taskLabelKey] != "task-1" || labels[planLabelKey] != "plan-1" {
		t.Fatalf("unexpected identity labels: %#v", labels)
	}
}

func TestSandboxLabelsRejectUnsafeIdentity(t *testing.T) {
	if _, err := sandboxLabels(CreateRequest{TaskID: "../escape"}); err == nil {
		t.Fatal("expected unsafe task_id to be rejected")
	}
	if _, err := sandboxLabels(CreateRequest{TaskID: "task-1", TenantID: "tenant with space"}); err == nil {
		t.Fatal("expected unsafe tenant_id to be rejected")
	}
}
