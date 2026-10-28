package modelprofile

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"time"

	"research-assistant-backend/internal/oaichat"
)

// ProbeResult is the outcome of a text or vision probe.
type ProbeResult struct {
	OK               bool
	Status           string // yes / no / unknown for vision; ok / error for text
	Model            string
	LatencyMS        int64
	Detail           string
	PromptTokens     int
	CompletionTokens int
}

// ProbeText checks key/base_url/model reachability with a fixed greeting.
func ProbeText(ctx context.Context, profile Profile, apiKey string, allowPrivate bool) ProbeResult {
	client := oaichat.NewClient(allowPrivate)
	started := time.Now()
	response, err := client.Complete(ctx, profile.BaseURL, apiKey, profile.Model, []oaichat.Message{
		{Role: "user", Text: "Reply with exactly the single word: PONG"},
	}, oaichat.Options{Timeout: time.Duration(profile.TimeoutSeconds) * time.Second, MaxOutputTokens: 32})
	result := ProbeResult{Model: profile.Model, LatencyMS: time.Since(started).Milliseconds()}
	if err != nil {
		result.Status = "error"
		result.Detail = err.Error()
		return result
	}
	content := strings.ToUpper(strings.TrimSpace(response.Content))
	result.OK = strings.Contains(content, "PONG")
	result.Status = "ok"
	if !result.OK {
		result.Status = "error"
		result.Detail = "response did not contain PONG: " + truncate(response.Content, 120)
	}
	result.PromptTokens = response.PromptTokens
	result.CompletionTokens = response.CompletionTokens
	return result
}

// ProbeVision sends a solid red and a solid blue image and requires both
// answers to be correct. A network failure leaves the capability unknown so it
// can be retried at runtime; a wrong answer / unsupported image_url means no.
func ProbeVision(ctx context.Context, profile Profile, apiKey string, allowPrivate bool) ProbeResult {
	client := oaichat.NewClient(allowPrivate)
	started := time.Now()
	result := ProbeResult{Model: profile.Model}
	redPNG, err := solidPNG(color.RGBA{R: 255, A: 255})
	if err != nil {
		result.Status = VisionNo
		result.Detail = "cannot render probe image: " + err.Error()
		return result
	}
	bluePNG, err := solidPNG(color.RGBA{B: 255, A: 255})
	if err != nil {
		result.Status = VisionNo
		result.Detail = "cannot render probe image: " + err.Error()
		return result
	}
	cases := []struct {
		name  string
		image []byte
		want  string
	}{
		{"red", redPNG, "red"},
		{"blue", bluePNG, "blue"},
	}
	for _, test := range cases {
		response, callErr := client.Complete(ctx, profile.BaseURL, apiKey, profile.Model, []oaichat.Message{{
			Role:   "user",
			Text:   "What is the dominant colour of this image? Reply with only one word: red or blue.",
			Images: []oaichat.ImagePart{{MIMEType: "image/png", Base64: base64.StdEncoding.EncodeToString(test.image)}},
		}}, oaichat.Options{Timeout: time.Duration(profile.TimeoutSeconds) * time.Second, MaxOutputTokens: 16})
		if callErr != nil {
			result.Status = classifyProbeError(callErr)
			result.Detail = fmt.Sprintf("%s image probe failed: %v", test.name, callErr)
			result.LatencyMS = time.Since(started).Milliseconds()
			return result
		}
		result.PromptTokens += response.PromptTokens
		result.CompletionTokens += response.CompletionTokens
		answer := strings.ToLower(strings.TrimSpace(response.Content))
		answer = strings.Trim(answer, " \"'.;:\n")
		if !strings.Contains(answer, test.want) || strings.Contains(answer, otherColour(test.want)) {
			result.Status = VisionNo
			result.Detail = fmt.Sprintf("%s image answered %q", test.name, truncate(response.Content, 80))
			result.LatencyMS = time.Since(started).Milliseconds()
			return result
		}
	}
	result.OK = true
	result.Status = VisionYes
	result.LatencyMS = time.Since(started).Milliseconds()
	return result
}

func otherColour(value string) string {
	if value == "red" {
		return "blue"
	}
	return "red"
}

func classifyProbeError(err error) string {
	if err == nil {
		return VisionUnknown
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"unsupported", "image_url", "image url", "invalid_content", "content policy", "400"} {
		if strings.Contains(message, marker) {
			return VisionNo
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "deadline") {
		return VisionUnknown
	}
	return VisionUnknown
}

func solidPNG(fill color.RGBA) ([]byte, error) {
	const size = 64
	canvas := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			canvas.Set(x, y, fill)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
