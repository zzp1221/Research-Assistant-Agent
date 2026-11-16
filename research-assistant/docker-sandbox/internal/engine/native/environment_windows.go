//go:build windows

package native

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalEnvironmentPath(path string) (string, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	return finalEnvironmentHandlePath(handle)
}

func finalEnvironmentHandlePath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil || n >= uint32(len(buffer)) {
		return "", fmt.Errorf("resolve final environment path")
	}
	resolved := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`), nil
	}
	return strings.TrimPrefix(resolved, `\\?\`), nil
}

func systemEnvironment() (map[string]string, error) {
	root, err := windows.GetWindowsDirectory()
	if err != nil {
		return nil, fmt.Errorf("resolve Windows directory: %w", err)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, fmt.Errorf("resolve Windows system directory: %w", err)
	}
	return map[string]string{"SystemRoot": root, "WINDIR": root, "PATH": system + ";" + root + ";" + filepath.Join(system, "WindowsPowerShell", "v1.0"), "COMSPEC": filepath.Join(system, "cmd.exe"), "PATHEXT": ".COM;.EXE;.BAT;.CMD"}, nil
}

func prepareEnvironmentDirectories(root string, privateDirectories ...string) error {
	// Windows rewrites an AppContainer's TMP/TEMP under LOCALAPPDATA's
	// Packages/<profile>/AC/Temp, even when an explicit environment block was
	// supplied. Prepare both profile trees inside the private cache so this
	// OS behavior cannot fall back to the host user's profile or fail to write.
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open private environment directory: %w", err)
	}
	defer workspace.Close()
	directories := append([]string{"", "cache", filepath.Join("cache", "Packages")}, privateDirectories...)
	for _, profile := range []string{AppContainerOfflineProfile, AppContainerOnlineProfile} {
		profileRoot := filepath.Join("cache", "Packages", strings.ToLower(profile))
		for _, leaf := range []string{"", "AC", filepath.Join("AC", "Temp"), "LocalState", "RoamingState", "TempState"} {
			relative := filepath.Join(profileRoot, leaf)
			if err := workspace.MkdirAll(relative, 0o700); err != nil {
				return fmt.Errorf("prepare private AppContainer profile: %w", err)
			}
			directories = append(directories, relative)
		}
	}
	// os.Root's directory handle intentionally omits share-delete. Release it
	// before opening a MAXIMUM_ALLOWED labeling handle (which includes DELETE).
	if err := workspace.Close(); err != nil {
		return err
	}
	// Label only these directories. Inheritable labels or normal propagation
	// would also relabel an external host file hardlinked into a task's cache.
	// Newly created process outputs receive the creator's low integrity label.
	sd, err := windows.SecurityDescriptorFromString("S:(ML;;NW;;;LW)")
	if err != nil {
		return fmt.Errorf("build environment integrity label: %w", err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return fmt.Errorf("read environment integrity label: %w", err)
	}
	parent, err := canonicalEnvironmentPath(filepath.Dir(root))
	if err != nil {
		return err
	}
	expectedRoot := filepath.Join(parent, filepath.Base(root))
	handle, err := openPrivateEnvironmentDirectory(root, expectedRoot)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	seen := make(map[string]bool)
	for _, relative := range directories {
		if seen[relative] {
			continue
		}
		seen[relative] = true
		directory := handle
		if relative != "" {
			directory, err = openPrivateEnvironmentDirectory(filepath.Join(root, relative), filepath.Join(expectedRoot, relative))
			if err != nil {
				return err
			}
		}
		labelErr := windows.SetSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl)
		if relative != "" {
			windows.CloseHandle(directory)
		}
		if labelErr != nil {
			return fmt.Errorf("prepare environment integrity label: %w", labelErr)
		}
	}
	return nil
}

func openPrivateEnvironmentDirectory(path, expected string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("invalid private environment directory")
	}
	// MAXIMUM_ALLOWED makes SetSecurityInfo suppress child propagation.
	// OPEN_REPARSE_POINT and the final handle path bind labels to the checked
	// directory; denying share-delete prevents replacing it during the change.
	handle, err := windows.CreateFile(name, windows.MAXIMUM_ALLOWED, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("open private environment for labeling: %w", err)
	}
	var info windows.ByHandleFileInformation
	resolved, resolveErr := finalEnvironmentHandlePath(handle)
	infoErr := windows.GetFileInformationByHandle(handle, &info)
	if resolveErr != nil || infoErr != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		!strings.EqualFold(filepath.Clean(resolved), filepath.Clean(expected)) {
		windows.CloseHandle(handle)
		return windows.InvalidHandle, fmt.Errorf("private environment directory was replaced or crossed a reparse point")
	}
	return handle, nil
}
