//go:build windows

package native

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	procSetKernelObjectSecurity   = windows.NewLazySystemDLL("advapi32.dll").NewProc("SetKernelObjectSecurity")
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
	// Deriving a SID always succeeds, even if the profile does not exist.
	// Only use it after Create confirms an already registered profile.
	if uint32(hr) == 0x800700B7 {
		return deriveAppContainerSID(name)
	}
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
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("appcontainer acl: current user unavailable: %w", err)
	}
	args := []string{workspace, "/inheritance:r", "/grant:r", "*" + user.User.Sid.String() + ":(OI)(CI)F"}
	if sid, err := ensureAppContainerProfile(AppContainerOfflineProfile, nil); err == nil {
		args = append(args, "/grant:r", "*"+sid.String()+":(OI)(CI)F")
	} else {
		return fmt.Errorf("appcontainer offline profile: %w", err)
	}
	capabilities, err := internetClientCapabilities()
	if err != nil {
		return err
	}
	sid, err := ensureAppContainerProfile(AppContainerOnlineProfile, capabilities)
	if err != nil {
		return fmt.Errorf("appcontainer online profile: %w", err)
	}
	args = append(args, "/grant:r", "*"+sid.String()+":(OI)(CI)F")
	command := exec.Command("icacls", args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("appcontainer acl: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// prepareAppContainerRuntimeCopy never changes the source runtime's DACL.
// All grants belong to fresh file objects in a service-owned staging tree.
// The restricted task tokens cannot write that tree while it is constructed,
// or after its explicit read/execute grants are published.
func prepareAppContainerRuntimeCopy(pythonPath, destinationParent string, managedRoots ...string) (string, error) {
	sourceRoot := filepath.Dir(pythonPath)
	venv := strings.EqualFold(filepath.Base(sourceRoot), "Scripts")
	if venv {
		sourceRoot = filepath.Dir(sourceRoot)
	}
	sourceRoot, err := canonicalEnvironmentPath(sourceRoot)
	if err != nil {
		return "", err
	}
	roots := []string{sourceRoot}
	var venvConfig []byte
	if venv {
		venvConfig, err = os.ReadFile(filepath.Join(sourceRoot, "pyvenv.cfg"))
		if err != nil {
			return "", fmt.Errorf("read managed virtual environment: %w", err)
		}
		base := ""
		for _, line := range strings.Split(string(venvConfig), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "home" {
				base = strings.TrimSpace(value)
			}
		}
		if base == "" {
			return "", fmt.Errorf("managed virtual environment is missing its base interpreter")
		}
		base, err = canonicalEnvironmentPath(base)
		if err != nil {
			return "", err
		}
		roots = append(roots, base)
	}
	for _, source := range roots {
		allowed := false
		for _, managed := range managedRoots {
			managed, err := canonicalEnvironmentPath(managed)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(managed, source)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				allowed = true
			}
		}
		if !allowed {
			return "", fmt.Errorf("sandbox_capability: interpreter is outside provider-managed runtimes")
		}
	}
	if err := os.MkdirAll(destinationParent, 0o700); err != nil {
		return "", err
	}
	destinationParent, err = canonicalEnvironmentPath(destinationParent)
	if err != nil {
		return "", err
	}
	for _, source := range roots {
		rel, relErr := filepath.Rel(source, destinationParent)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return "", fmt.Errorf("sandbox_capability: runtime copy destination must be outside source runtimes")
		}
	}
	destination, err := os.MkdirTemp(destinationParent, "appcontainer-")
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()
	if err := protectRuntimeCopyDirectory(destination); err != nil {
		return "", err
	}
	copies := make([]string, len(roots))
	for index, source := range roots {
		copies[index] = filepath.Join(destination, fmt.Sprintf("runtime-%d", index))
		if err := copyManagedRuntimeTree(source, copies[index]); err != nil {
			return "", err
		}
	}
	if venv {
		base := copies[1]
		var lines []string
		for _, line := range strings.Split(string(venvConfig), "\n") {
			key, _, ok := strings.Cut(line, "=")
			if ok {
				switch strings.TrimSpace(key) {
				case "home":
					line = "home = " + base
				case "executable":
					line = "executable = " + filepath.Join(base, "python.exe")
				case "command":
					continue
				}
			}
			lines = append(lines, strings.TrimSuffix(line, "\r"))
		}
		if err := os.WriteFile(filepath.Join(copies[0], "pyvenv.cfg"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			return "", err
		}
	}
	if err := grantRuntimeCopyProfiles(destination); err != nil {
		return "", err
	}
	complete = true
	if venv {
		return filepath.Join(copies[0], "Scripts", filepath.Base(pythonPath)), nil
	}
	return filepath.Join(copies[0], filepath.Base(pythonPath)), nil
}

func protectRuntimeCopyDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func copyManagedRuntimeTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		access := uint32(windows.FILE_READ_ATTRIBUTES)
		if !entry.IsDir() {
			access |= windows.GENERIC_READ
		}
		handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return fmt.Errorf("open stable managed runtime input: %w", err)
		}
		defer func() {
			if handle != windows.InvalidHandle {
				windows.CloseHandle(handle)
			}
		}()
		var before windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &before); err != nil {
			return err
		}
		if before.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("sandbox_capability: managed runtime contains a reparse point")
		}
		resolved, err := finalRuntimeHandlePath(handle)
		if err != nil {
			return err
		}
		inside, err := filepath.Rel(source, resolved)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
			return fmt.Errorf("sandbox_capability: managed runtime input moved outside its root")
		}
		if before.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return os.Mkdir(target, 0o700)
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		input := os.NewFile(uintptr(handle), path)
		// NewFile owns the handle; close it exactly once after the copy.
		handle = windows.InvalidHandle
		_, copyErr := io.Copy(output, input)
		var after windows.ByHandleFileInformation
		statErr := windows.GetFileInformationByHandle(windows.Handle(input.Fd()), &after)
		input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if before.FileSizeHigh != after.FileSizeHigh || before.FileSizeLow != after.FileSizeLow || before.LastWriteTime != after.LastWriteTime {
			return fmt.Errorf("sandbox_capability: managed runtime changed while being copied")
		}
		return nil
	})
}

func finalRuntimeHandlePath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil || n >= uint32(len(buffer)) {
		return "", fmt.Errorf("resolve managed runtime input")
	}
	path := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`), nil
	}
	return strings.TrimPrefix(path, `\\?\`), nil
}

func grantRuntimeCopyProfiles(root string) error {
	var entries []windows.EXPLICIT_ACCESS
	var pinner runtime.Pinner
	defer pinner.Unpin()
	for _, online := range []bool{false, true} {
		security, err := appContainerCapabilities(online)
		if err != nil {
			return err
		}
		pinner.Pin(security.AppContainerSid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE,
			AccessMode:        windows.SET_ACCESS,
			Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(security.AppContainerSid)},
		})
	}
	return filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		handle, err := windows.CreateFile(ptr, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(handle)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && info.NumberOfLinks != 1) {
			return fmt.Errorf("sandbox_capability: private runtime copy contains an unexpected linked object")
		}
		sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		sd, err = windows.BuildSecurityDescriptor(nil, nil, entries, nil, sd)
		if err != nil {
			return err
		}
		// Direct object updates avoid automatic recursive propagation into any
		// name absent from the freshly built service-owned copy.
		ok, _, callErr := procSetKernelObjectSecurity.Call(uintptr(handle), windows.DACL_SECURITY_INFORMATION, uintptr(unsafe.Pointer(sd)))
		runtime.KeepAlive(sd)
		if ok == 0 {
			return fmt.Errorf("runtime copy read/execute ACL: %w", callErr)
		}
		return nil
	})
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
