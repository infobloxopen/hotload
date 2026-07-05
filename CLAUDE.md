# hotload — contributor guidance

## Branches

- `main` is hotload **v3** (module `github.com/infobloxopen/hotload/v3`). All new work lands here.
- `release-1.x` is the **v1 maintenance line** (module `github.com/infobloxopen/hotload`), security and critical fixes only. v1 patches target that branch, never main.
- Both branches are protected: linear history (rebase or squash, no merge commits), required status check literally named `build` — do not rename that CI job without updating branch protection.

## Hard constraints

- **Dependency budget:** the root module's only direct dependency is `github.com/fsnotify/fsnotify`, enforced by `make dep-budget` in CI. Anything needing prometheus goes in the `observability` module; anything needing client-go goes in `k8ssecret`. Do not add dependencies to the core.
- **Tests are stdlib `testing` only** (no ginkgo/gomega on main). Run `make test` (all four modules, `-race`). `make ci-test` adds fmt/tidy/generate/no-diff checks — the conn/stmt combination wrappers and dbfake capability views are generated (`go generate ./...`, source in `internal/gen`); edit the generator, not the `*_gen.go` files.
- **Never pin a tool fetch to "latest"** in Makefiles or CI (`go install` uses the go.mod-pinned version; unpinned `go get` broke v1 CI for months).
- Connection-string values may carry credentials: never log them unredacted (`internal.RedactUrl`), in code or in tests.
- Strategy plugins: `Watchable.Close` must be idempotent and must never call back into hotload (the core invokes it under internal locks).
- `internal/dbfake` must not import the hotload package (internal tests import dbfake); fake strategies live in the test packages instead.

## Releasing

See RELEASING.md — ordering is load-bearing: tag root `v3.Y.Z` first, then bump the satellite `go.mod` requires to that tag, then tag `observability/vA.B.C` and `k8ssecret/vA.B.C`. Satellite `require` lines must point at published tags (the in-repo `replace` masks a missing tag; always run the consumer smoke test in RELEASING.md).

## Integration tests

`test/integration` needs real postgres; opt in with `HOTLOAD_INTEGRATION_TESTS=1` (`make postgres-docker-compose-up` / `make local-integration-tests`).

## Commit messages

Describe the change and its intent only. No AI/tool attribution of any kind.
