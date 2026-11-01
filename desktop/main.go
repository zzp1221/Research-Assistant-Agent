//go:build desktop && !wails

package main

import (
	"fmt"
	"os"

	"research-assistant-desktop/paths"
)

func main() {
	fmt.Println("ResearchAssistant desktop shell (headless stub)")
	fmt.Printf("data dir: %s\n", paths.DataDir())
	fmt.Printf("log dir:  %s\n", paths.LogDir())
	fmt.Printf("install:  %s\n", paths.InstallDir())
	fmt.Println("Rebuild with -tags \"desktop wails\" and Wails v2 to open the real window.")
	if err := paths.Ensure(paths.LogDir()); err != nil {
		fmt.Fprintf(os.Stderr, "create log dir: %v\n", err)
		os.Exit(1)
	}
}
