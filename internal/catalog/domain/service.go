package domain

// Service describes a logical capability. Runtime requirements belong to Implementation.
type Service struct {
	Name            string
	Provides        map[string]Interface
	Implementations map[string]Implementation
}

type Interface struct {
	Protocol     string
	Contract     string
	ContractHash string
}

type Endpoint struct{ Port int }
type Build struct{ Context, Dockerfile string }
type Readiness struct{ Command []string }

type Dependency struct {
	Service    string
	Interfaces []string
	Env        map[string]string
}

type Implementation struct {
	Type         string // image or source; mock is a behavior role, independent of packaging
	Role         string // real or mock
	Image        string
	Build        *Build
	Command      []string
	Env          map[string]string
	Endpoints    map[string]Endpoint
	Dependencies map[string]Dependency
	Readiness    Readiness
}
