package tray

import "testing"

func TestMemoryTray(t *testing.T) {
	memory := NewMemory()
	memory.SetStatus(Status{Running: true, Provider: "native", Isolation: "lowil"})
	if err := memory.Notify("title", "body"); err != nil {
		t.Fatal(err)
	}
	status, notifications, quit := memory.Snapshot()
	if !status.Running || status.Provider != "native" || status.Isolation != "lowil" {
		t.Fatalf("status = %#v", status)
	}
	if len(notifications) != 1 || notifications[0].Title != "title" || notifications[0].Body != "body" {
		t.Fatalf("notifications = %#v", notifications)
	}
	if quit {
		t.Fatal("quit should be false")
	}
	memory.Quit()
	_, _, quit = memory.Snapshot()
	if !quit {
		t.Fatal("quit should be true")
	}
}
