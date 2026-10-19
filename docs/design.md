# Local Mock design baseline

## Scope
Local Linux CLI for service integration testing. Users install it on the same machine as Docker. Development source is edited on Mac and exercised on Debian. SSH is a development transport, not a product feature.

Go modular monolith, SQLite metadata, file artifacts. No daemon, GUI, cluster scheduler, high availability, or resumable LLM process is required. TUI is optional. Online Boutique is the target reference application; Apollo and Disf are deferred.

## Model
Services define logical identities and provided interfaces. Implementations define executable behavior, configuration and outgoing dependencies. Deployments select implementations and configuration in a Scenario. Instances are actual Docker containers within a Run.

Call graphs are directed multigraphs: a caller may use several interfaces/protocols of the same target service. One binding selects the target deployment for ALL interfaces of a caller's service dependency. Different callers may select different target deployments. Cycles are valid and do not define startup ordering.

Catalog, Scenario, Mocking, Execution, Diagnosis and Agent Orchestration are bounded contexts. Introduce packages as executable capabilities are implemented, not as empty architecture placeholders.

## Execution
Validate YAML structure, domain relationships and supported execution capabilities separately. Resolve binding addresses deterministically. Save a run specification before creating Docker resources. Each run owns an isolated Compose project, with a unique project name and application labels. No unrestricted Compose extensions, host mounts, privileged workloads or Docker socket mounts are accepted from scenario DSL.

`up` waits for readiness and exits while containers continue running. Startup failure/cancellation captures logs and removes partially created resources. `stop` stops containers; `down` removes run resources and ephemeral volumes, retaining diagnostic files and SQLite history. Unexpected workload exit is observable and does not silently erase evidence. Explicit cleanup handles interrupted starts; normal CLI exit is not an interrupted run.

Use short SQL transactions and a per-run OS lock. Docker actions are outside SQL transactions. Local development scope permits cleaning interrupted resources rather than adopting them. Cleanup only targets the exact run project; it never performs global pruning.

## Agents
Repository: inspect code/build/IDL/configuration. Topology: derive dependency graph and propose scenario bindings. Config: resolve environment and configuration. Mock: create replacement behavior. Runtime: prepare executable artifacts and invoke lifecycle use cases. Debug: run tests, inspect evidence and propose explicit changes.

A deterministic coordinator manages tasks. Each agent has isolated history, versioned inputs, permitted tools and bounded rounds. Tool requests and final artifacts require validation. Missing evidence is reported, not invented. An agent's own claim of success is not a validated artifact.

DeepSeek is the first model adapter. The key is read from the process environment only. Provider-specific message fields are retained during tool conversations. JSON output does not replace domain validation.

## Reproducibility
Record selected versions, effective configuration, resolved bindings and actual container image IDs. Retain exact execution files and relevant artifacts. A source revision alone does not promise reproducible dependency downloads. Strong historical replay also requires retained images, input requests and initial data; do not claim it until implemented and verified.
