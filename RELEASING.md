# Releasing

This repository contains two released modules with independent tag
namespaces, plus an internal test module that is never released:

| Module | Tag format | Example |
|---|---|---|
| `github.com/infobloxopen/hotload/v3` (repo root) | `vX.Y.Z` | `v3.0.0` |
| `github.com/infobloxopen/hotload/k8ssecret` | `k8ssecret/vX.Y.Z` | `k8ssecret/v1.0.0` |
| `github.com/infobloxopen/hotload/observability` | `observability/vX.Y.Z` | `observability/v1.0.0` |
| `github.com/infobloxopen/hotload/test/integration` | never tagged | — |

## Order matters

`observability/go.mod` and `k8ssecret/go.mod` require
`github.com/infobloxopen/hotload/v3` by version. The `replace` directives in
those files only affect building inside this repository — **consumers
resolve the `require` line**, so it must point at a tag that exists.

For a release that touches the root and the satellite modules:

1. Tag the root module first:
   ```sh
   git tag v3.Y.Z && git push origin v3.Y.Z
   ```
2. Bump the require in each satellite module to the new tag:
   ```sh
   (cd observability && go mod edit -require=github.com/infobloxopen/hotload/v3@v3.Y.Z)
   (cd k8ssecret && go mod edit -require=github.com/infobloxopen/hotload/v3@v3.Y.Z)
   ```
   Commit, then tag each satellite:
   ```sh
   git tag observability/vA.B.C && git push origin observability/vA.B.C
   git tag k8ssecret/vA.B.C && git push origin k8ssecret/vA.B.C
   ```

3. Sanity-check as a consumer (from any directory outside this repo):
   ```sh
   cd $(mktemp -d) && go mod init smoke
   GOFLAGS=-mod=mod go get github.com/infobloxopen/hotload/v3@v3.Y.Z \
       github.com/infobloxopen/hotload/observability@vA.B.C \
       github.com/infobloxopen/hotload/k8ssecret@vA.B.C
   ```
   This catches the failure mode of a require pointing at a nonexistent
   version (a `replace` masks it inside the repo).

## Notes

- The root module path carries the `/v3` suffix; tags below `v3.0.0` on the
  root module are invalid for it.
- Do not tag `observability` with a version of the root module's tag series;
  the namespaces are independent and need not be aligned.
- `test/integration` has a permanent `replace` and a nominal require
  version; it is intentionally excluded from releases.
