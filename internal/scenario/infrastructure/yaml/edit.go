package yaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
	"gopkg.in/yaml.v3"
)

type ComponentEditor struct{}

// Update retains the YAML tree, comments and relative paths outside changed entries.
// Validation uses a candidate in the same directory before replacing the original.
func (ComponentEditor) Update(ctx context.Context, path string, change func(map[string]catalog.Component) (map[string]catalog.Component, error)) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Serialize CLI edits without leaving lock files behind on normal exit.
	lockPath := path + ".localmock-lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("cannot lock scenario (another edit, or stale %s): %w", lockPath, err)
	}
	lock.Close()
	defer os.Remove(lockPath)
	info, err := os.Lstat(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if !missing && !info.Mode().IsRegular() {
		return fmt.Errorf("scenario edit requires a regular file")
	}
	mode := os.FileMode(0600)
	var original []byte
	if !missing {
		mode = info.Mode().Perm()
		original, err = readFile(path)
		if err != nil {
			return err
		}
	}
	content := original
	if missing {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if !scenario.ValidName(name) {
			name = "local"
		}
		content, err = yaml.Marshal(map[string]any{"apiVersion": Version, "kind": "Scenario", "metadata": map[string]string{"name": name}})
		if err != nil {
			return err
		}
	}
	// Reject unknown/duplicate fields even if the edited entry would remove them.
	var raw scenarioDTO
	if err = decodeContent(content, &raw); err != nil {
		return err
	}
	if raw.APIVersion != Version || raw.Kind != "Scenario" {
		return fmt.Errorf("expected %s Scenario", Version)
	}
	var tree yaml.Node
	if err = yaml.Unmarshal(content, &tree); err != nil {
		return err
	}
	if hasAlias(&tree) {
		return fmt.Errorf("component editing does not support YAML aliases or merge keys; expand them first")
	}
	root := tree.Content[0]
	current := map[string]catalog.Component{}
	for name, c := range raw.Components {
		current[name] = componentFromDTO(c)
	}
	updated, err := change(current)
	if err != nil {
		return err
	}
	components := mappingValue(root, "components")
	if components == nil {
		components = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "components"}, components)
	}
	if components.Kind != yaml.MappingNode {
		if len(current) != 0 {
			return fmt.Errorf("components must be a mapping")
		}
		*components = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := len(components.Content) - 2; i >= 0; i -= 2 {
		if _, ok := updated[components.Content[i].Value]; !ok {
			components.Content = append(components.Content[:i], components.Content[i+2:]...)
		}
	}
	for _, name := range sortedKeys(updated) {
		if _, exists := current[name]; exists {
			continue
		}
		var value yaml.Node
		if err = value.Encode(componentToDTO(updated[name])); err != nil {
			return err
		}
		components.Content = append(components.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, &value)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err = encoder.Encode(&tree); err != nil {
		return err
	}
	if err = encoder.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".localmock-edit-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	doc, err := LoadContext(ctx, tmp.Name())
	if err != nil {
		return fmt.Errorf("scenario unchanged: %w", err)
	}
	if problems := scenario.Validate(doc.Scenario, doc.Services); len(problems) > 0 {
		return fmt.Errorf("scenario unchanged: %w", problems[0])
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Detect intervening manual edits; never overwrite a different file silently.
	now, readErr := readFile(path)
	if (missing && !errors.Is(readErr, os.ErrNotExist)) || (!missing && (readErr != nil || !bytes.Equal(original, now))) {
		return fmt.Errorf("scenario changed while editing; retry")
	}
	if err = os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func hasAlias(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Tag == "!!merge" {
		return true
	}
	for _, child := range node.Content {
		if hasAlias(child) {
			return true
		}
	}
	return false
}
