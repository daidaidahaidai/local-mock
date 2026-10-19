package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"

	execapp "github.com/daidaidahaidai/local-mock/internal/execution/application"
	"github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/docker"
	"github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/lock"
	"github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/sqlite"
	agent "github.com/daidaidahaidai/local-mock/internal/orchestration/application"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/infrastructure/deepseek"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/infrastructure/repository"
	scenarioapp "github.com/daidaidahaidai/local-mock/internal/scenario/application"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

const Version = "0.1.0-dev"

type CLI struct{ Out, Err io.Writer }

func (c CLI) Run(ctx context.Context, args []string) int {
	code, err := c.run(ctx, args)
	if err != nil {
		fmt.Fprintln(c.Err, "error:", err)
	}
	if ctx.Err() != nil {
		return 130
	}
	return code
}
func (c CLI) run(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		c.help()
		return 0, nil
	}
	if args[0] == "version" {
		fmt.Fprintln(c.Out, Version)
		return 0, nil
	}
	if args[0] == "agent" {
		return c.agent(ctx, args[1:])
	}
	if args[0] == "mock" {
		return c.mock(ctx, args[1:])
	}
	if args[0] == "rpc" {
		return c.rpc(ctx, args[1:])
	}
	command := args[0]
	switch command {
	case "doctor", "validate", "plan", "up", "ps", "logs", "stop", "down":
	default:
		return 2, fmt.Errorf("unknown command %q", command)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(c.Err)
	file := f.String("f", "", "scenario YAML")
	state := f.String("state", defaultState(), "state directory")
	asJSON := f.Bool("json", false, "JSON output")
	timeout := f.Duration("timeout", 10*time.Minute, "operation timeout")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if *timeout <= 0 {
		return 2, fmt.Errorf("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if command == "doctor" {
		version, err := exec.CommandContext(ctx, "docker", "compose", "version", "--short").Output()
		_, daemonErr := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Output()
		result := map[string]any{"compose_available": err == nil, "compose_version": string(version), "docker_available": daemonErr == nil, "deepseek_key_present": os.Getenv("DEEPSEEK_API_KEY") != "", "model": modelName(), "state": *state}
		if e := c.json(result); e != nil {
			return 1, e
		}
		if err != nil || daemonErr != nil {
			return 1, fmt.Errorf("Docker Engine and Compose must both be available")
		}
		return 0, nil
	}
	if command == "validate" || command == "plan" || command == "up" {
		if *file == "" || f.NArg() != 0 {
			return 2, fmt.Errorf("%s requires -f PATH and no positional arguments", command)
		}
		doc, err := loader.LoadContext(ctx, *file)
		if err != nil {
			return 2, err
		}
		problems := scenario.Validate(doc.Scenario, doc.Services)
		if len(problems) > 0 {
			if *asJSON {
				_ = c.json(map[string]any{"valid": false, "problems": problems})
			} else {
				for _, p := range problems {
					fmt.Fprintln(c.Err, p.Error())
				}
			}
			return 2, nil
		}
		if command == "validate" {
			if *asJSON {
				return 0, c.json(map[string]any{"valid": true, "scenario": doc.Scenario.Name})
			}
			fmt.Fprintln(c.Out, "valid:", doc.Scenario.Name)
			return 0, nil
		}
		spec, err := scenarioapp.Resolve(doc.Scenario, doc.Services, doc.Files)
		if err != nil {
			return 2, err
		}
		if command == "plan" {
			return 0, c.json(spec)
		}
		manager, store, err := c.manager(*state)
		if err != nil {
			return 1, err
		}
		defer store.Close()
		run, err := manager.Start(ctx, doc.Scenario.Name, spec)
		if err != nil {
			return 1, err
		}
		if *asJSON {
			return 0, c.json(run)
		}
		fmt.Fprintf(c.Out, "%s\t%s\t%s\n", run.ID, run.Scenario, run.State)
		return 0, nil
	}
	if command == "ps" && f.NArg() != 0 {
		return 2, fmt.Errorf("ps takes no positional arguments")
	}
	if command != "ps" && f.NArg() != 1 {
		return 2, fmt.Errorf("%s requires one run ID (flags must precede the ID)", command)
	}
	manager, store, err := c.manager(*state)
	if err != nil {
		return 1, err
	}
	defer store.Close()
	if command == "ps" {
		runs, err := store.List(ctx)
		if err != nil {
			return 1, err
		}
		type entry struct {
			Run              any             `json:"run"`
			Containers       json.RawMessage `json:"containers"`
			ObservationError string          `json:"observation_error,omitempty"`
		}
		entries := []entry{}
		for _, run := range runs {
			status := json.RawMessage("[]")
			var observation error
			if run.State != "removed" {
				status, observation = manager.Driver.Status(ctx, run)
			}
			e := entry{Run: run, Containers: status}
			if observation != nil {
				e.ObservationError = observation.Error()
			}
			entries = append(entries, e)
		}
		if *asJSON {
			return 0, c.json(entries)
		}
		w := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "RUN\tSCENARIO\tRECORDED STATE\tCONTAINERS")
		for i, run := range runs {
			status := string(entries[i].Containers)
			if entries[i].ObservationError != "" {
				status = entries[i].ObservationError
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", run.ID, run.Scenario, run.State, status)
		}
		return 0, w.Flush()
	}
	id := f.Arg(0)
	if command == "logs" {
		run, err := store.Get(ctx, id)
		if err != nil {
			return 1, err
		}
		if run.State == "removed" || run.State == "failed" {
			b, err := os.ReadFile(filepath.Join(run.Directory, "workload.log"))
			if err != nil {
				return 1, err
			}
			_, err = c.Out.Write(b)
			return exit(err), err
		}
		err = manager.Driver.Logs(ctx, run, c.Out)
		return exit(err), err
	}
	var result any
	if command == "stop" {
		result, err = manager.Stop(ctx, id)
	} else {
		result, err = manager.Down(ctx, id)
	}
	if err != nil {
		return 1, err
	}
	if *asJSON {
		return 0, c.json(result)
	}
	fmt.Fprintln(c.Out, command+":", id)
	return 0, nil
}
func (c CLI) manager(root string) (execapp.Manager, *sqlite.Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return execapp.Manager{}, nil, err
	}
	store, err := sqlite.Open(absolute)
	if err != nil {
		return execapp.Manager{}, nil, err
	}
	return execapp.Manager{Root: absolute, Store: store, Driver: docker.Driver{Progress: c.Err, WaitSeconds: 90}, Locker: lock.Locker{Root: absolute}}, store, nil
}
func (c CLI) agent(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 || args[0] != "inspect" {
		return 2, fmt.Errorf("usage: localmock agent inspect --repo PATH [--output FILE] [--model NAME]")
	}
	f := flag.NewFlagSet("agent inspect", flag.ContinueOnError)
	f.SetOutput(c.Err)
	root := f.String("repo", ".", "existing local repository")
	model := f.String("model", modelName(), "DeepSeek model")
	rounds := f.Int("max-rounds", 12, "maximum model rounds")
	timeout := f.Duration("timeout", 5*time.Minute, "task timeout")
	output := f.String("output", "", "save validated report as JSON")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 {
		return 2, fmt.Errorf("unexpected positional arguments")
	}
	if *rounds < 1 || *rounds > 100 || *timeout <= 0 {
		return 2, fmt.Errorf("invalid task budget")
	}
	workspace, err := repository.New(*root)
	if err != nil {
		return 2, err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	runner := agent.Runner{Model: deepseek.New(os.Getenv("DEEPSEEK_API_KEY"), *model), OnEvent: func(e agent.Event) {
		if e.Tool != "" {
			fmt.Fprintf(c.Err, "[%s] round %d: %s\n", e.Agent, e.Round, e.Tool)
		} else {
			fmt.Fprintf(c.Err, "[%s] round %d\n", e.Agent, e.Round)
		}
	}}
	result, err := runner.Run(ctx, repository.Definition(*rounds), map[string]string{"task": "Inspect the repository root and identify service candidates with evidence."}, workspace.Tools(), workspace.Validate)
	if err != nil {
		return 1, err
	}
	if *output != "" {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return 1, err
		}
		if err = os.WriteFile(*output, b, 0600); err != nil {
			return 1, err
		}
	}
	return 0, c.json(result)
}
func (c CLI) json(value any) error {
	e := json.NewEncoder(c.Out)
	e.SetIndent("", "  ")
	return e.Encode(value)
}
func defaultState() string {
	if p := os.Getenv("LOCALMOCK_STATE_DIR"); p != "" {
		return p
	}
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "localmock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "localmock")
}
func modelName() string {
	if m := os.Getenv("DEEPSEEK_MODEL"); m != "" {
		return m
	}
	return "deepseek-flash"
}
func exit(err error) int {
	if err != nil {
		return 1
	}
	return 0
}
func (c CLI) help() {
	fmt.Fprintln(c.Out, `localmock — reproducible local service environments

  doctor                         check Docker/Compose and key presence; no model call
  validate -f scenario.yaml       strict DSL and binding validation
  plan -f scenario.yaml           print resolved execution specification as JSON
  up -f scenario.yaml             start an isolated run and wait for readiness
  ps                             show recorded runs and observed container state
  logs RUN                       show current or retained workload logs
  stop RUN                       stop a container group
  down RUN                       remove owned resources; retain diagnostic artifacts
  agent inspect --repo PATH       inspect source with DeepSeek and read-only tools
  mock serve -f mock.yaml         run a configured gRPC mock (also packaged by up)
  mock health --address HOST:PORT check standard gRPC health
  rpc --address ... --proto ...   invoke a unary gRPC method with a trace ID
  version                        print version

Run commands accept --state DIR, --json and --timeout DURATION.
Put flags before positional run IDs. Ctrl-C cancels active startup and cleans its resources.
Use LOCALMOCK_STATE_DIR or XDG_STATE_HOME to select local state storage.`)
}
