package sandboxserver

import "testing"

func TestDockerGPURequest(t *testing.T) {
	t.Setenv("SANDBOX_DOCKER_GPUS", " all ")
	if got := dockerGPURequest(); got != "all" {
		t.Fatalf("dockerGPURequest() = %q, want all", got)
	}

	t.Setenv("SANDBOX_DOCKER_GPUS", "none")
	if got := dockerGPURequest(); got != "" {
		t.Fatalf("dockerGPURequest() = %q, want empty", got)
	}
}

func TestExtractContainerIDRejectsNonHexLines(t *testing.T) {
	if got := extractContainerIDFromDockerRunOutput("pull progress\nnot-a-container-id\n"); got != "" {
		t.Fatalf("unexpected container id: %q", got)
	}
	if got := extractContainerIDFromDockerRunOutput("pull progress\n0123456789ab\n"); got != "0123456789ab" {
		t.Fatalf("unexpected container id: %q", got)
	}
}
