package cli

import (
	"fmt"
	"sort"

	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

func (c CLI) components(args []string) (int, error) {
	templates := components.Templates()
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
	return 2, fmt.Errorf("usage: localmock components list | components show nacos|mysql|redis")
}
