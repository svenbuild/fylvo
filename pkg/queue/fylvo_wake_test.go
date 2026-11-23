package queue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/task"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

type wakeTestDep struct{}

func (wakeTestDep) ForkWithLogger(ctx context.Context, l logging.Logger) context.Context { return ctx }

// wakeTestTask counts how often it ran and reports each run on done.
type wakeTestTask struct {
	*InMemoryTask
	done chan struct{}
}

func (t *wakeTestTask) Do(ctx context.Context) (task.Status, error) {
	t.done <- struct{}{}
	return task.StatusCompleted, nil
}

func newWakeTestTask(done chan struct{}) *wakeTestTask {
	return &wakeTestTask{
		InMemoryTask: &InMemoryTask{DBTask: &DBTask{Task: &ent.Task{PublicState: &types.TaskPublicState{}}}},
		done:         done,
	}
}

func newWakeTestQueue(pull time.Duration) *queue {
	q := New(logging.NewConsoleLogger(logging.LevelError), nil, nil, wakeTestDep{},
		WithWorkerCount(2), WithName("wake-test"), WithMaxTaskExecution(time.Minute)).(*queue)
	q.taskPullInterval = pull
	return q
}

// Fylvo: a task queued into an idle queue starts at once, not after the pull interval.
func TestQueueWakesIdleWorker(t *testing.T) {
	q := newWakeTestQueue(time.Hour)
	q.Start()
	defer q.Shutdown()

	// Let the worker find the queue empty and go to sleep.
	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{}, 10)
	for round := 0; round < 3; round++ {
		require.NoError(t, q.QueueTask(context.Background(), newWakeTestTask(done)))
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: task did not start although the queue was idle", round)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Fylvo: a burst of tasks all run, and an idle queue does not spin between them.
func TestQueueBurstAndNoSpin(t *testing.T) {
	q := newWakeTestQueue(time.Hour)
	var requests atomic.Int32
	q.scheduler = &countingScheduler{Scheduler: q.scheduler, requests: &requests}
	q.Start()
	defer q.Shutdown()

	done := make(chan struct{}, 50)
	for i := 0; i < 20; i++ {
		require.NoError(t, q.QueueTask(context.Background(), newWakeTestTask(done)))
	}
	for i := 0; i < 20; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 20 tasks ran", i)
		}
	}

	time.Sleep(200 * time.Millisecond)
	before := requests.Load()
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, before, requests.Load(), "an idle queue keeps asking the scheduler")
}

// Fylvo: an idle queue shuts down promptly.
func TestQueueIdleShutdown(t *testing.T) {
	q := newWakeTestQueue(time.Hour)
	q.Start()
	time.Sleep(100 * time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		q.Shutdown()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown of an idle queue hangs")
	}
}

type countingScheduler struct {
	Scheduler
	requests *atomic.Int32
}

func (s *countingScheduler) Request() (Task, error) {
	s.requests.Add(1)
	return s.Scheduler.Request()
}
