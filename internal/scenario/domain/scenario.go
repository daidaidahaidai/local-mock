package domain

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
)

type Scenario struct {
	Name        string
	Deployments map[string]Deployment
	Bindings    map[string]map[string]string
}
type Deployment struct {
	Service        string
	Implementation string
	Env            map[string]string
	Publish        map[string]int // interface -> host port; zero means ephemeral, always loopback
}
type Problem struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) Error() string { return p.Code + " at " + p.Path + ": " + p.Message }

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var referencePattern = regexp.MustCompile(`^\$\{dependency\.([a-z][a-z0-9-]*)\.([a-z][a-z0-9-]*)\.(address|url)\}$`)

func ValidName(s string) bool { return namePattern.MatchString(s) }
func keys[T any](m map[string]T) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}

func Validate(s Scenario, services map[string]catalog.Service) []Problem {
	var problems []Problem
	add := func(code, path, message string) { problems = append(problems, Problem{code, path, message}) }
	if !ValidName(s.Name) {
		add("INVALID_NAME", "metadata.name", "use lowercase letters, digits and hyphens; start with a letter")
	}
	if len(s.Deployments) == 0 {
		add("EMPTY_SCENARIO", "deployments", "at least one deployment is required")
	}
	usedPorts := map[int]string{}
	for _, name := range keys(s.Deployments) {
		d := s.Deployments[name]
		path := "deployments." + name
		if !ValidName(name) {
			add("INVALID_NAME", path, "invalid deployment name")
		}
		svc, ok := services[d.Service]
		if !ok {
			add("SERVICE_NOT_FOUND", path+".service", d.Service)
			continue
		}
		impl, ok := svc.Implementations[d.Implementation]
		if !ok {
			add("IMPLEMENTATION_NOT_FOUND", path+".implementation", d.Implementation)
			continue
		}
		for _, env := range keys(d.Env) {
			if !envPattern.MatchString(env) {
				add("INVALID_ENV", path+".env."+env, "invalid environment variable name")
			}
		}
		for _, endpoint := range keys(d.Publish) {
			port := d.Publish[endpoint]
			if _, ok := impl.Endpoints[endpoint]; !ok {
				add("ENDPOINT_NOT_FOUND", path+".publish."+endpoint, endpoint)
			}
			if port < 0 || port > 65535 {
				add("INVALID_PORT", path+".publish."+endpoint, "port must be 0..65535")
			}
			if port > 0 {
				if previous, ok := usedPorts[port]; ok {
					add("DUPLICATE_PORT", path+".publish."+endpoint, previous)
				}
				usedPorts[port] = name + "." + endpoint
			}
		}
		serviceDependencies := map[string]string{}
		managedEnv := map[string]string{}
		for _, depName := range keys(impl.Dependencies) {
			dep := impl.Dependencies[depName]
			bindingPath := "bindings." + name + "." + depName
			if previous, ok := serviceDependencies[dep.Service]; ok {
				add("DUPLICATE_SERVICE_DEPENDENCY", path, previous+" and "+depName+" must be a single dependency")
			}
			serviceDependencies[dep.Service] = depName
			targetName, ok := s.Bindings[name][depName]
			if !ok {
				add("DEPENDENCY_UNBOUND", bindingPath, "target deployment required")
				continue
			}
			target, ok := s.Deployments[targetName]
			if !ok {
				add("TARGET_NOT_FOUND", bindingPath, targetName)
				continue
			}
			if target.Service != dep.Service {
				add("TARGET_SERVICE_MISMATCH", bindingPath, "expected service "+dep.Service)
				continue
			}
			targetSvc, ok := services[target.Service]
			if !ok {
				continue
			}
			targetImpl, ok := targetSvc.Implementations[target.Implementation]
			if !ok {
				continue
			}
			for _, iface := range dep.Interfaces {
				if _, ok := targetImpl.Endpoints[iface]; !ok {
					add("DEPENDENCY_INTERFACE_MISSING", bindingPath, iface)
				}
			}
			for _, env := range keys(dep.Env) {
				if _, ok := d.Env[env]; ok {
					add("MANAGED_ENV_OVERRIDE", path+".env."+env, "dependency addresses are generated from bindings")
				}
				if _, ok := impl.Env[env]; ok {
					add("MANAGED_ENV_OVERRIDE", path, env+" is also declared in implementation defaults")
				}
				if previous, ok := managedEnv[env]; ok {
					add("DUPLICATE_INJECTION", path, env+" is also managed by "+previous)
				}
				managedEnv[env] = depName
				if _, err := resolveReference(dep.Env[env], depName, dep, targetName, targetSvc, targetImpl); err != nil {
					add("INVALID_REFERENCE", bindingPath+".env."+env, err.Error())
				}
			}
		}
		for _, dep := range keys(s.Bindings[name]) {
			if _, ok := impl.Dependencies[dep]; !ok {
				add("DEPENDENCY_NOT_FOUND", "bindings."+name+"."+dep, dep)
			}
		}
	}
	for _, caller := range keys(s.Bindings) {
		if _, ok := s.Deployments[caller]; !ok {
			add("CALLER_NOT_FOUND", "bindings."+caller, caller)
		}
	}
	return problems
}

func ResolveEnv(name string, s Scenario, services map[string]catalog.Service) (map[string]string, error) {
	d := s.Deployments[name]
	impl := services[d.Service].Implementations[d.Implementation]
	env := map[string]string{}
	for k, v := range impl.Env {
		env[k] = v
	}
	for k, v := range d.Env {
		env[k] = v
	}
	for depName, dep := range impl.Dependencies {
		targetName := s.Bindings[name][depName]
		target := s.Deployments[targetName]
		svc := services[target.Service]
		ti := svc.Implementations[target.Implementation]
		for key, value := range dep.Env {
			resolved, err := resolveReference(value, depName, dep, targetName, svc, ti)
			if err != nil {
				return nil, err
			}
			env[key] = resolved
		}
	}
	return env, nil
}

func resolveReference(value, depName string, dep catalog.Dependency, target string, svc catalog.Service, impl catalog.Implementation) (string, error) {
	m := referencePattern.FindStringSubmatch(value)
	if m == nil || m[1] != depName {
		return "", fmt.Errorf("expected a whole dependency reference for %s", depName)
	}
	found := false
	for _, i := range dep.Interfaces {
		if i == m[2] {
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("interface %s is not declared by dependency", m[2])
	}
	ep, ok := impl.Endpoints[m[2]]
	if !ok {
		return "", fmt.Errorf("missing endpoint %s", m[2])
	}
	address := net.JoinHostPort(target, strconv.Itoa(ep.Port))
	if m[3] == "address" {
		return address, nil
	}
	protocol := svc.Provides[m[2]].Protocol
	if protocol != "http" && protocol != "https" {
		return "", fmt.Errorf("URL reference requires HTTP(S), got %s", protocol)
	}
	return strings.Join([]string{protocol, "://", address}, ""), nil
}
