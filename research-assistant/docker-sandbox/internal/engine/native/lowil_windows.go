//go:build windows

package native

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
