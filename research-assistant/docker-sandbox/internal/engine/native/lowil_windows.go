//go:build windows

package native

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepareLowIntegrityWorkspace permits a low token to create new outputs in
// this directory. Do not inherit or recursively apply the label: existing
// files, hardlinks and junction targets may belong to a host trust boundary.
func prepareLowIntegrityWorkspace(workspace string) error {
	if !filepath.IsAbs(workspace) {
		return fmt.Errorf("lowil workspace must be absolute")
	}
	workspace = filepath.Clean(workspace)
	expected, err := canonicalEnvironmentPath(workspace)
	if err != nil {
		return fmt.Errorf("resolve lowil workspace: %w", err)
	}
	name, err := windows.UTF16PtrFromString(workspace)
	if err != nil {
		return fmt.Errorf("invalid lowil workspace")
	}
	// SetSecurityInfo suppresses child propagation for MAXIMUM_ALLOWED handles.
	// Without it Windows also rewrites noninheriting child SACL metadata, which
	// would modify an external file reached through a workspace hardlink.
	handle, err := windows.CreateFile(name, windows.MAXIMUM_ALLOWED,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("open lowil workspace for labeling: %w", err)
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("lowil workspace must be a directory without a reparse point")
	}
	resolved, err := finalEnvironmentHandlePath(handle)
	if err != nil || !strings.EqualFold(filepath.Clean(expected), filepath.Clean(resolved)) {
		return fmt.Errorf("lowil workspace changed while being prepared")
	}
	sd, err := windows.SecurityDescriptorFromString("S:(ML;;NW;;;LW)")
	if err != nil {
		return err
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return err
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		return fmt.Errorf("label lowil workspace: %w", err)
	}
	return nil
}

type tokenMandatoryLabel struct {
	Label windows.SIDAndAttributes
}

// createLowIntegrityToken duplicates the current primary token and lowers its
// integrity level to Low (S-1-16-4096).
func createLowIntegrityToken() (windows.Token, error) {
	var current windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_ADJUST_DEFAULT, &current); err != nil {
		return 0, fmt.Errorf("open process token: %w", err)
	}
	defer current.Close()
	var duplicate windows.Token
	if err := windows.DuplicateTokenEx(current, windows.TOKEN_ALL_ACCESS, nil, windows.SecurityImpersonation, windows.TokenPrimary, &duplicate); err != nil {
		return 0, fmt.Errorf("duplicate token: %w", err)
	}
	lowSid, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		_ = duplicate.Close()
		return 0, fmt.Errorf("create low integrity sid: %w", err)
	}
	label := tokenMandatoryLabel{Label: windows.SIDAndAttributes{Sid: lowSid, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(duplicate, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), uint32(unsafe.Sizeof(label))); err != nil {
		_ = duplicate.Close()
		return 0, fmt.Errorf("set token integrity level: %w", err)
	}
	return duplicate, nil
}
