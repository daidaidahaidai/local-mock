package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	scenarioapp "github.com/daidaidahaidai/local-mock/internal/scenario/application"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

func (c CLI) components(ctx context.Context, args []string) (int, error) {
	templates := components.Templates()
	if len(args) > 0 && (args[0] == "add" || args[0] == "remove") {
		f := flag.NewFlagSet("components "+args[0], flag.ContinueOnError)
		f.SetOutput(c.Err)
		path := f.String("f", "", "scenario YAML (add creates a missing file)")
		name := f.String("name", "", "component instance name")
		image := f.String("image", "", "explicit image override for the template")
		rest := append([]string(nil), args[1:]...)
		// Both add redis -f PATH and add -f PATH redis are accepted.
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			rest = append(rest[1:], rest[0])
		}
		if err := f.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0, nil
			}
			return 2, err
		}
		if *path == "" || f.NArg() != 1 {
			return 2, fmt.Errorf("components %s requires one name and -f PATH", args[0])
		}
		var err error
		if args[0] == "add" {
			v, ok := templates[f.Arg(0)]
			if !ok {
				return 2, fmt.Errorf("unknown template %q; use components list or supply an image in Scenario.components", f.Arg(0))
			}
			if *name == "" {
				*name = f.Arg(0)
			}
			if *image != "" {
				v.Image = *image
			}
			err = scenarioapp.AddComponent(ctx, loader.ComponentEditor{}, *path, *name, v)
		} else {
			if *name != "" || *image != "" {
				return 2, fmt.Errorf("remove does not accept --name or --image")
			}
			*name = f.Arg(0)
			err = scenarioapp.RemoveComponent(ctx, loader.ComponentEditor{}, *path, *name)
		}
		if err != nil {
			return 2, err
		}
		fmt.Fprintf(c.Out, "%s: %s (%s); run validate/plan/up to use the saved scenario\n", args[0], *name, *path)
		return 0, nil
	}
	if len(args) == 1 && args[0] == "list" {
		names := make([]string, 0, len(templates))
		for name := range templates {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			v := templates[name]
			fmt.Fprintf(c.Out, "%s\t%s\t%v\n", name, v.Image, v.Roles)
		}
		return 0, nil
	}
	if len(args) == 2 && args[0] == "show" {
		v, ok := templates[args[1]]
		if !ok {
			return 2, fmt.Errorf("unknown template %q; use components list or supply an image in Scenario.components", args[1])
		}
		b, err := loader.ComponentFragment(args[1], v)
		if err != nil {
			return 1, err
		}
		_, err = c.Out.Write(b)
		return exit(err), err
	}
	return 2, fmt.Errorf("usage: localmock components list | show NAME | add TEMPLATE -f PATH [--name NAME] | remove NAME -f PATH")
}
