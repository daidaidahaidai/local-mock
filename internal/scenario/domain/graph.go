package domain

import catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}
type Node struct {
	Deployment     string `json:"deployment"`
	Service        string `json:"service"`
	Implementation string `json:"implementation"`
	Role           string `json:"role"`
}
type Edge struct {
	Caller     string `json:"caller"`
	Dependency string `json:"dependency"`
	Target     string `json:"target"`
	Interface  string `json:"interface"`
	Protocol   string `json:"protocol"`
}

// DependencyGraph expands each whole-service binding into its parallel interface
// edges. Deployment identity is retained even for multiple instances of a service.
func DependencyGraph(s Scenario, services map[string]catalog.Service) (Graph, error) {
	if problems := Validate(s, services); len(problems) > 0 {
		return Graph{}, problems[0]
	}
	graph := Graph{Nodes: []Node{}, Edges: []Edge{}}
	for _, name := range keys(s.Deployments) {
		d := s.Deployments[name]
		impl := services[d.Service].Implementations[d.Implementation]
		graph.Nodes = append(graph.Nodes, Node{Deployment: name, Service: d.Service, Implementation: d.Implementation, Role: impl.Role})
		for _, alias := range keys(impl.Dependencies) {
			dep := impl.Dependencies[alias]
			target := s.Bindings[name][alias]
			ifaceSet := map[string]bool{}
			for _, iface := range dep.Interfaces {
				ifaceSet[iface] = true
			}
			for _, iface := range keys(ifaceSet) {
				graph.Edges = append(graph.Edges, Edge{Caller: name, Dependency: alias, Target: target, Interface: iface, Protocol: services[dep.Service].Provides[iface].Protocol})
			}
		}
	}
	return graph, nil
}
