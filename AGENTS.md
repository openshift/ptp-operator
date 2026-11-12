# AGENTS.md

Instructions for AI coding agents working in this repository. Also follow [CONTRIBUTING.md](CONTRIBUTING.md); the commit rules below match its "Commit expectations" section.

## Commits

- Make small, self-consistent, atomic commits: one logical change per commit, including the tests and generated or vendored files that change needs.
- Refactoring needed for a feature or fix may go in the same PR, but MUST be in its own commit, separate from the feature or fix.
- Every commit MUST pass CI on its own, including lint and formatting checks, unit tests and the other required checks. Passing only at the PR head is not enough.
- Every commit message MUST explain both what the commit changes and why the change is needed (for example the root cause or motivation), not only what was done.
