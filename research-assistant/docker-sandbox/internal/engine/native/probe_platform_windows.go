//go:build windows

package native

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func probePlatformKey() string {
	version := windows.RtlGetVersion()
	return fmt.Sprintf("windows/%d.%d.%d/service-pack-%d.%d/product-%d/suite-%d",
		version.MajorVersion, version.MinorVersion, version.BuildNumber,
		version.ServicePackMajor, version.ServicePackMinor, version.ProductType, version.SuiteMask)
}
