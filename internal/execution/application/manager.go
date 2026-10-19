package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
)

type Store interface {
	Insert(context.Context, domain.Run) error
	Save(context.Context, domain.Run) error
	Get(context.Context, string) (domain.Run, error)
	List(context.Context) ([]domain.Run, error)
}
type Driver interface {
	Prepare(context.Context, domain.Run, domain.Spec) error
	Up(context.Context, domain.Run) error
	Stop(context.Context, domain.Run) error
	Down(context.Context, domain.Run) error
	Status(context.Context, domain.Run) (json.RawMessage, error)
	Logs(context.Context, domain.Run, io.Writer) error
}
type Locker interface{ Acquire(string) (func(), error) }
type Manager struct {
	Store  Store
	Driver Driver
	Locker Locker
	Root   string
}

func (m Manager) Start(ctx context.Context, name string, spec domain.Spec) (domain.Run, error) {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return domain.Run{}, err
	}
	id := hex.EncodeToString(token[:])
	run := domain.Run{ID: id, Scenario: name, Project: "localmock-" + id, Directory: filepath.Join(m.Root, "runs", id), State: domain.Starting, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := os.MkdirAll(run.Directory, 0700); err != nil {
		return run, err
	}
	unlock, err := m.Locker.Acquire(id)
	if err != nil {
		return run, err
	}
	defer unlock()
	if err = m.Store.Insert(ctx, run); err != nil {
		return run, err
	}
	if err = m.Driver.Prepare(ctx, run, spec); err != nil {
		return run, m.fail(run, err, false)
	}
	if err = m.Driver.Up(ctx, run); err != nil {
		return run, m.fail(run, err, true)
	}
	if err = run.Transition(domain.Ready); err != nil {
		return run, err
	}
	if err = m.Store.Save(ctx, run); err != nil {
		return run, m.fail(run, err, true)
	}
	return run, nil
}
func (m Manager) fail(run domain.Run, cause error, cleanup bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var cleanupErr error
	if cleanup {
		m.capture(ctx, run)
		cleanupErr = m.Driver.Down(ctx, run)
	}
	_ = run.Transition(domain.Failed)
	run.Error = cause.Error()
	if cleanupErr != nil {
		run.Error += "; cleanup failed: " + cleanupErr.Error()
	}
	saveErr := m.Store.Save(ctx, run)
	return errors.Join(fmt.Errorf("run %s: %w", run.ID, cause), cleanupErr, saveErr)
}
func (m Manager) Stop(ctx context.Context, id string) (domain.Run, error) {
	return m.change(ctx, id, false)
}
func (m Manager) Down(ctx context.Context, id string) (domain.Run, error) {
	return m.change(ctx, id, true)
}
func (m Manager) change(ctx context.Context, id string, remove bool) (domain.Run, error) {
	unlock, err := m.Locker.Acquire(id)
	if err != nil {
		return domain.Run{}, err
	}
	defer unlock()
	run, err := m.Store.Get(ctx, id)
	if err != nil {
		return run, err
	}
	if run.State == domain.Removed {
		return run, nil
	}
	if !remove && run.State == domain.Starting {
		return run, fmt.Errorf("interrupted start: use down to clean run %s", id)
	}
	m.capture(ctx, run)
	if remove {
		err = m.Driver.Down(ctx, run)
	} else {
		err = m.Driver.Stop(ctx, run)
	}
	if err != nil {
		return run, err
	}
	next := domain.Stopped
	if remove {
		next = domain.Removed
	}
	if err = run.Transition(next); err != nil {
		return run, err
	}
	return run, m.Store.Save(ctx, run)
}
func (m Manager) capture(ctx context.Context, run domain.Run) {
	f, err := os.OpenFile(filepath.Join(run.Directory, "workload.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = m.Driver.Logs(ctx, run, f)
}
