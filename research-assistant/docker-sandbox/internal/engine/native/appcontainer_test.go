package native

import "testing"

func TestAppContainerProfileName(t *testing.T) {
	if got := AppContainerProfileName(false); got != AppContainerOfflineProfile {
		t.Fatalf("offline = %q", got)
	}
	if got := AppContainerProfileName(true); got != AppContainerOnlineProfile {
		t.Fatalf("online = %q", got)
	}
	if InternetClientCapabilitySID != "S-1-15-2-1" {
		t.Fatalf("capability sid = %q", InternetClientCapabilitySID)
	}
}
