package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"docker-sandbox/internal/engine"
)

func TestParseWSLList(t *testing.T) {
	output := "  NAME            STATE           VERSION\n* Ubuntu-22.04    Running         2\n  docker-desktop  Stopped         2\n"
	distributions := ParseWSLList(output)
	if len(distributions) != 2 {
		t.Fatalf("len = %d, %#v", len(distributions), distributions)
	}
	if distributions[0].Name != "Ubuntu-22.04" || !distributions[0].Default || distributions[0].State != "Running" || distributions[0].Version != 2 {
		t.Fatalf("first = %#v", distributions[0])
	}
	if distributions[1].Default {
		t.Fatalf("second should not be default: %#v", distributions[1])
	}
}

func TestParseWSLListSkipsHeaderAndGarbage(t *testing.T) {
	distributions := ParseWSLList("NAME STATE VERSION\nnot a row\n")
	if len(distributions) != 0 {
		t.Fatalf("distributions = %#v", distributions)
	}
}

func TestDecodeWSLOutputUTF16(t *testing.T) {
	text := "Ubuntu"
	raw := make([]byte, 0, len(text)*2)
	for _, char := range text {
		raw = append(raw, byte(char), 0)
	}
	if got := DecodeWSLOutput(raw); got != text {
		t.Fatalf("decoded = %q", got)
	}
	if got := DecodeWSLOutput([]byte("plain")); got != "plain" {
		t.Fatalf("plain = %q", got)
	}
}

func TestProviderCapabilitiesAndExplicitFailure(t *testing.T) {
	provider := NewProvider(context.Background())
	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Provider != "vm" || caps.Isolation != "vm" || !caps.Images || caps.NetworkEnforced != "os" {
		t.Fatalf("caps = %#v", caps)
	}
	if !strings.Contains(strings.Join(caps.Notes, " "), "WSL2") {
		t.Fatalf("notes = %#v", caps.Notes)
	}
	if _, err := provider.Create(context.Background(), engine.CreateSpec{}); err == nil {
		t.Fatal("Create should fail explicitly")
	} else if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
