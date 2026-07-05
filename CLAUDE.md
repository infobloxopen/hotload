# hotload release-1.x — maintenance branch guidance

This branch is hotload **v1** (module `github.com/infobloxopen/hotload`),
in maintenance: **security and critical bug fixes only**. No new features,
no API changes, no dependency upgrades beyond what a fix requires. New
development happens on `main` (hotload v3, module path with `/v3`); do not
cherry-pick v3 code here — the two lines have different internals
(v3 rewrote the driver core) and different interfaces (`Strategy`).

- Releases from this branch are `v1.7.Z` tags; see RELEASING.md on `main`
  for the branch/tag mapping. Nothing here is ever tagged `v3.*`.
- The branch is protected like main: linear history (rebase/squash only)
  and a required CI check named `build`.
- Never pin a tool fetch to "latest" in the Makefile or CI: `go install`
  uses the go.mod-pinned version; an unpinned `go get ginkgo` once broke
  this branch's CI for months when a new ginkgo required a newer Go than
  the golang:1.23 CI image.
- Connection strings may carry credentials — never log them unredacted.
- Commit messages describe the change and its intent only. No AI/tool
  attribution of any kind.
