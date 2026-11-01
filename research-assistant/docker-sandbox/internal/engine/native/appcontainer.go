package native

// AppContainer profiles used by the native runner. The offline profile has no
// capabilities; the online profile grants internetClient only.
const (
	AppContainerOfflineProfile  = "ResearchAssistant.Sandbox"
	AppContainerOnlineProfile   = "ResearchAssistant.Sandbox.Online"
	InternetClientCapabilitySID = "S-1-15-2-1"
)

// AppContainerProfileName selects the profile for a command.
func AppContainerProfileName(networkOn bool) string {
	if networkOn {
		return AppContainerOnlineProfile
	}
	return AppContainerOfflineProfile
}
