package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
)

func hasInitialization(spec domain.Spec) bool {
	for _, w := range spec.Workloads {
		if len(w.Initialize) > 0 {
			return true
		}
	}
	return false
}

type initializationEvent struct {
	Component string `json:"component"`
	Step      string `json:"step"`
	Status    string `json:"status"`
	Time      string `json:"time"`
	InputHash string `json:"input_hash,omitempty"`
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// No automatic retry: a nonzero result may still have committed partial writes.
func (d Driver) initializeComponents(ctx context.Context, r domain.Run, spec domain.Spec) error {
	f, err := os.OpenFile(filepath.Join(r.Directory, "initialization.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("initialization already attempted or journal unavailable: %w", err)
	}
	defer f.Close()
	record := func(e initializationEvent) error {
		e.Time = time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(f).Encode(e); err != nil {
			return err
		}
		return f.Sync()
	}
	for _, name := range Names(spec) {
		w := spec.Workloads[name]
		if len(w.Initialize) == 0 {
			continue
		}
		if w.Role != "infrastructure" {
			return fmt.Errorf("initialization is restricted to selected components")
		}
		var ids bytes.Buffer
		if err = d.command(ctx, &ids, "ps", "-q", "--filter", "label=io.localmock.managed=true", "--filter", "label=io.localmock.run="+r.ID, "--filter", "label=com.docker.compose.service="+name); err != nil {
			return err
		}
		containers := strings.Fields(ids.String())
		if len(containers) != 1 {
			return fmt.Errorf("expected one running component %s", name)
		}
		for _, step := range w.Initialize {
			e := initializationEvent{Component: name, Step: step.Name, Status: "started", InputHash: step.InputHash}
			if err = ctx.Err(); err != nil {
				return err
			}
			input, err := initializationInput(r, step.InputHash)
			if err != nil {
				return err
			}
			if len(step.Command) == 0 || step.TimeoutSeconds < 1 || step.TimeoutSeconds > 300 {
				return fmt.Errorf("invalid initialization execution spec")
			}
			if err = record(e); err != nil {
				return err
			}
			stepCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
			output := &boundedOutput{limit: 32 << 10}
			stepDriver := d
			stepDriver.Progress = output
			err = stepDriver.commandInput(stepCtx, bytes.NewReader(input), output, append([]string{"exec", "-i", containers[0]}, step.Command...)...)
			if stepCtx.Err() != nil {
				err = stepCtx.Err()
			}
			cancel()
			e.Status, e.Output, e.Truncated = "succeeded", output.String(), output.truncated
			if err != nil {
				e.Status, e.Error = "failed", err.Error()
			}
			if journalErr := record(e); journalErr != nil {
				return journalErr
			}
			if err != nil {
				return fmt.Errorf("component %s initialization %s failed (see initialization.jsonl): %w", name, step.Name, err)
			}
			if d.Progress != nil {
				fmt.Fprintf(d.Progress, "initialized %s/%s\n", name, step.Name)
			}
		}
	}
	return nil
}

func initializationInput(r domain.Run, hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	if b, err := hex.DecodeString(hash); err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("invalid initialization input hash")
	}
	f, err := os.Open(filepath.Join(r.Directory, "inputs", hash))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 4<<20 || fmt.Sprintf("%x", sha256.Sum256(b)) != hash {
		return nil, fmt.Errorf("initialization snapshot changed")
	}
	return b, nil
}

type boundedOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if n > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *boundedOutput) String() string { return b.buffer.String() }
