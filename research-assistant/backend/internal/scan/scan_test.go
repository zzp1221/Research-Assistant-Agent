package scan

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
)

func fakeClamd(t *testing.T, reply string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer listener.Close()
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		command, _ := reader.ReadString(0)
		_ = command
		for {
			var length [4]byte
			if _, readErr := io.ReadFull(reader, length[:]); readErr != nil {
				return
			}
			size := binary.BigEndian.Uint32(length[:])
			if size == 0 {
				break
			}
			if _, readErr := io.CopyN(io.Discard, reader, int64(size)); readErr != nil {
				return
			}
		}
		_, _ = connection.Write([]byte(reply))
	}()
	return listener.Addr().String()
}

func TestScanSkippedWithoutConfiguration(t *testing.T) {
	t.Setenv("CLAMAV_ADDR", "")
	result, err := ScanBytes(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "skipped" {
		t.Fatalf("status=%s", result.Status)
	}
}

func TestScanCleanAndInfected(t *testing.T) {
	t.Setenv("CLAMAV_ADDR", fakeClamd(t, "stream: OK\x00"))
	result, err := ScanBytes(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "clean" {
		t.Fatalf("clean status=%s detail=%s", result.Status, result.Detail)
	}

	t.Setenv("CLAMAV_ADDR", fakeClamd(t, "stream: Win.Test.EICAR_HDB-1 FOUND\x00"))
	result, err = ScanBytes(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "infected" || !strings.Contains(result.Signature, "EICAR") {
		t.Fatalf("infected result=%#v", result)
	}
}
