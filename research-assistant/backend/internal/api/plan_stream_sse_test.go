package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

// planStreamFixture drives the real SSE handler over a real HTTP connection so
// the test observes exactly what an EventSource client would receive, including
// flush timing and stream termination.
type planStreamFixture struct {
	t        *testing.T
	runtime  *planRuntime
	planBase store.PlanStore
	bus      *events.Bus
	server   *httptest.Server
	owner    string
	planID   string
}

type sseFrame struct {
	ID    int64
	Event string
	Data  string
}

func newPlanStreamFixture(t *testing.T, owner, planID string) *planStreamFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	runner := scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), bus, 1)
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, runner, bus)
	router := gin.New()
	runtime.register(router.Group("/api"))
	seedRevisionPlan(t, planStore, owner, planID)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &planStreamFixture{
		t: t, runtime: runtime, planBase: planStore, bus: bus,
		server: server, owner: owner, planID: planID,
	}
}

func (f *planStreamFixture) appendEvent(eventType string, timestamp time.Time, payload map[string]any) models.PlanEvent {
	f.t.Helper()
	stored, err := f.planBase.AppendEventReturning(f.planID, models.PlanEvent{
		PlanID: f.planID, EventType: eventType, Payload: payload, Timestamp: timestamp,
	})
	if err != nil {
		f.t.Fatalf("append %s: %v", eventType, err)
	}
	return stored
}

func (f *planStreamFixture) publish(event models.PlanEvent) {
	f.bus.Publish(f.planID, event)
}

func (f *planStreamFixture) updatePlan(mutate func(plan *models.PlanGraph)) {
	f.t.Helper()
	if err := f.planBase.UpdatePlan(f.planID, func(plan *models.PlanGraph) error {
		mutate(plan)
		return nil
	}); err != nil {
		f.t.Fatalf("update plan: %v", err)
	}
}

// open starts an SSE request and returns the frame channel plus a cancel func.
// Cancelling the request is how a real client disconnects.
func (f *planStreamFixture) open(afterSeq int64) (<-chan sseFrame, context.CancelFunc, *http.Response) {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	url := f.server.URL + "/api/plans/" + f.planID + "/stream"
	if afterSeq > 0 {
		url += "?after_seq=" + strconv.FormatInt(afterSeq, 10)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		f.t.Fatalf("build stream request: %v", err)
	}
	request.Header.Set(userIDHeaderName, f.owner)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		f.t.Fatalf("open stream: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		cancel()
		f.t.Fatalf("stream status=%d body=%s", response.StatusCode, body)
	}
	frames := make(chan sseFrame, 64)
	go func() {
		defer close(frames)
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		current := sseFrame{}
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if current.Event != "" || current.ID > 0 {
					frames <- current
					current = sseFrame{}
				}
			case strings.HasPrefix(line, "id: "):
				if value, convErr := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id: ")), 10, 64); convErr == nil {
					current.ID = value
				}
			case strings.HasPrefix(line, "event: "):
				current.Event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				current.Data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	return frames, cancel, response
}

// collectPlanFrames reads until the stream delivers `want` plan_event frames or
// the deadline expires. It reports every non-heartbeat frame it saw so a test
// can assert on ordering and duplication.
func collectPlanFrames(t *testing.T, frames <-chan sseFrame, want int, deadline time.Duration) ([]sseFrame, bool) {
	t.Helper()
	var collected []sseFrame
	timeout := time.After(deadline)
	for len(collected) < want {
		select {
		case frame, ok := <-frames:
			if !ok {
				return collected, true
			}
			if frame.Event == "heartbeat" {
				continue
			}
			collected = append(collected, frame)
		case <-timeout:
			return collected, false
		}
	}
	return collected, false
}

// TestPlanStreamReplayToleratesOutOfOrderBusPublication covers the live
// sequence inversion: seq N+1 is published before N, and the stream must still
// write N then N+1 exactly once, driven by the durable log rather than the bus.
func TestPlanStreamReplayToleratesOutOfOrderBusPublication(t *testing.T) {
	f := newPlanStreamFixture(t, "owner-stream", "plan-stream-order")
	startedAt := time.Now().UTC().Add(-time.Minute)
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &startedAt
	})
	f.appendEvent("plan_started", startedAt, nil)

	frames, cancel, _ := f.open(0)
	defer cancel()
	if _, open := collectPlanFrames(t, frames, 1, 3*time.Second); open {
		t.Fatal("stream closed before the run finished")
	}

	// Two durable events, published to the lossy bus in reverse order.
	lower := f.appendEvent("task_logs", startedAt.Add(time.Second), map[string]any{"chunk": "first"})
	upper := f.appendEvent("task_completed", startedAt.Add(2*time.Second), map[string]any{"chunk": "second"})
	if lower.Seq >= upper.Seq {
		t.Fatalf("fixture seq not increasing: %d then %d", lower.Seq, upper.Seq)
	}
	f.publish(upper)
	f.publish(lower)

	collected, closed := collectPlanFrames(t, frames, 2, 3*time.Second)
	if len(collected) != 2 {
		t.Fatalf("got %d frames, want the two durable events: %+v", len(collected), collected)
	}
	if collected[0].ID != lower.Seq || collected[1].ID != upper.Seq {
		t.Fatalf("out-of-order publication produced seq %d,%d; want %d,%d",
			collected[0].ID, collected[1].ID, lower.Seq, upper.Seq)
	}
	if closed {
		t.Fatal("stream closed before the plan reached a terminal state")
	}

	// The real terminal closes the stream once its own run ends.
	terminal := f.appendEvent("plan_completed", startedAt.Add(3*time.Second), map[string]any{"outcome": "completed"})
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusCompleted
		finishedAt := startedAt.Add(3 * time.Second)
		plan.Usage.FinishedAt = &finishedAt
	})
	f.publish(terminal)

	final, streamClosed := collectPlanFrames(t, frames, 1, 3*time.Second)
	if len(final) != 1 || final[0].ID != terminal.Seq {
		t.Fatalf("terminal frame=%+v, want id %d", final, terminal.Seq)
	}
	if !streamClosed {
		// The terminal frame arrived; the handler must now return.
		select {
		case _, ok := <-frames:
			if ok {
				t.Fatal("unexpected frame after the terminal event")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("stream did not close after the run's terminal event")
		}
	}
}

// TestPlanStreamHealsLostLowerBusWakeup covers the intentionally lossy bus: the
// wakeup for seq N never arrives, so only N+1 wakes the subscriber. The durable
// replay must still deliver N before N+1.
func TestPlanStreamHealsLostLowerBusWakeup(t *testing.T) {
	f := newPlanStreamFixture(t, "owner-stream", "plan-stream-lossy")
	startedAt := time.Now().UTC().Add(-time.Minute)
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &startedAt
	})
	f.appendEvent("plan_started", startedAt, nil)

	frames, cancel, _ := f.open(0)
	defer cancel()
	if _, open := collectPlanFrames(t, frames, 1, 3*time.Second); open {
		t.Fatal("stream closed before the run finished")
	}

	lower := f.appendEvent("task_logs", startedAt.Add(time.Second), map[string]any{"chunk": "dropped-wakeup"})
	upper := f.appendEvent("task_progress", startedAt.Add(2*time.Second), map[string]any{"chunk": "surviving-wakeup"})
	// Only the higher seq is published: the lower bus message was dropped.
	f.publish(upper)

	collected, closed := collectPlanFrames(t, frames, 2, 3*time.Second)
	if len(collected) != 2 {
		t.Fatalf("got %d frames, want both durable events: %+v", len(collected), collected)
	}
	if collected[0].ID != lower.Seq || collected[1].ID != upper.Seq {
		t.Fatalf("lost wakeup produced seq %d,%d; want %d,%d",
			collected[0].ID, collected[1].ID, lower.Seq, upper.Seq)
	}
	if closed {
		t.Fatal("stream closed before the plan reached a terminal state")
	}
	cancel()
}

// TestPlanStreamKeepsRunningAfterHistoricalTerminal covers the second bug: a
// fresh stream (after_seq=0) on a plan whose previous run completed must replay
// the old terminal, keep going, and then follow the new running run instead of
// closing on the historical terminal.
func TestPlanStreamKeepsRunningAfterHistoricalTerminal(t *testing.T) {
	f := newPlanStreamFixture(t, "owner-stream", "plan-stream-rerun")
	oldStart := time.Now().UTC().Add(-10 * time.Minute)
	f.appendEvent("plan_started", oldStart, nil)
	f.appendEvent("plan_completed", oldStart.Add(time.Minute), map[string]any{"outcome": "completed"})

	// Second run: newer usage.started_at, still in progress.
	newStart := time.Now().UTC().Add(-30 * time.Second)
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &newStart
		plan.Usage.FinishedAt = nil
	})
	secondStart := f.appendEvent("plan_started", newStart.Add(time.Second), nil)
	logEvent := f.appendEvent("task_logs", newStart.Add(2*time.Second), map[string]any{"chunk": "second run"})
	if secondStart.Seq == 0 || secondStart.Seq >= logEvent.Seq {
		t.Fatalf("second run seq not increasing: %d then %d", secondStart.Seq, logEvent.Seq)
	}

	frames, cancel, _ := f.open(0)
	collected, closed := collectPlanFrames(t, frames, 4, 3*time.Second)
	if len(collected) != 4 {
		t.Fatalf("got %d frames, want the full history including the new run: %+v", len(collected), collected)
	}
	ids := make([]int64, 0, len(collected))
	for _, frame := range collected {
		ids = append(ids, frame.ID)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("replay seq not increasing: %v", ids)
		}
	}
	if closed {
		t.Fatalf("stream closed on the historical terminal instead of following the new run: %v", ids)
	}
	if collected[len(collected)-1].ID != logEvent.Seq {
		t.Fatalf("new run logs not replayed last: %+v (want id %d)", collected, logEvent.Seq)
	}
	if !strings.Contains(collected[len(collected)-1].Data, secondStartTimestampMarker) {
		t.Fatalf("last replayed event is not the new run's log: %s", collected[len(collected)-1].Data)
	}

	// Still open: nothing further arrives, and only a client disconnect ends it.
	select {
	case frame, ok := <-frames:
		if ok && frame.Event != "heartbeat" {
			t.Fatalf("unexpected frame while the run is active: %+v", frame)
		}
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("stream kept delivering frames after the client disconnected")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after the client disconnected")
	}
}

const secondStartTimestampMarker = "second run"

// TestPlanStreamIgnoresZeroSeqBusPublication proves a zero-seq bus message can
// never become speculative UI state: it carries no cursor, so the stream must
// ignore it unless the durable log backs the same event.
func TestPlanStreamIgnoresZeroSeqBusPublication(t *testing.T) {
	f := newPlanStreamFixture(t, "owner-stream", "plan-stream-seq0")
	startedAt := time.Now().UTC().Add(-time.Minute)
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &startedAt
	})
	f.appendEvent("plan_started", startedAt, nil)

	frames, cancel, _ := f.open(0)
	defer cancel()
	if _, open := collectPlanFrames(t, frames, 1, 3*time.Second); open {
		t.Fatal("stream closed before the run finished")
	}

	f.publish(models.PlanEvent{
		PlanID:    f.planID,
		EventType: "task_logs",
		Payload:   map[string]any{"chunk": "speculative-ui-state"},
		Timestamp: time.Now().UTC(),
	})

	select {
	case frame, ok := <-frames:
		if ok && frame.Event != "heartbeat" {
			t.Fatalf("zero-seq bus publication produced a stream frame: %+v", frame)
		}
	case <-time.After(400 * time.Millisecond):
	}

	// A durable event after the zero-seq message still flows, with a real id.
	durable := f.appendEvent("task_logs", startedAt.Add(time.Second), map[string]any{"chunk": "durable"})
	f.publish(models.PlanEvent{PlanID: f.planID, EventType: "task_logs", Payload: map[string]any{"chunk": "forged"}, Timestamp: time.Now().UTC()})

	collected, closed := collectPlanFrames(t, frames, 1, 3*time.Second)
	if len(collected) != 1 {
		t.Fatalf("got %d frames, want the durable event: %+v", len(collected), collected)
	}
	if collected[0].ID != durable.Seq {
		t.Fatalf("frame id=%d, want durable seq %d", collected[0].ID, durable.Seq)
	}
	if strings.Contains(collected[0].Data, "speculative-ui-state") {
		t.Fatalf("speculative payload leaked into the stream: %s", collected[0].Data)
	}
	if closed {
		t.Fatal("stream closed before the plan reached a terminal state")
	}
	cancel()
}

// TestPlanStreamTerminalAfterStatusUpdateCloses proves the documented ordering
// hazard: the plan snapshot flips to terminal before its terminal event is
// persisted, so the replay that runs in between must not close the stream.
func TestPlanStreamTerminalAfterStatusUpdateCloses(t *testing.T) {
	f := newPlanStreamFixture(t, "owner-stream", "plan-stream-terminal-race")
	startedAt := time.Now().UTC().Add(-time.Minute)
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &startedAt
	})
	f.appendEvent("plan_started", startedAt, nil)

	frames, cancel, _ := f.open(0)
	defer cancel()
	if _, open := collectPlanFrames(t, frames, 1, 3*time.Second); open {
		t.Fatal("stream closed before the run finished")
	}

	// Status becomes terminal but the terminal event is not written yet.
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusCompleted
		finishedAt := time.Now().UTC()
		plan.Usage.FinishedAt = &finishedAt
	})
	f.publish(models.PlanEvent{PlanID: f.planID, EventType: "task_completed", Timestamp: startedAt.Add(time.Second)})

	select {
	case frame, ok := <-frames:
		if ok && frame.Event != "heartbeat" {
			t.Fatalf("premature frame before the terminal event: %+v", frame)
		}
	case <-time.After(400 * time.Millisecond):
	}

	terminal := f.appendEvent("plan_completed", startedAt.Add(2*time.Second), map[string]any{"outcome": "completed"})
	f.publish(terminal)

	collected, _ := collectPlanFrames(t, frames, 1, 3*time.Second)
	if len(collected) != 1 || collected[0].ID != terminal.Seq {
		t.Fatalf("terminal frame=%+v, want id %d", collected, terminal.Seq)
	}
	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("unexpected frame after the terminal event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close after the terminal event was delivered")
	}
}

// TestPlanStreamFreshCursorOnTerminalCloses covers the reconnect case: a client
// whose cursor already covers the terminal run must not hang.
func TestPlanStreamFreshCursorOnTerminalCloses(t *testing.T) {
	f := newPlanStreamFixture(t, "owner-stream", "plan-stream-fresh-cursor")
	startedAt := time.Now().UTC().Add(-time.Minute)
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &startedAt
	})
	first := f.appendEvent("plan_started", startedAt, nil)
	terminal := f.appendEvent("plan_completed", startedAt.Add(time.Second), map[string]any{"outcome": "completed"})
	f.updatePlan(func(plan *models.PlanGraph) {
		plan.Status = models.StatusCompleted
		finishedAt := startedAt.Add(time.Second)
		plan.Usage.FinishedAt = &finishedAt
	})

	frames, cancel, _ := f.open(terminal.Seq)
	defer cancel()
	select {
	case frame, ok := <-frames:
		if ok {
			t.Fatalf("fresh cursor received a frame it already had: %+v", frame)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("stream did not close on a fresh cursor past the terminal (first seq %d)", first.Seq)
	}

	// The same fixture from scratch replays the terminal and closes.
	frames, cancelAll, _ := f.open(0)
	defer cancelAll()
	collected, _ := collectPlanFrames(t, frames, 2, 3*time.Second)
	if len(collected) != 2 || collected[1].ID != terminal.Seq {
		t.Fatalf("full replay frames=%+v, want terminal id %d", collected, terminal.Seq)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(collected[1].Data), &payload); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	if payload["event_type"] != "plan_completed" {
		t.Fatalf("terminal payload=%v", payload)
	}
}
