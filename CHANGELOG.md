# Changelog

All notable changes. Versions follow [Semantic Versioning](https://semver.org/); before 1.0
minor versions may change behavior.

## [Unreleased]

- Docs: 20 research examples in docs/examples/, linked from the README.

## [0.6.4] - 2026-10-06

### Fixed

- The pod and GPU caps counted every task in a sweep instead of the tasks running at the
  same time, so a 1,000-task sweep run 50 at a time was refused under the default
  `pods_per_run: 50`. `pods_per_run` and `gpus_per_run` now limit what runs at once
  (a Job counts min(parallelism, completions)), and the refusal says to lower `parallel`
  rather than the count. A new cap, `tasks_per_run` (default 10,000), limits a sweep's
  total. The NRP's rule that more than 100 pods need limit = request still counts the
  total, conservatively. Found while writing realistic research examples, where most large
  sweeps hit it.

## [0.6.3] - 2026-10-06

### Fixed

- Slurm array scripts ran every task with the same id. nrp renamed `$SLURM_ARRAY_TASK_ID`
  in the command line but not inside the script, so a script that read the variable (as
  most do) saw it empty in every task: ten tasks, ten identical answers. Each sweep task
  now gets Slurm's array variables (`SLURM_ARRAY_TASK_ID`, `_COUNT`, `_MIN`, `_MAX`) set
  from `$JOB_COMPLETION_INDEX`, mapped to the script's real ids: `--array=1-50` gives
  1..50, `0-20:5` gives 0, 5, .., 20, and lists like `1,3,7` work. The same file now runs
  unchanged on Slurm and on Nautilus. `%N` becomes the sweep's parallelism.

### Added

- `examples/slurm-array-bootstrap`: an R bootstrap with a `--array=0-9` Slurm script.
- `scripts/e2earray`: live check that every array task gets a distinct id and result.

## [0.6.2] - 2026-10-06

### Fixed

- Gemini models rejected the `nrp_plan` tool schema (`urls` was typed `["null", "array"]`;
  Gemini accepts one type per field), so OpenCode on a Gemini model failed before its first
  call. Input schemas now use single types; a test checks every tool. Found by testing
  Hermes Agent, Gemini CLI and OpenCode side by side with a Gemini API key.

## [0.6.1] - 2026-10-06

- `nrp-mcp setup --no-sign-in` stops before the browser sign-in (CI, offline checks).
- Windows: setup now reads kubelogin's version there too (Windows kubelogin rejects
  `--version`; setup falls back to `version`, then `--help`), and a kubelogin that does not
  run at all is reported as a problem instead of ok. Found by the new Windows CI run.
- setup finds the latest kubelogin through the github.com release redirect instead of the
  GitHub API, which allows only 60 calls an hour per IP address (a classroom on one campus
  network could hit it). The API stays as a fallback. Found by the macOS CI run (HTTP 403).
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
