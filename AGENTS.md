# AGENTS.md - k8sd

Canonical Kubernetes daemon. Go backend that manages the Kubernetes control plane
and provides a REST API consumed by the k8s-snap shell layer.

## Repository Layout

```
cmd/             entry points (k8sd, k8s)
pkg/k8sd/        core daemon logic
  api/           REST API handlers
  controllers/   control-loop controllers
  setup/         node setup (kubelet args, containerd config, etc.)
  types/         shared types
hack/            development helpers
```

## Multi-Repo Dependencies

| Repo | Purpose |
|------|---------|
| `github.com/canonical/k8sd` | this repo — Kubernetes daemon |
| `github.com/canonical/k8s-snap` | snap shell, build scripts, integration tests |
| `github.com/canonical/k8s-snap-api` | shared Go API types |

API changes require PRs in all three. During development, add a `replace` directive in
`go.mod` pointing to a local k8s-snap-api checkout; remove it before merging.

## Go Toolchain

- Use the Go version in `go.mod` (`go` directive). The snap build uses `GOTOOLCHAIN=local`
  so `go.mod` must specify a version available as `go/<version>-fips/stable` on the snap store.
- Run `make lint` before pushing — uses golangci-lint.
- Run `make unit-test` before pushing — must pass with zero failures.

## Git Commit Standards

**Every commit MUST be signed off.** Canonical repositories enforce DCO (Developer Certificate of Origin).
Missing sign-offs will fail the `cla-check` CI gate and block merging.

```bash
# Always commit with --signoff
git commit --signoff -m "feat: my change"

# Amend an existing commit to add sign-off
git commit --amend --signoff --no-edit

# Fix a whole branch (rebase onto upstream base)
git rebase --exec "git commit --amend --no-edit --signoff" <base-branch>
```

The sign-off trailer must match the author's name and email exactly:
```
Signed-off-by: Full Name <user@canonical.com>
```

**Before every commit, run:**

```bash
make lint        # golangci-lint — must pass with zero errors
make unit-test   # Go unit tests — must pass with zero failures
```

Do not push a commit that breaks linting or unit tests. Fix the issue first.

## Commit Message Format

Use Conventional Commits: `type(scope): description`

Common types: `feat`, `fix`, `ci`, `docs`, `refactor`, `test`, `chore`

Example:
```
fix(setup): remove deprecated --containerd kubelet flag

The --containerd cAdvisor flag was removed in Kubernetes 1.37.0.
Kubelet fails to start if this flag is present.

Signed-off-by: Louise K. Schmidtgen <louise.schmidtgen@canonical.com>
```
