# Development contract

## Product
- Local Linux CLI for reproducible service debugging. Docker runs on the same machine as the CLI.
- Mac is a source-editing workstation. Run builds, Go tests and containers on the Debian development host, using a Go build container. Do not install runtimes on the Mac.
- Go modular monolith using DDD. Optional TUI later; no GUI or remote-control product service.
- Independent Repository, Topology, Config, Mock, Runtime and Debug agents use a shared Go model adapter, with separate contexts and tool permissions.
- Start with DeepSeek. Read DEEPSEEK_API_KEY only at runtime; never print, commit, embed in artifacts, or pass it to workload containers.

## Domain rules
- Distinguish logical Service, configured Deployment and runtime Instance.
- Directed multigraph call edges describe interfaces/protocols. Bindings select one whole target-service implementation per caller deployment.
- Never mix real and mock methods within one caller's service dependency. Never silently fall through from mock to a real service.
- Dependencies belong to the selected implementation. A mock need not inherit the real implementation's dependencies.
- DSL is the execution intent. Agents produce reviewable artifacts; deterministic application use cases validate and execute them.
- Unknown fields, duplicate YAML keys, unresolved references and overrides of injected dependency addresses are errors.
- Scope resources to a Run; only delete resources owned by that Run. Normal CLI exit must not stop running workloads.
- Runtime workloads use one internal, isolated Docker bridge per Run, with no host ports or DNS forwarding. Probes run inside that network. Never fall back to a network with egress.
- Infrastructure components are explicit user-owned Scenario selections. Prefer real images; custom images use the same lifecycle. Agents cannot add/remove/replace these selections.
- Stop, down and cancellation must have explicit behavior. Preserve diagnostic artifacts before cleanup.

## Architecture
- Organize internal packages by bounded context. Domain packages must not import YAML, SQL, Docker, model-provider or CLI packages.
- Application packages define use cases and ports. Infrastructure implements ports. CLI performs presentation and composition only.
- Model request retries must not automatically replay tool side effects.
- Keep agent-generated patches and application source changes explicit; do not change business behavior merely to make tests pass.
- Avoid speculative frameworks, empty domain packages, service daemons or HA infrastructure.

## Verification
- Test domain invariants, parser failures, lifecycle rollback, cancellation and resource ownership.
- Provider tests use a local HTTP fake; paid live calls should be small and directly relevant to an authorized integration check.
- Verify Docker lifecycle on Debian. Do not run global prune, delete unrelated containers, or change host-wide Docker configuration.
- Keep README and docs/status.md honest about implemented capabilities and limitations.
- Do not commit credentials, host login details, environment dumps, model conversations containing secrets, or generated runtime data.
