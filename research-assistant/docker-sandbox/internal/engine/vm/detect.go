// Package vm implements the WSL2-backed VM sandbox provider placeholder.
package vm

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ErrUnavailable means the VM provider cannot execute in this environment.
var ErrUnavailable = errors.New("vm provider requires Windows 11 with a configured WSL2 distribution")

// Distribution is one WSL distribution.
type Distribution struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	State   string `json:"state"`
	Version int    `json:"version"`
}

// Detection is the result of probing WSL.
type Detection struct {
	Available           bool           `json:"available"`
	Distributions       []Distribution `json:"distributions,omitempty"`
	DefaultDistribution string         `json:"default_distribution,omitempty"`
	Reason              string         `json:"reason,omitempty"`
}

// ParseWSLList parses the output of `wsl.exe -l -v`.
func ParseWSLList(output string) []Distribution {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(output, "\n")
	distributions := []Distribution{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "NAME") || strings.Contains(upper, "NAME STATE VERSION") {
			continue
		}
		fields := strings.Fields(line)
		isDefault := false
		if len(fields) > 0 && fields[0] == "*" {
			isDefault = true
			fields = fields[1:]
		}
		if len(fields) < 3 {
			continue
		}
		version, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			continue
		}
		state := fields[len(fields)-2]
		name := strings.Join(fields[:len(fields)-2], " ")
		if name == "" {
			continue
		}
		distributions = append(distributions, Distribution{Name: name, Default: isDefault, State: state, Version: version})
	}
	return distributions
}

// DecodeWSLOutput decodes wsl.exe output, which is UTF-16LE when redirected.
func DecodeWSLOutput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	hasNUL := false
	for _, value := range raw {
		if value == 0 {
			hasNUL = true
			break
		}
	}
	if !hasNUL {
		return string(raw)
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

// Detect probes WSL2 availability.
func Detect(ctx context.Context) Detection {
	if runtime.GOOS != "windows" {
		return Detection{Reason: "wsl is only available on windows"}
	}
	command := exec.CommandContext(ctx, "wsl.exe", "-l", "-v")
	output, err := command.CombinedOutput()
	if err != nil {
		return Detection{Reason: fmt.Sprintf("wsl -l -v failed: %v", err)}
	}
	distributions := ParseWSLList(DecodeWSLOutput(output))
	if len(distributions) == 0 {
		return Detection{Reason: "no WSL2 distribution is installed"}
	}
	result := Detection{Available: true, Distributions: distributions}
	for _, distribution := range distributions {
		if distribution.Default {
			result.DefaultDistribution = distribution.Name
			break
		}
	}
	if result.DefaultDistribution == "" {
		result.DefaultDistribution = distributions[0].Name
	}
	return result
}
