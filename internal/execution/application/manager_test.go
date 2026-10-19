package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
)

type memoryStore struct{ runs map[string]domain.Run }

func (s *memoryStore) Insert(_ context.Context, r domain.Run) error { s.runs[r.ID] = r; return nil }
func (s *memoryStore) Save(_ context.Context, r domain.Run) error   { s.runs[r.ID] = r; return nil }
func (s *memoryStore) Get(_ context.Context, id string) (domain.Run, error) {
	r, ok := s.runs[id]
	if !ok {
		return r, errors.New("missing")
	}
	return r, nil
}
func (s *memoryStore) List(context.Context) ([]domain.Run, error) { return nil, nil }

type noLock struct{}

func (noLock) Acquire(string) (func(), error) { return func() {}, nil }

type fakeDriver struct {
	upErr             error
	prepareErr        error
	downErr           error
	downCount         int
	logCount          int
	cleanupContextErr error
}

func (d *fakeDriver) Prepare(context.Context, domain.Run, domain.Spec) error { return d.prepareErr }
func (d *fakeDriver) Up(context.Context, domain.Run) error                   { return d.upErr }
func (d *fakeDriver) Stop(context.Context, domain.Run) error                 { return nil }
func (d *fakeDriver) Down(ctx context.Context, _ domain.Run) error {
	d.downCount++
	d.cleanupContextErr = ctx.Err()
	return d.downErr
}
func (d *fakeDriver) Status(context.Context, domain.Run) (json.RawMessage, error) { return nil, nil }
func (d *fakeDriver) Logs(context.Context, domain.Run, io.Writer) error           { d.logCount++; return nil }
func managerFixture(t *testing.T) (Manager, *fakeDriver, *memoryStore) {
	t.Helper()
	s := &memoryStore{runs: map[string]domain.Run{}}
	d := &fakeDriver{}
	return Manager{Store: s, Driver: d, Locker: noLock{}, Root: t.TempDir()}, d, s
}
func TestStartReadyAndRepeatedDown(t *testing.T) {
	m, d, s := managerFixture(t)
	run, err := m.Start(context.Background(), "example", domain.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	if s.runs[run.ID].State != domain.Ready {
		t.Fatal("not ready")
	}
	for i := 0; i < 2; i++ {
		if _, err = m.Down(context.Background(), run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if d.downCount != 1 || s.runs[run.ID].State != domain.Removed {
		t.Fatal("down is not idempotent")
	}
}
func TestFailedStartupCapturesLogsAndCleans(t *testing.T) {
	m, d, s := managerFixture(t)
	d.upErr = errors.New("unhealthy")
	run, err := m.Start(context.Background(), "example", domain.Spec{})
	if err == nil || d.logCount != 1 || d.downCount != 1 || s.runs[run.ID].State != domain.Failed {
		t.Fatalf("%v %+v %+v", err, d, s.runs)
	}
}
func TestCancellationUsesIndependentCleanupContext(t *testing.T) {
	m, d, _ := managerFixture(t)
	d.upErr = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Start(ctx, "example", domain.Spec{})
	if !errors.Is(err, context.Canceled) || d.cleanupContextErr != nil {
		t.Fatalf("%v %+v", err, d)
	}
}
func TestPrepareFailureDoesNotRemoveOtherResources(t *testing.T) {
	m, d, _ := managerFixture(t)
	d.prepareErr = errors.New("bad input")
	_, err := m.Start(context.Background(), "example", domain.Spec{})
	if err == nil || d.downCount != 0 {
		t.Fatal("unexpected cleanup")
	}
}
func TestCleanupFailureIsRecordedAndCanBeRetried(t *testing.T) {
	m, d, s := managerFixture(t)
	d.upErr = errors.New("unhealthy")
	d.downErr = errors.New("daemon unavailable")
	run, err := m.Start(context.Background(), "example", domain.Spec{})
	if err == nil || s.runs[run.ID].Error == "" {
		t.Fatal("missing failure")
	}
	d.downErr = nil
	if _, err = m.Down(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
}
