package domain

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
)

var componentReference = regexp.MustCompile(`^\$\{component\.([a-z][a-z0-9-]*)\.([a-z][a-z0-9-]*)\.(host|port|address)\}$`)

func validateComponents(s Scenario) []Problem {
	var out []Problem
	add := func(name, message string) {
		out = append(out, Problem{"INVALID_COMPONENT", "components." + name, message})
	}
	slots := map[int]string{}
	if len(s.Deployments) > 150 {
		out = append(out, Problem{"TOO_MANY_DEPLOYMENTS", "deployments", "at most 150 deployments"})
	}
	for _, name := range keys(s.Components) {
		c := s.Components[name]
		if !ValidName(name) || name == "localmock-runner" {
			add(name, "invalid or reserved name")
		}
		if _, exists := s.Deployments[name]; exists {
			add(name, "name also used by a deployment")
		}
		if c.Image == "" || strings.ContainsAny(c.Image, " \t\r\n$") {
			add(name, "explicit image required; no environment expansion")
		}
		if len(c.Roles) == 0 {
			add(name, "select at least one role")
		}
		seen := map[string]bool{}
		for _, role := range c.Roles {
			switch role {
			case "registry", "config", "database", "cache", "middleware":
			default:
				add(name, "unknown role "+role)
			}
			if seen[role] {
				add(name, "duplicate role "+role)
			}
			seen[role] = true
		}
		if c.IPOffset < 10 || c.IPOffset > 99 {
			add(name, "ipOffset must be 10..99 (reserved infrastructure range)")
		}
		if previous, ok := slots[c.IPOffset]; ok {
			add(name, "ipOffset already used by "+previous)
		}
		slots[c.IPOffset] = name
		if len(c.Endpoints) == 0 {
			add(name, "declare at least one endpoint")
		}
		for _, endpoint := range keys(c.Endpoints) {
			if !ValidName(endpoint) || c.Endpoints[endpoint] < 1 || c.Endpoints[endpoint] > 65535 {
				add(name, "invalid endpoint "+endpoint)
			}
		}
		if len(c.Readiness) == 0 || c.Readiness[0] == "" {
			add(name, "readiness.command required")
		}
		if c.MemoryMB != 0 && (c.MemoryMB < 64 || c.MemoryMB > 16384) {
			add(name, "memoryMB must be 64..16384")
		}
		for _, key := range keys(c.Env) {
			if !envPattern.MatchString(key) {
				add(name, "invalid environment key "+key)
			}
			if _, err := resolveComponentValue(c.Env[key], s.Components); err != nil {
				add(name, err.Error())
			}
		}
	}
	return out
}

// ResolveComponentEnv uses stable internal names; addresses never depend on host ports.
func ResolveComponentEnv(env map[string]string, components map[string]catalog.Component) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range env {
		value, err := resolveComponentValue(v, components)
		if err != nil {
			return nil, err
		}
		out[k] = value
	}
	return out, nil
}

func resolveComponentValue(value string, components map[string]catalog.Component) (string, error) {
	if !strings.Contains(value, "${component.") {
		return value, nil
	}
	m := componentReference.FindStringSubmatch(value)
	if m == nil {
		return "", fmt.Errorf("expected a whole ${component.NAME.ENDPOINT.host|port|address} reference")
	}
	c, ok := components[m[1]]
	if !ok {
		return "", fmt.Errorf("component %s was not selected by the user", m[1])
	}
	port, ok := c.Endpoints[m[2]]
	if !ok {
		return "", fmt.Errorf("component %s has no endpoint %s", m[1], m[2])
	}
	switch m[3] {
	case "host":
		return m[1], nil
	case "port":
		return strconv.Itoa(port), nil
	default:
		return net.JoinHostPort(m[1], strconv.Itoa(port)), nil
	}
}
