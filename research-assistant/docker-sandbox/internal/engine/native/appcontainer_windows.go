//go:build windows

package native

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	seGroupEnabled                          = 0x00000004
)

// securityCapabilities mirrors SECURITY_CAPABILITIES from processthreadsapi.h.
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

var (
	modUserenv                    = syscall.NewLazyDLL("userenv.dll")
	procCreateAppContainerProfile = modUserenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile = modUserenv.NewProc("DeleteAppContainerProfile")
	procDeriveAppContainerSid     = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

func deriveAppContainerSID(name string) (*windows.SID, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := procDeriveAppContainerSid.Call(uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(&sid)))
	if int32(hr) == 0 && sid != nil {
		return sid, nil
	}
	return nil, fmt.Errorf("DeriveAppContainerSidFromAppContainerName: HRESULT 0x%X", uint32(hr))
}

// ensureAppContainerProfile derives the profile SID, creating the profile with
// the supplied capabilities on first use.
func ensureAppContainerProfile(name string, capabilities []windows.SIDAndAttributes) (*windows.SID, error) {
	if sid, err := deriveAppContainerSID(name); err == nil {
		return sid, nil
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	displayPtr, _ := windows.UTF16PtrFromString("ResearchAssistant Sandbox")
	descriptionPtr, _ := windows.UTF16PtrFromString("ResearchAssistant native sandbox")
	var capabilityPtr *windows.SIDAndAttributes
	if len(capabilities) > 0 {
		capabilityPtr = &capabilities[0]
	}
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(displayPtr)),
		uintptr(unsafe.Pointer(descriptionPtr)),
		uintptr(unsafe.Pointer(capabilityPtr)),
		uintptr(len(capabilities)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if int32(hr) != 0 {
		return nil, fmt.Errorf("CreateAppContainerProfile(%s): HRESULT 0x%X", name, uint32(hr))
	}
	return sid, nil
}

// deleteAppContainerProfile removes a sandbox profile during uninstall.
func deleteAppContainerProfile(name string) error {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	hr, _, _ := procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(namePtr)))
	if int32(hr) != 0 {
		return fmt.Errorf("DeleteAppContainerProfile(%s): HRESULT 0x%X", name, uint32(hr))
	}
	return nil
}

func internetClientCapabilities() ([]windows.SIDAndAttributes, error) {
	sid, err := windows.StringToSid(InternetClientCapabilitySID)
	if err != nil {
		return nil, fmt.Errorf("internetClient capability sid: %w", err)
	}
	return []windows.SIDAndAttributes{{Sid: sid, Attributes: seGroupEnabled}}, nil
}

// prepareAppContainerWorkspace removes ACL inheritance and grants the current
// user plus both sandbox profile SIDs access to the plan workspace only.
func prepareAppContainerWorkspace(workspace string) error {
	user := strings.TrimSpace(os.Getenv("USERNAME"))
	if user == "" {
		return fmt.Errorf("appcontainer acl: USERNAME is empty")
	}
	args := []string{workspace, "/inheritance:r", "/grant:r", user + ":(OI)(CI)F"}
	if sid, err := ensureAppContainerProfile(AppContainerOfflineProfile, nil); err == nil {
		args = append(args, "/grant:r", sid.String()+":(OI)(CI)F")
	} else {
		return fmt.Errorf("appcontainer offline profile: %w", err)
	}
	if capabilities, err := internetClientCapabilities(); err == nil {
		if sid, err := ensureAppContainerProfile(AppContainerOnlineProfile, capabilities); err == nil {
			args = append(args, "/grant:r", sid.String()+":(OI)(CI)F")
		}
	}
	command := exec.Command("icacls", args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("appcontainer acl: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// appContainerCapabilities returns the security capabilities for a command.
// Offline commands use a profile without capabilities; online commands use the
// profile that grants internetClient.
func appContainerCapabilities(networkOn bool) (*securityCapabilities, error) {
	var capabilities []windows.SIDAndAttributes
	if networkOn {
		var err error
		capabilities, err = internetClientCapabilities()
		if err != nil {
			return nil, err
		}
	}
	sid, err := ensureAppContainerProfile(AppContainerProfileName(networkOn), capabilities)
	if err != nil {
		return nil, err
	}
	var capabilityPtr *windows.SIDAndAttributes
	if len(capabilities) > 0 {
		capabilityPtr = &capabilities[0]
	}
	return &securityCapabilities{AppContainerSid: sid, Capabilities: capabilityPtr, CapabilityCount: uint32(len(capabilities))}, nil
}
