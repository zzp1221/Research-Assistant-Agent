package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.2.3", "1.2.2", 1},
		{"0.9.0", "0.10.0", -1},
		{"2.0", "2.0.0", 0},
		{"1.2.3-beta.1", "1.2.3", 0},
	}
	for _, item := range cases {
		if got := CompareVersions(item.a, item.b); got != item.want {
			t.Fatalf("CompareVersions(%q,%q) = %d, want %d", item.a, item.b, got, item.want)
		}
	}
}

func TestCheckReturnsNewerManifest(t *testing.T) {
	manifest := Manifest{Version: "0.9.2", URL: "https://example.com/a.exe", SHA256: strings64("a"), Signature: "sig"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()
	checker := &Checker{Endpoint: server.URL, CurrentVersion: "0.9.1"}
	got, available, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !available || got == nil || got.Version != "0.9.2" {
		t.Fatalf("available=%v manifest=%#v", available, got)
	}
	same := &Checker{Endpoint: server.URL, CurrentVersion: "0.9.2"}
	_, available, err = same.Check(context.Background())
	if err != nil || available {
		t.Fatalf("same version: available=%v err=%v", available, err)
	}
}

func TestDownloadAndVerify(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("update payload")
	digest := sha256.Sum256(payload)
	signature := ed25519.Sign(privateKey, []byte(hex.EncodeToString(digest[:])))
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer fileServer.Close()
	manifest := &Manifest{Version: "1.0.0", URL: fileServer.URL, SHA256: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(signature)}
	installer := &Installer{PublicKey: publicKey}
	path, err := installer.DownloadAndVerify(context.Background(), manifest, t.TempDir())
	if err != nil {
		t.Fatalf("DownloadAndVerify: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("downloaded = %q", data)
	}
}

func TestDownloadRejectsTamperedContent(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	payload := []byte("original")
	digest := sha256.Sum256(payload)
	signature := ed25519.Sign(privateKey, []byte(hex.EncodeToString(digest[:])))
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer fileServer.Close()
	manifest := &Manifest{Version: "1.0.0", URL: fileServer.URL, SHA256: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(signature)}
	installer := &Installer{PublicKey: publicKey}
	if _, err := installer.DownloadAndVerify(context.Background(), manifest, t.TempDir()); err == nil {
		t.Fatal("expected sha256 mismatch")
	}
}

func TestDownloadRejectsBadSignature(t *testing.T) {
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	payload := []byte("payload")
	digest := sha256.Sum256(payload)
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer fileServer.Close()
	manifest := &Manifest{Version: "1.0.0", URL: fileServer.URL, SHA256: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))}
	installer := &Installer{PublicKey: publicKey}
	if _, err := installer.DownloadAndVerify(context.Background(), manifest, t.TempDir()); err == nil {
		t.Fatal("expected signature failure")
	}
}

func strings64(ch string) string {
	out := ""
	for len(out) < 64 {
		out += ch
	}
	return out
}
func TestPublicKeyFromEnv(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RA_UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))
	parsed, err := PublicKeyFromEnv()
	if err != nil {
		t.Fatalf("PublicKeyFromEnv: %v", err)
	}
	if !parsed.Equal(publicKey) {
		t.Fatal("parsed key differs")
	}
	t.Setenv("RA_UPDATE_PUBLIC_KEY", "")
	if _, err := PublicKeyFromEnv(); err == nil {
		t.Fatal("empty key should fail")
	}
}
func TestManagerCheckAndDownload(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("installer-binary")
	digest := sha256.Sum256(payload)
	signature := ed25519.Sign(privateKey, []byte(hex.EncodeToString(digest[:])))
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer fileServer.Close()
	manifest := Manifest{Version: "1.0.0", URL: fileServer.URL, SHA256: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(signature)}
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer manifestServer.Close()
	manager := &Manager{
		Checker:     &Checker{Endpoint: manifestServer.URL, CurrentVersion: "0.9.0"},
		Installer:   &Installer{PublicKey: publicKey},
		DownloadDir: t.TempDir(),
	}
	status, err := manager.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !status.Available || status.Version != "1.0.0" {
		t.Fatalf("status = %#v", status)
	}
	downloaded, err := manager.Download(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if downloaded.Path == "" {
		t.Fatal("download path is empty")
	}
	data, err := os.ReadFile(downloaded.Path)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("downloaded = %q, %v", data, err)
	}
}

func TestManagerRequiresConfiguration(t *testing.T) {
	if _, err := (&Manager{}).Check(context.Background()); err == nil {
		t.Fatal("unconfigured manager should fail")
	}
	if _, err := (&Manager{}).Download(context.Background(), ""); err == nil {
		t.Fatal("unconfigured download should fail")
	}
}
func TestManagerInstall(t *testing.T) {
	installer := filepath.Join(t.TempDir(), "setup.exe")
	if err := os.WriteFile(installer, []byte("installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	launched := ""
	manager := &Manager{Launcher: func(path string) error {
		launched = path
		return nil
	}}
	if err := manager.Install(installer); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if launched != installer {
		t.Fatalf("launched = %q", launched)
	}
	if err := manager.Install(""); err == nil {
		t.Fatal("empty path should fail")
	}
	if err := manager.Install(filepath.Join(t.TempDir(), "missing.exe")); err == nil {
		t.Fatal("missing installer should fail")
	}
}
