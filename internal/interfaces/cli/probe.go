package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	execapp "github.com/daidaidahaidai/local-mock/internal/execution/application"
	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	probeimpl "github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/probe"
)

func (c CLI) probe(ctx context.Context, args []string) (int, error) {
	f := flag.NewFlagSet("probe", flag.ContinueOnError)
	f.SetOutput(c.Err)
	state := f.String("state", defaultState(), "state directory")
	deployment := f.String("deployment", "", "deployment or component name")
	endpoint := f.String("interface", "", "declared endpoint name")
	path := f.String("path", "", "HTTP path")
	method := f.String("method", "", "gRPC package.Service/Method")
	data := f.String("data", "", "protobuf JSON request")
	dataFile := f.String("data-file", "", "protobuf JSON request file")
	timeout := f.Duration("timeout", 20*time.Second, "operation timeout")
	f.Bool("json", true, "JSON output (always enabled)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 1 || *deployment == "" || *endpoint == "" || *timeout <= 0 {
		return 2, fmt.Errorf("probe requires --deployment, --interface and one run ID")
	}
	request := []byte(*data)
	if *dataFile != "" {
		if *data != "" {
			return 2, fmt.Errorf("use --data or --data-file")
		}
		var err error
		request, err = os.ReadFile(*dataFile)
		if err != nil {
			return 2, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	manager, store, err := c.manager(*state)
	if err != nil {
		return 1, err
	}
	defer store.Close()
	r, err := store.Get(ctx, f.Arg(0))
	if err != nil {
		return 1, err
	}
	if r.State != domain.Ready {
		return 1, fmt.Errorf("run must be ready for probing")
	}
	result, err := manager.Driver.(execapp.Prober).Probe(ctx, r, domain.Probe{Deployment: *deployment, Interface: *endpoint, Path: *path, Method: *method, Request: request})
	if err != nil {
		return 1, err
	}
	if err = c.json(result); err != nil {
		return 1, err
	}
	if result.Error != "" {
		return 1, fmt.Errorf("%s", result.Error)
	}
	return 0, nil
}

// Internal helper commands have no Docker access or model credentials.
func (c CLI) runtimeCommand(ctx context.Context, args []string) (int, error) {
	if len(args) != 1 {
		return 2, fmt.Errorf("internal runtime command required")
	}
	switch args[0] {
	case "idle":
		<-ctx.Done()
		return 0, nil
	case "health":
		return 0, nil
	case "probe":
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 32<<20))
		decoder.DisallowUnknownFields()
		var call domain.ProbeCall
		if err := decoder.Decode(&call); err != nil {
			return 2, err
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return 0, c.json(probeimpl.Execute(ctx, call))
	default:
		return 2, fmt.Errorf("unknown internal runtime command")
	}
}
