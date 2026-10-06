# Changelog

All notable changes. Versions follow [Semantic Versioning](https://semver.org/); before 1.0
minor versions may change behavior.

## [0.6.1] - 2026-10-06

- `nrp-mcp setup --no-sign-in` stops before the browser sign-in (CI, offline checks).
- Windows: setup now reads kubelogin's version there too (Windows kubelogin rejects
  `--version`; setup falls back to `version`, then `--help`), and a kubelogin that does not
  run at all is reported as a problem instead of ok. Found by the new Windows CI run.
- New CI workflow `setup-e2e`: on Linux, Windows, macOS (Apple Silicon) and macOS (Intel),
  a fresh home folder gets kubectl and kubelogin installed by `nrp-mcp setup` from the real
  release downloads, the sample NRP config placed, the tools run, and a second run changes
  nothing. Runs on every change and weekly.

## [0.6.0] - 2026-10-06

First public release.

- Per-person cleanup: every object carries an owner-id label (a short hash of the cluster
  username), so in a shared namespace each person sees and deletes only their own runs. A
  namespace admin can pass `everyone=true`.
- Every tool resolves the namespace the same way (argument, config, then your first
  namespace); cleanup no longer needs a configured namespace.
- Release binaries for Linux, macOS and Windows (amd64, arm64) with SHA256SUMS, and an
  install script.
- Community files, issue forms, security policy.

## [0.5.0] - 2026-10-06

- New ninth tool `nrp_setup` and command `nrp-mcp setup`: checks kubectl (and its version
  against the cluster), the kubelogin plugin, the NRP config and the sign-in; with your yes,
  installs official kubectl and kubelogin into your user folder (SHA256-checked, no admin
  rights, old copies kept) and puts a downloaded NRP config in place.

## [0.4.1] - 2026-10-06

- First end-to-end web publish. `nrp_watch` now checks a web app's public URL when given a
  run id.

## [0.4.0] - 2026-10-06

- `nrp_data` (list, small uploads, downloads through a labelled helper pod) and
  `nrp_session` (private Jupyter or VS Code through port-forward), live-tested.
- kubectl calls retry once when the sign-in token is being refreshed.
- Cleanup removes a volume's data helper pod with the volume.

## [0.3.0] - 2026-10-06

- First working server: `nrp_status`, `nrp_plan`, `nrp_run`, `nrp_watch`, `nrp_cleanup`,
  `nrp_session`, `nrp_data`, `nrp_build`; NRP rules engine; confirm tokens; run cards;
  audit log; knowledge resources and prompts.

[0.6.1]: https://github.com/UCR-Research-Computing/nrp-mcp/releases/tag/v0.6.1
[0.6.0]: https://github.com/UCR-Research-Computing/nrp-mcp/releases/tag/v0.6.0
