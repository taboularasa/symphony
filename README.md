# Symphony

Symphony turns project work into isolated, autonomous implementation runs so
teams can manage work instead of supervising coding agents.

This fork is the Hadto Go implementation track. New implementation code in this
repository must be written in Go.

The upstream Elixir reference implementation is intentionally not vendored here.
Use the upstream repository as historical reference only:

> https://github.com/openai/symphony/tree/main/elixir

## Repository Layout

- `SPEC.md`: language-agnostic Symphony contract.
- `rfcs/`: Hadto extension RFCs and design decisions.
- `.github/`: pull request template and lightweight CI.
- `.codex/`: repo-local agent workflow helpers.

The Go implementation packages will be added at the repository root as Phase 1
work lands.

## Implementation Direction

- Build the runtime in Go only.
- Keep the spec and RFCs as the source of truth for behavior.
- Do not reintroduce local Elixir implementation code or Elixir validation
  gates.

---

## License

This project is licensed under the [Apache License 2.0](LICENSE).
