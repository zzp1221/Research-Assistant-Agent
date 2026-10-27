// Package scan performs optional ClamAV (clamd INSTREAM) malware scanning for
// uploaded files. When no clamd endpoint is configured the scanner explicitly
// reports "skipped" instead of pretending the file was verified.
package scan

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Result is the outcome of one scan.
type Result struct {
	Status    string `json:"status"` // clean / infected / skipped / error
	Engine    string `json:"engine,omitempty"`
	Signature string `json:"signature,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Configured reports whether a ClamAV endpoint is configured.
func Configured() bool {
	return strings.TrimSpace(os.Getenv("CLAMAV_ADDR")) != ""
}

func address() string {
	return strings.TrimSpace(os.Getenv("CLAMAV_ADDR"))
}

func timeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CLAMAV_TIMEOUT_SECONDS"))
	if raw == "" {
		return 60 * time.Second
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// ScanBytes scans data with clamd INSTREAM. A missing configuration returns a
// skipped result and a nil error.
func ScanBytes(ctx context.Context, data []byte) (Result, error) {
	if !Configured() {
		return Result{Status: "skipped", Detail: "CLAMAV_ADDR is not configured"}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scanCtx, cancel := context.WithTimeout(ctx, timeout())
	defer cancel()

	var dialer net.Dialer
	connection, err := dialer.DialContext(scanCtx, "tcp", address())
	if err != nil {
		return Result{Status: "error", Detail: err.Error()}, err
	}
	defer connection.Close()
	if deadline, ok := scanCtx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}

	writer := bufio.NewWriter(connection)
	if _, err := writer.WriteString("zINSTREAM\x00"); err != nil {
		return Result{Status: "error", Detail: err.Error()}, err
	}
	const chunkSize = 64 * 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(chunk)))
		if _, err := writer.Write(length[:]); err != nil {
			return Result{Status: "error", Detail: err.Error()}, err
		}
		if _, err := writer.Write(chunk); err != nil {
			return Result{Status: "error", Detail: err.Error()}, err
		}
	}
	var endMarker [4]byte
	if _, err := writer.Write(endMarker[:]); err != nil {
		return Result{Status: "error", Detail: err.Error()}, err
	}
	if err := writer.Flush(); err != nil {
		return Result{Status: "error", Detail: err.Error()}, err
	}

	response, err := io.ReadAll(io.LimitReader(connection, 4096))
	if err != nil {
		return Result{Status: "error", Detail: err.Error()}, err
	}
	message := strings.TrimSpace(string(bytes.TrimRight(response, "\x00\r\n")))
	return classify(message), nil
}

func classify(message string) Result {
	result := Result{Status: "clean", Engine: "clamav", Detail: message}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "found") {
		result.Status = "infected"
		// Typical: "stream: Eicar-Test-Signature FOUND"
		trimmed := strings.TrimSuffix(message, "FOUND")
		trimmed = strings.TrimSpace(trimmed)
		if idx := strings.LastIndex(trimmed, ":"); idx >= 0 && idx < len(trimmed)-1 {
			result.Signature = strings.TrimSpace(trimmed[idx+1:])
		}
		return result
	}
	if strings.Contains(lower, "error") {
		result.Status = "error"
	}
	return result
}

// ScanFile reads a file and scans it.
func ScanFile(ctx context.Context, path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Status: "error", Detail: err.Error()}, err
	}
	result, scanErr := ScanBytes(ctx, data)
	if scanErr != nil {
		return result, fmt.Errorf("clamav scan failed: %w", scanErr)
	}
	return result, nil
}
