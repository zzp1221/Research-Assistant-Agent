// Package mygoapp adapts the MyGo desktop framework to the contracts the
// rest of the desktop module already uses. Business code (bridge, tray,
// shell) depends on this package rather than on MyGo directly, so a MyGo
// API change stays here.
package mygoapp

import (
	"strings"

	mygo "github.com/egoist/mygo"
)

// ChooseDirectory opens the system folder picker and returns the selected
// path, or "" when the user cancels.
func ChooseDirectory(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     strings.TrimSpace(title),
		Directory: true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// ShowInFolder reveals path in Explorer.
func ShowInFolder(path string) error {
	mygo.Shell.ShowItemInFolder(path)
	return nil
}

// OpenPath opens a directory or file with the default application.
func OpenPath(path string) error {
	return mygo.Shell.OpenPath(path)
}

// Notify shows a system notification.
func Notify(title, body string) error {
	if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
		return nil
	}
	return mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show()
}
