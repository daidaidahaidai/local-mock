package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	agent "github.com/daidaidahaidai/local-mock/internal/orchestration/application"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/infrastructure/deepseek"
	workflow "github.com/daidaidahaidai/local-mock/internal/orchestration/infrastructure/workflow"
)

func (c CLI) agentWorkflow(ctx context.Context, args []string) (int, error) {
	f := flag.NewFlagSet("agent develop", flag.ContinueOnError)
	f.SetOutput(c.Err)
	repo := f.String("repo", ".", "source repository for evidence inspection")
	seed := f.String("f", "", "seed Scenario with executable Service catalog")
	task := f.String("task", "", "environment/debugging task")
	output := f.String("output", "", "new directory for DSL and agent artifacts")
	model := f.String("model", modelName(), "DeepSeek model")
	rounds := f.Int("max-rounds", 10, "maximum model rounds per agent")
	tokens := f.Int("max-tokens", 100000, "workflow token budget (checked before each request)")
	timeout := f.Duration("timeout", 15*time.Minute, "whole workflow timeout")
	execute := f.Bool("execute", false, "start generated environment and run probes once")
	cleanup := f.Bool("cleanup", false, "also clean successful runs after verification")
	state := f.String("state", defaultState(), "runtime state directory")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if f.NArg() != 0 || *seed == "" || *task == "" || *output == "" || *timeout <= 0 || *rounds < 1 || *rounds > 100 || *tokens < 1 || (*cleanup && !*execute) {
		return 2, fmt.Errorf("agent develop requires -f, --task, --output, valid budgets; --cleanup requires --execute")
	}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		return 2, fmt.Errorf("DEEPSEEK_API_KEY is not set in this process")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	project, err := workflow.New(ctx, *repo, *seed, *output)
	if err != nil {
		return 2, err
	}
	runner := agent.Runner{Model: deepseek.New(os.Getenv("DEEPSEEK_API_KEY"), *model), OnEvent: func(e agent.Event) {
		if e.ValidationError != "" {
			fmt.Fprintf(c.Err, "[%s] artifact rejected: %s\n", e.Agent, e.ValidationError)
		} else {
			fmt.Fprintf(c.Err, "[%s] round %d %s\n", e.Agent, e.Round, e.Tool)
		}
	}}
	flow := agent.Workflow{Runner: runner, Project: project}
	if *execute {
		manager, store, e := c.manager(*state)
		if e != nil {
			return 1, e
		}
		defer store.Close()
		flow.Executor = &workflow.Executor{Manager: manager}
	}
	result, err := flow.Run(ctx, agent.WorkflowRequest{Task: *task, Model: *model, MaxRounds: *rounds, MaxTokens: *tokens, Execute: *execute, Cleanup: *cleanup})
	if outputErr := c.json(result); outputErr != nil {
		return 1, errors.Join(err, outputErr)
	}
	return exit(err), err
}
