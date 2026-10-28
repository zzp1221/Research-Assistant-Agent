package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
)

func makePNG(t *testing.T, width, height int, fill func(x, y int) color.Color) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, fill(x, y))
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

func patternedPNG(t *testing.T, width, height int) []byte {
	return makePNG(t, width, height, func(x, y int) color.Color {
		if (x/8+y/8)%2 == 0 {
			return color.RGBA{R: 20, G: 40, B: 120, A: 255}
		}
		return color.RGBA{R: 245, G: 240, B: 210, A: 255}
	})
}

func TestStructuralCheckRejectsBlankImages(t *testing.T) {
	solid := makePNG(t, 64, 64, func(int, int) color.Color { return color.RGBA{R: 255, G: 255, B: 255, A: 255} })
	if report := StructuralCheck(solid); report.OK {
		t.Fatalf("solid white image must fail the structural gate")
	}
	transparent := makePNG(t, 64, 64, func(int, int) color.Color { return color.RGBA{} })
	if report := StructuralCheck(transparent); report.OK {
		t.Fatalf("fully transparent image must fail the structural gate")
	}
	tiny := makePNG(t, 1, 1, func(int, int) color.Color { return color.RGBA{R: 255, A: 255} })
	if report := StructuralCheck(tiny); report.OK {
		t.Fatalf("1x1 image must fail the structural gate")
	}
	if report := StructuralCheck([]byte("not an image")); report.OK {
		t.Fatalf("corrupt image must fail the structural gate")
	}
	valid := patternedPNG(t, 320, 240)
	if report := StructuralCheck(valid); !report.OK {
		t.Fatalf("patterned figure must pass, issues=%v", report.Issues)
	}
}

func TestParseVisionVerdictStrictContract(t *testing.T) {
	valid := `{"verdict":"PASS","confidence":1.4,"summary":"ok","issues":[],"evidence":["two series"]}`
	verdict, err := parseVisionVerdict("```json\n" + valid + "\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if verdict.Verdict != "pass" || verdict.Confidence != 1 {
		t.Fatalf("verdict = %#v", verdict)
	}
	if _, err := parseVisionVerdict("not json"); err == nil {
		t.Fatalf("non-JSON must fail")
	}
	if _, err := parseVisionVerdict(`{"verdict":"maybe"}`); err == nil {
		t.Fatalf("unknown verdict must fail")
	}
	unknownFields := `{"verdict":"fail","confidence":0.4,"summary":"x","issues":["clipped axis"],"evidence":[],"extra":"ignored"}`
	parsed, err := parseVisionVerdict(unknownFields)
	if err != nil || parsed.Verdict != "fail" || len(parsed.Issues) != 1 {
		t.Fatalf("unknown fields must be ignored: %#v %v", parsed, err)
	}
}

type visionServer struct {
	server  *httptest.Server
	calls   atomic.Int64
	payload func(call int64) string
}

func newVisionServer(t *testing.T, payload func(call int64) string) *visionServer {
	t.Helper()
	srv := &visionServer{payload: payload}
	srv.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		call := srv.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1,
			"model":   "test-vision",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": srv.payload(call)}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
		})
	}))
	t.Cleanup(srv.server.Close)
	return srv
}

func newTestResolver(t *testing.T, baseURL string, supportsVision string) *modelresolver.Resolver {
	t.Helper()
	if err := os.Setenv("MODEL_PROFILE_ENCRYPTION_KEY", "dGVzdC1rZXktMzItYnl0ZXMtZm9yLWFlcy0yNTYtZ2NtISE="); err != nil {
		t.Fatalf("env: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("MODEL_PROFILE_ENCRYPTION_KEY") })
	if err := os.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true"); err != nil {
		t.Fatalf("env: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("ALLOW_PRIVATE_MODEL_ENDPOINTS") })
	store, err := modelprofile.NewStore("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	profile, err := store.Create("owner", modelprofile.Input{
		Name:           "vision",
		BaseURL:        baseURL,
		Model:          "test-vision",
		APIKey:         "test-key",
		Roles:          []string{"main", "vision"},
		SupportsVision: supportsVision,
	}, 10)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if _, err := store.SaveRouting("owner", profile.ID, []string{profile.ID}, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	return modelresolver.New(store)
}

func TestVerifyPassFailInconclusiveAndSkipped(t *testing.T) {
	imageData := patternedPNG(t, 320, 240)
	passServer := newVisionServer(t, func(int64) string {
		return `{"verdict":"pass","confidence":0.9,"summary":"figure matches the request","issues":[],"evidence":["two series visible"]}`
	})
	resolver := newTestResolver(t, passServer.server.URL, modelprofile.VisionYes)
	verifier := NewVisualVerifier(resolver)
	result := verifier.Verify(context.Background(), VerifyInput{
		Intent:  "compare two curves",
		Images:  []ImageInput{{Key: "plot_image", Data: imageData}},
		OwnerID: "owner",
	})
	if result.Status != "pass" || !result.Verified || result.Level != "visual" {
		t.Fatalf("pass verification = %#v", result)
	}
	if result.VerifiedBy == nil || result.VerifiedBy.ProfileID == "" {
		t.Fatalf("verified_by missing: %#v", result.VerifiedBy)
	}
	if result.TokenUsage == nil || result.TokenUsage.Prompt == 0 {
		t.Fatalf("token usage missing")
	}

	failServer := newVisionServer(t, func(int64) string {
		return `{"verdict":"fail","confidence":0.7,"summary":"axis label missing","issues":["x axis has no unit"],"evidence":[]}`
	})
	failVerifier := NewVisualVerifier(newTestResolver(t, failServer.server.URL, modelprofile.VisionYes))
	failResult := failVerifier.Verify(context.Background(), VerifyInput{Intent: "plot", Images: []ImageInput{{Key: "plot_image", Data: imageData}}, OwnerID: "owner"})
	if failResult.Status != "fail" || failResult.Level != "visual" || len(failResult.Issues) == 0 {
		t.Fatalf("fail verification = %#v", failResult)
	}

	noVisionStore, err := modelprofile.NewStore("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	inconclusive := NewVisualVerifier(modelresolver.New(noVisionStore)).Verify(context.Background(), VerifyInput{
		Intent: "plot", Images: []ImageInput{{Key: "plot_image", Data: imageData}}, OwnerID: "owner",
	})
	if inconclusive.Status != "inconclusive" || inconclusive.Level != "structural" {
		t.Fatalf("no-vision verification = %#v", inconclusive)
	}
	if !strings.Contains(inconclusive.Reason, "no vision-capable") {
		t.Fatalf("reason = %q", inconclusive.Reason)
	}

	skipped := NewVisualVerifier(nil).Verify(context.Background(), VerifyInput{Intent: "chat"})
	if skipped.Status != "skipped" || skipped.Level != "none" {
		t.Fatalf("skipped verification = %#v", skipped)
	}
}

func TestVisionFallbackAndGarbageJSON(t *testing.T) {
	imageData := patternedPNG(t, 320, 240)
	garbage := newVisionServer(t, func(int64) string { return "I think it looks fine, definitely not JSON" })
	garbageResolver := newTestResolver(t, garbage.server.URL, modelprofile.VisionYes)
	result := NewVisualVerifier(garbageResolver).Verify(context.Background(), VerifyInput{
		Intent: "plot", Images: []ImageInput{{Key: "plot_image", Data: imageData}}, OwnerID: "owner",
	})
	if result.Status != "inconclusive" {
		t.Fatalf("garbage JSON must be inconclusive, got %#v", result)
	}
	if garbage.calls.Load() < 2 {
		t.Fatalf("expected one repair retry, calls=%d", garbage.calls.Load())
	}

	working := newVisionServer(t, func(int64) string {
		return `{"verdict":"pass","confidence":0.6,"summary":"ok","issues":[],"evidence":[]}`
	})
	store, _ := modelprofile.NewStore("")
	if err := os.Setenv("MODEL_PROFILE_ENCRYPTION_KEY", "dGVzdC1rZXktMzItYnl0ZXMtZm9yLWFlcy0yNTYtZ2NtISE="); err != nil {
		t.Fatalf("env: %v", err)
	}
	plain, err := store.Create("owner", modelprofile.Input{Name: "plain", BaseURL: "https://plain.example/v1", Model: "plain", APIKey: "k", Roles: []string{"main"}, SupportsVision: modelprofile.VisionNo}, 10)
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	vision, err := store.Create("owner", modelprofile.Input{Name: "vision", BaseURL: working.server.URL, Model: "test-vision", APIKey: "k", Roles: []string{"vision"}, SupportsVision: modelprofile.VisionYes}, 10)
	if err != nil {
		t.Fatalf("create vision: %v", err)
	}
	if _, err := store.SaveRouting("owner", plain.ID, []string{vision.ID}, nil); err != nil {
		t.Fatalf("routing: %v", err)
	}
	fallback := NewVisualVerifier(modelresolver.New(store)).Verify(context.Background(), VerifyInput{
		Intent: "plot", Images: []ImageInput{{Key: "plot_image", Data: imageData}}, OwnerID: "owner",
	})
	if fallback.Status != "pass" {
		t.Fatalf("fallback verification = %#v", fallback)
	}
	if fallback.VerifiedBy == nil || fallback.VerifiedBy.Mode != "vision_fallback" {
		t.Fatalf("fallback mode = %#v", fallback.VerifiedBy)
	}
}

func TestVerifyStructuralFailureSkipsVision(t *testing.T) {
	server := newVisionServer(t, func(int64) string {
		return `{"verdict":"pass","confidence":0.9,"summary":"ok","issues":[],"evidence":[]}`
	})
	blank := makePNG(t, 64, 64, func(int, int) color.Color { return color.RGBA{R: 255, G: 255, B: 255, A: 255} })
	result := NewVisualVerifier(newTestResolver(t, server.server.URL, modelprofile.VisionYes)).Verify(context.Background(), VerifyInput{
		Intent: "plot", Images: []ImageInput{{Key: "plot_image", Data: blank}}, OwnerID: "owner",
	})
	if result.Status != "fail" || result.Level != "structural" {
		t.Fatalf("structural failure = %#v", result)
	}
	if server.calls.Load() != 0 {
		t.Fatalf("vision must not be called after a structural failure, calls=%d", server.calls.Load())
	}
}
