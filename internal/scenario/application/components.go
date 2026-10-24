package application

import (
	"context"
	"fmt"
	"maps"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"github.com/daidaidahaidai/local-mock/internal/scenario/domain"
)

// ComponentEditor validates and atomically persists only the component selection.
type ComponentEditor interface {
	Update(context.Context, string, func(map[string]catalog.Component) (map[string]catalog.Component, error)) error
}

func AddComponent(ctx context.Context, editor ComponentEditor, path, name string, component catalog.Component) error {
	if !domain.ValidName(name) || name == "localmock-runner" {
		return fmt.Errorf("invalid or reserved component name %q", name)
	}
	return editor.Update(ctx, path, func(current map[string]catalog.Component) (map[string]catalog.Component, error) {
		if _, ok := current[name]; ok {
			return nil, fmt.Errorf("component %s already exists", name)
		}
		used := map[int]bool{}
		for _, c := range current {
			used[c.IPOffset] = true
		}
		if component.IPOffset < 10 || component.IPOffset > 99 || used[component.IPOffset] {
			component.IPOffset = 0
			for slot := 10; slot <= 99; slot++ {
				if !used[slot] {
					component.IPOffset = slot
					break
				}
			}
			if component.IPOffset == 0 {
				return nil, fmt.Errorf("no infrastructure IP slots available (10..99)")
			}
		}
		out := maps.Clone(current)
		if out == nil {
			out = map[string]catalog.Component{}
		}
		out[name] = component
		return out, nil
	})
}

func RemoveComponent(ctx context.Context, editor ComponentEditor, path, name string) error {
	return editor.Update(ctx, path, func(current map[string]catalog.Component) (map[string]catalog.Component, error) {
		if _, ok := current[name]; !ok {
			return nil, fmt.Errorf("component %s does not exist", name)
		}
		out := maps.Clone(current)
		delete(out, name)
		return out, nil
	})
}
