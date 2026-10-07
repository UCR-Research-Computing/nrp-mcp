# nrp-mcp SPEC

Version 0.7.0 (2026-10-07; code 0.7.0). Owner: UCR Research Computing.
Repo: `UCR-Research-Computing/nrp-mcp`. License: MIT.

## 1. What it is

`nrp-mcp` is a local MCP server for researchers on the National Research Platform's
Nautilus Kubernetes cluster. It runs on the researcher's own computer, reads their own
kubeconfig, acts as them, and turns "here is my code and my data, run it on Nautilus" into
a reviewed plan and then a running workload. It knows the NRP cluster policies and refuses
plans that break them, explaining why and offering the fixed version.

It has nine tools, all named `nrp_*`, each at the level of what the researcher wants rather
than Kubernetes objects.

## 2. Decisions

| # | Decision | Source |
|---|---|---|
| D1 | Name `nrp`; binary `nrp-mcp`; tools `nrp_<verb>` | RC 2026-10-06 |
| D2 | Local only: stdio MCP on the user's machine; their kubeconfig; acts as them through the kubelogin exec plugin. Never reads, prints or sends the token | design |
| D3 | Plan, approve, run. Anything that creates, deletes, publishes or spends GPU time takes a single-use confirm token bound to the exact plan hash (10 minutes) | design (two-step pattern from RC's other MCP servers) |
| D4 | Builds run outside the namespace and nrp-mcp never holds registry credentials. Default (2026-10-07, Chuck: most researchers already keep code on GitHub): a GitHub Actions workflow builds into ghcr.io with the built-in GITHUB_TOKEN. Alternative: NRP GitLab CI with kaniko (2026-10-06). `nrp_build` suggests the Dockerfile and workflow; `nrp_plan image=` runs a built image as is (`prebuilt`: its own CMD, no code copy), with `pull_secret=` for private images | design, revised 2026-10-07; live-tested |
| D5 | Web hosting in v1 as a goal of `nrp_plan`; publishing also needs `public_ack` equal to the exact public URL | RC 2026-10-06 |
| D6 | The tool suggests names (2-3, checked unused, default first) from the app or project; no person or lab names unless the user asks | RC 2026-10-06 |
| D7 | Go, one static binary, `kubectl` (with `kubectl-oidc_login`) as the cluster client | design |
| D8 | NRP rules are code with a test per rule, and are also served as an MCP resource | design |

## 3. Architecture

```
MCP client (Claude Code, Claude Desktop, Gemini CLI, VS Code, Cursor, ...)
   | stdio
nrp-mcp serve
   |-- internal/mcpserver   9 tools, resources, prompts
   |-- internal/plan        build a Plan (pure: project facts + goal -> manifests)
   |-- internal/inspect     read the local project folder (no uploads)
   |-- internal/rules       NRP policy checks on manifests (pure, unit-tested)
   |-- internal/store       plans + confirm tokens + run cards + audit log on disk
   |-- internal/kube        kubectl runner (context, namespace, JSON in/out, apply/delete)
   |-- internal/knowledge   policy digest, GPU table, storage chooser, KB links (resources)
   |-- internal/setup       laptop readiness: kubectl, kubelogin, NRP config, sign-in
   |-- internal/diagnose    plain-language explanations of pod states
   |-- internal/ops         cluster work behind the tools (status, watch, apply, cleanup)
   `-- internal/version
```

**Why kubectl and not client-go:** NRP sign-in is the `oidc-login` exec plugin, which the
user already installed for kubectl (KB024). Shelling to kubectl reuses their exact working
setup, keeps the binary small, and means nrp-mcp never handles a token. Every call is
`kubectl --context <ctx> -n <ns> ... -o json` with a timeout; manifests go in on stdin.

**Files** (all 0700 dirs, 0600 files):

| Path | What |
|---|---|
| `~/.config/nrp-mcp/config.yaml` | kubeconfig path, context, default namespace, caps |
| `~/.local/state/nrp-mcp/plans/<plan_id>.json` | stored plans (manifests, hash, summary) |
| `~/.local/state/nrp-mcp/tokens.json` | outstanding confirm tokens (hash of token, plan id, plan hash, expiry, used) |
| `~/.local/state/nrp-mcp/audit.log` | one JSON line per tool call that changes anything |
| `<project>/.nrp/runs/<run_id>.json` | run card: plan, image, manifests, git commit, times, outcome |
| `<project>/.nrp/data.json` | per-dataset P1 confirmations |

## 4. Configuration

`~/.config/nrp-mcp/config.yaml` (all optional; env overrides `NRP_KUBECONFIG`,
`NRP_CONTEXT`, `NRP_NAMESPACE`):

```yaml
kubeconfig: ~/.kube/config     # the NRP config from https://nrp.ai/config
context: nautilus
namespace: ucr-example         # default; tools take a namespace argument too
kubectl: kubectl               # path if not on PATH
caps:
  pods_per_run: 50       # pods running at the same time (a sweep's parallel)
  gpus_per_run: 4        # GPUs in use at the same time
  tasks_per_run: 10000   # total tasks in one sweep
  hours_per_run: 48
  upload_gb_per_run: 50
```

`nrp-mcp doctor` checks: kubectl present, oidc-login plugin present, kubeconfig readable,
`auth whoami` works, namespace reachable, `auth can-i create jobs`.

## 5. Tools

All tools return a `summary` (plain language) first, then structured fields, and where
useful the `kubectl` equivalent so people learn. Errors are explanations, not stack traces.

| Tool | Changes things? | Confirm? | Purpose |
|---|---|---|---|
| `nrp_setup` | only with `fix=true` | the model asks the user; CLI `nrp-mcp setup` asks [y/N] | laptop readiness: kubectl (version vs the cluster's public /version, verified against the kubeconfig CA), kubelogin, NRP config, sign-in. Fix: official kubectl (dl.k8s.io, matched to the cluster's minor line via stable-X.Y.txt) and kubelogin (GitHub latest release) into the user bin folder, SHA256 verified against the published files, atomic install with a dated backup of any old copy; downloaded NRP config copied to ~/.kube/config (0600), or merged with `kubectl config view --flatten` after a dated backup when a non-NRP config exists. Never needs admin rights; never reads the sign-in token |
| `nrp_status` | no | - | who am I, namespaces, quotas, limit ranges, running workloads and their use, policy warnings, public URLs, upcoming purges/expiries |
| `nrp_plan` | no | - | inspect a project + goal; return summary, manifests, rules applied, warnings, refusals, suggested names (web), `plan_id`, and a `confirm_token` when the plan is runnable |
| `nrp_build` | no | no | containerize: suggests a Dockerfile and a GitHub Actions workflow (`target=github`, default; image `ghcr.io/<owner>/<repo>` from the git remote) or a `.gitlab-ci.yml` (`target=gitlab`), plus steps and public/private image guidance; writes nothing |
| `nrp_run` | yes | token (+ `public_ack` for web) | apply an approved plan; label; secrets; TTL; run card |
| `nrp_watch` | no | - | progress, logs (tail/grep), events, plain-language diagnosis with a suggested fix |
| `nrp_data` | yes for uploads | `data_is_p1` for up | list / up (under 100 MB, kubectl cp through a small labelled helper pod) / down; bigger data is pointed to `nrp_plan goal=pull` or NRP S3. No token in 0.3.0 (TODO: token for uploads) |
| `nrp_session` | no | - | connect to a session created by `nrp_plan goal=session` + `nrp_run` (which carry the token): readiness, port-forward command, local URL and sign-in token; or point to the hosted JupyterHub |
| `nrp_cleanup` | yes | token | list and delete the caller's own nrp-mcp runs (objects labelled with their owner id); a namespace admin can pass `everyone=true` for all runs in the namespace |

### 5.1 `nrp_plan` goals

| Goal | Workload | Notes |
|---|---|---|
| `job` | `batch/v1 Job` | default; one run to completion |
| `sweep` | Indexed Job | `count` tasks, `parallel` at a time; task index in `JOB_COMPLETION_INDEX`, plus `SLURM_ARRAY_TASK_ID` (the Slurm `--array` id when translated from a script, else the index); >100 tasks forces limits = requests |
| `web` | Deployment + Service + Ingress | app type detection (Shiny 3838, Streamlit 8501, Dash 8050, Gradio 7860, FastAPI 8000, Flask 5000 incl. Flask with a mounted Dash app, static nginx 8080, Node 3000), readiness probe, no GPU, TLS, suggested hosts; a prebuilt image takes its port from the Dockerfile's `EXPOSE`; same name and owner = update in place |
| `session` | Deployment (1 replica) | used by `nrp_session` |
| `llm-batch` | Job | LLM token in a Secret; script retries with backoff; `cache_salt` advice |

Inputs: `project` (path), `goal`, `command` (what to run; inferred if omitted), `gpu` (0..n
or a type), `cpu`, `memory`, `count`/`parallel` (sweep), `image` (override), `name`,
`namespace`, `host` (web), `port` (web), `data_volume`, `pull_secret` (private image),
`llm_secret`, `size`/`urls`/`subdir`/`data_is_p1` (volume, pull), `session`, `opportunistic`.

Image choice order: user `image` (with a Dockerfile or no project: run as built, port from
`EXPOSE`, no code copy or install); project Dockerfile without `image` (needs a build first;
suggests `ghcr.io/<owner>/<repo>:latest`); detected stack
-> known public image (PyTorch -> `pytorch/pytorch`, TensorFlow ->
`tensorflow/tensorflow:latest-gpu`, Jupyter/data science ->
`quay.io/jupyter/scipy-notebook`, R -> `rocker/r-ver`, Shiny -> `rocker/shiny`, Python ->
`python:3.12-slim`); requirements installed at start only for small projects, otherwise a
build is recommended. `pull_secret` names a docker-registry Secret for a private image
(added as `imagePullSecrets`); without it, a ghcr.io or NRP GitLab image gets a note that a
private image needs one or must be made public.

Web plans reuse the run id of the person's own nrp-mcp Deployment of the same name, so a new
version updates it in place (a Deployment selector is immutable); a same-named Deployment that
someone else made is refused.

### 5.2 Confirm tokens

Token = 32 random bytes, base64url, returned once. Store keeps sha256(token), plan id, plan
hash (sha256 of the canonical manifests + goal + public URL), expiry (10 min), used flag.
`nrp_run`/`nrp_build`/`nrp_cleanup` recompute the plan hash and refuse on mismatch, expiry
or reuse.

## 6. Rules (internal/rules)

Each rule has an id, a source, a severity (`refuse` or `warn`), and a fix. The engine is a
pure function `Check(manifests, ctx) -> []Finding`; refusals block the token.

| Id | Rule | Severity | Source |
|---|---|---|---|
| R1 | A Job's command must not be (or end with) `sleep` / `sleep infinity` / `tail -f /dev/null` | refuse | NRP Cluster Policies |
| R2 | Every container sets cpu and memory requests and limits | refuse | Cluster Policies (resource allocation) |
| R3 | Limits within 20% of requests (cpu, memory, ephemeral-storage) | refuse | Cluster Policies |
| R4 | > 100 pods (sweep count, counted on the total, conservatively) requires limit = request | refuse | Cluster Policies |
| R5 | No banned `priorityClassName` (only unset, `armada-default`, `owner-no-preempt`, `opportunistic`, `opportunistic2`) | refuse | Opportunistic Use page |
| R6 | Special GPU (`nvidia.com/a100`, `h100`, `h200`, `gh200`) needs quota > 0 or `opportunistic` | refuse | GPU Pods page + live quota |
| R7 | GPUs per pod: at most 8 for Jobs, 2 for bare pods/interactive | refuse | GPU Pods page |
| R8 | Deployments (web/session idle) must not request GPUs, except `session` with an explicit GPU and a confirm | refuse/warn | Long Idle Pods page |
| R9 | No bare Pod for batch work (use a Job) | refuse | Cluster Policies |
| R10 | Storage class `ceph-rbd` not allowed (never provisions) | refuse | tested 2026-10-05 |
| R11 | No secret-looking literal env values (keys, tokens, passwords) in specs; use Secrets | refuse | Scheduling page (specs visible) |
| R12 | Ingress: class `haproxy`, TLS host listed, HTTP only, host under `nrp-nautilus.io` or user domain with certificate | refuse | Exposing HTTP page |
| R13 | Web/publish requires `public_ack` equal to the plan's URL | refuse (at run) | RC 2026-10-06 |
| R14 | Caps (config): pods and GPUs running at the same time (a Job counts min(parallelism, completions); Kubernetes defaults parallelism to 1), total tasks per sweep, hours per run | refuse | nrp-mcp config |
| R15 | Jobs get `ttlSecondsAfterFinished` and `backoffLimit` | auto-fix | good citizen |
| R16 | Requests over 1 CPU / 2 GiB are subject to usage-violation checks (GPU < 40%, CPU outside 20-200%, RAM outside 20-150%) | warn | Cluster Policies |
| R17 | Deployments are removed after 2 weeks unless the namespace is on the exceptions list | warn | Long Idle Pods page |
| R18 | Storage not accessed for 6 months can be purged | warn | Storage intro |
| R19 | Data must be P1 (non-sensitive) | warn + confirmation in `nrp_data` | NRP AUP |

## 7. Labels

Every object nrp-mcp creates carries:
`app.kubernetes.io/managed-by: nrp-mcp`, `nrp-mcp/run: <run_id>`, `nrp-mcp/plan: <plan_id>`,
`nrp-mcp/owner-id: <first 16 hex of sha256(cluster username)>`, and the annotation
`nrp-mcp/owner: <cluster username>`. `nrp_cleanup` selects on managed-by + owner-id, so in
a shared namespace (a workshop) each person sees and deletes only their own runs; a namespace
admin may pass `everyone=true`.

## 8. Diagnosis (nrp_watch)

Pattern table on pod status, container states and events:

| Signal | Plain-language explanation | Suggested fix |
|---|---|---|
| Pending, `exceeded quota: a100-limit` | your namespace has no A100 quota | use `nvidia.com/gpu` or `opportunistic`, or request A100 access |
| Pending, `Insufficient nvidia.com/gpu` / `didn't match` | no free node fits right now | smaller GPU, fewer GPUs, or wait |
| Pending, `high-priority-ban` / `low-priority-ban` | banned priorityClassName | remove it |
| `OOMKilled` | ran out of memory at the limit | raise memory 50% and rerun |
| `ImagePullBackOff` / `ErrImagePull` | image missing or private | check the name and tag; a private ghcr.io/GitLab image needs `pull_secret=` or a public package (live-checked 2026-10-07) |
| `CrashLoopBackOff` / exit != 0 | the program failed | last 40 log lines |
| `ContainerCreating` long, `FailedMount` | volume can't mount on that node (seen with CephFS) | delete the pod so the Job reschedules |
| exit 137 without OOM | killed (deadline or preemption) | check activeDeadlineSeconds / opportunistic |
| Ingress up, HTTPS not 200 | app not ready or wrong port | check readiness probe and Service port |

## 9. Resources and prompts

Resources: `nrp://policy` (rule digest with sources and date), `nrp://gpus` (resource names
and quota rules), `nrp://storage` (chooser), `nrp://kb` (UCR guide links KB023-KB030),
`nrp://llm` (endpoint, models, fair use).
Prompts: `first_run`, `port_slurm_script`, `parameter_sweep`, `my_job_failed`,
`host_web_tool`, `teach_workshop`.

## 10. Security

- Only the user's identity; only namespaces they belong to; no cluster-scoped writes.
- Token never read: kubectl handles auth. Secret values (S3 keys, LLM token) are read from
  files the user names and written straight into Kubernetes Secrets; never logged or
  returned.
- Project inspection is local reads, size-capped, skipping `.git`, `node_modules`, data
  files over 1 MB, and anything matching `.env`/`*key*`/`*secret*`.
- Audit log of every change.

## 11. Phases

| Phase | Version | Scope | Status |
|---|---|---|---|
| P0/P1 | 0.1.0 | doctor, `nrp_status`, `nrp_watch`, rules engine, knowledge resources | done; live-tested 2026-10-06 |
| P2 | 0.2.0 | `nrp_plan` (job, sweep, web, session, llm-batch, volume, pull; Slurm translation) | done; job and web live-tested (web plan only) |
| P3 | 0.3.0 | `nrp_run`, tokens, run cards, `nrp_cleanup` | done; live-tested 2026-10-06 |
| P4 | 0.4.0 | `nrp_data`, `nrp_session` | done; live-tested 2026-10-06 (`scripts/e2e2`) |
| P4b | 0.4.1 | web publish end to end with `public_ack` | done 2026-10-06: leaf-area-explorer published through the MCP tools, live in about 1 minute, HTTP 200, TLS valid (NRP wildcard cert, Let's Encrypt), then removed |
| P5 | 0.5.0 | `nrp_build` | 0.3.0 suggests Dockerfile + NRP GitLab kaniko CI; does not build (decided: builds never run in the namespace, so no registry credentials touch nrp-mcp) |
| P5a | 0.5.0 | `nrp_setup` and `nrp-mcp setup` (laptop readiness) | done; live-tested on fresh laptops |
| P6 | 0.6.0 | public repo, release binaries for Linux/macOS/Windows, install script, per-person cleanup | done 2026-10-06 |
| P6b | 0.6.1 | `setup-e2e` CI: `nrp-mcp setup` on Linux, Windows, macOS arm64 and macOS Intel runners (real downloads, sample config, second run idempotent) | done |
| P6c | 0.6.2-0.6.4 | client compatibility and sweep fixes: single-typed tool schemas (Gemini), Slurm array ids inside scripts, caps count what runs at once | done 2026-10-06; Hermes Agent, Gemini CLI and OpenCode tested live |
| P5b | 0.7.0 | GitHub image path: GitHub Actions -> ghcr.io (default, built-in token), prebuilt image runs, `pull_secret`, in-place web updates; NRP GitLab as `target=gitlab` | done; live-tested 2026-10-07 (section 12). Not yet live-tested: the `pull_secret` path with a real token, and `target=gitlab` |
| P7 | 1.0.0 | pilot with RC and a first lab workshop; browser sign-in on macOS and Windows checked by hand | next |

## 12. Testing

`scripts/gauntlet.sh` (gofmt, vet, test, build, ASCII-only docs); CI runs the same. Unit tests: every rule (a violating manifest is
refused with the right id; the fixed one passes), plan generation for each goal, token
lifecycle (single use, expiry, hash mismatch), Slurm translation, diagnosis table. Live
tests run only in an RC namespace, labelled and cleaned up after; no GPU or public objects
without the namespace admin's go.

In-process MCP tests (`internal/mcpserver`) drive the real protocol over an in-memory
transport with a fake kubectl: nine tools, five resources, six prompts; plan creates
nothing; wrong, reused tokens refused; web refused without or with a wrong `public_ack`
(token survives a failed ack); A100 with quota 0 not runnable; cleanup two-step and only
by the managed-by label; volume refused without `data_is_p1`; build writes nothing; build
defaults to GitHub (workflow uses GITHUB_TOKEN and ghcr.io; image from the git remote;
compiled code gets build-essential and `make`; Flask gets gunicorn and EXPOSE) and
`target=gitlab` gives kaniko. Plan tests: a prebuilt image runs its own CMD with no code
volume, port from EXPOSE and the pull secret; a Dockerfile without an image needs a build on
ghcr.io; a prebuilt Job keeps /data.

Live end-to-end (`scripts/e2e`, a real stdio client against Nautilus), 2026-10-06 in an
RC namespace: status, plan (1 CPU, 1 GiB, python:3.12-slim), run, watch
(Succeeded, logs read back), cleanup step 1 and 2, then nothing left; a 3-task CPU sweep
(Indexed Job) ran to 3 Succeeded and was cleaned up; web plan suggested
three names and `nrp_run` refused without `public_ack`. Namespace empty afterwards. The
first live run found a gap (a folder with one script had no entry point); fixed with the
"only script in the folder" rule and a test.

Live data and session test (`scripts/e2e2`), 2026-10-06: volume planned, run, Bound; upload
refused without `data_is_p1`, then uploaded; list; download byte-identical; CPU-only Jupyter
session ready, reached through port-forward (HTTP 200), API without the token refused
(HTTP 403); two-step cleanup removed the session, the volume and the data helper pod.
Found and fixed: (1) a kubectl call failed once with "the server has asked for the client
to provide credentials" while the OIDC token refreshed, so kube.Runner now retries once on
that error (never on Forbidden); (2) the data helper pod was not part of the volume's run,
so cleanup left it and the volume behind; it now takes the volume's run label and cleanup
deletes bare pods first.

Laptop setup, 2026-10-06 (`nrp-mcp setup --yes` with an empty home folder and a PATH
without kubectl, real downloads): installed kubelogin v1.36.4 and kubectl v1.34.12 (matched to
the cluster's v1.34.11, SHA256 verified), copied the NRP config from Downloads to
~/.kube/config (0600, identical to the source), signed in, and `nrp-mcp doctor` then saw the
namespace. Answering "n" changed nothing. Over MCP, `nrp_setup sign_in=true` reported
an RC laptop ready with a note (kubectl 1.37 is three minors newer than the cluster).
Found and fixed during testing: (1) the first fresh run installed the latest kubectl, then
replaced it once the config showed the cluster version; the cluster version is now read from
the kubeconfig directly, so the first download matches; (2) a newer-than-supported kubectl
was reported as blocking; it is now a note (older-than-supported still blocks); (3) the
sign-in failed with "executable kubectl not found" because the NRP config runs `kubectl
oidc-login` from PATH; setup now puts its install folder on the PATH of everything nrp-mcp
starts (the user's shell gets a printed hint instead; nrp-mcp never edits shell profiles).

First web publish, 2026-10-06 (name approved by the namespace admin): `examples/leaf-area-explorer`
(Streamlit, synthetic data) published through the nrp MCP tools from an AI assistant: `nrp_plan goal=web
host=leaf-area-explorer`, then `nrp_run` with `public_ack`. https://leaf-area-explorer.nrp-nautilus.io/
answered HTTP 200 about a minute later, served by a pod on nrp0.njedge.net, with the NRP
wildcard certificate (Let's Encrypt, valid to 2026-11-25). The page rendered (title,
slider, chart, table). Found and fixed: `nrp_watch` did not check the public URL when
given a run id (it looked up the Ingress by name); it now finds Ingresses by the same
label selector and adds the result to the summary. The site was then removed with
`nrp_cleanup`.

Per-person cleanup, 2026-10-06: objects carry an owner-id label; cleanup lists and deletes
only the caller's runs (live: "Nothing from run ... (yours) is left"); `everyone=true` is
refused unless the caller is admin of that namespace (test). Found while adding it: cleanup
did not fall back to the user's namespace when none was configured (status did); every tool
now resolves the namespace the same way.

Cross-platform setup (`.github/workflows/setup-e2e.yml`, 0.6.1): on Linux, Windows, macOS
(Apple Silicon) and macOS (Intel) GitHub runners, an empty home folder with
`testdata/nrp-sample-kubeconfig.yaml` in Downloads (public server address and CA,
placeholder OIDC values) runs `nrp-mcp setup --yes --no-sign-in` with a PATH that hides
the runner's own kubectl. It must install kubectl and kubelogin (SHA256 verified), copy
the config byte for byte, run both tools, let kubectl find the plugin by name, and change
nothing on a second run. Runs on every push, pull request and weekly.

Clients, 2026-10-06 (0.6.2): Hermes Agent, Gemini CLI and OpenCode, each with a Gemini API
key, answered "where do I stand on Nautilus?" from the live cluster. OpenCode on Gemini first
failed because one tool schema used a two-type field; fixed with single types and a test.

GitHub image path, 2026-10-07 (0.7.0, RC namespace, synthetic data): a Flask app with a
mounted Dash dashboard, a C helper built with `make`, a CSV over 900 KB and a Dockerfile, in a
throwaway private GitHub repo. `nrp_build` gave the workflow and image name from the remote;
the push built green on GitHub Actions (built-in token, no secrets added). With the package
private, the Deployment stopped at ErrImagePull and `nrp_watch` named the cause and the fix
(pull secret or public package). After the package was made public, `nrp_plan image=...`
then `nrp_run` with `public_ack` gave HTTP 200 on the Flask page, the compiled helper and the
Dash chart. A new image was then deployed in place at the same address. Found and fixed: (1) a
Flask app with Dash mounted was detected as Dash (wrong port and command); (2) re-planning a
deployed name collided with the running Deployment (`spec.selector: field is immutable`);
plans now reuse the person's run. Also seen: two crash loops from the test app's own missing
packages (numpy, pandas), each shown with the exact error by `nrp_watch`. Everything was
removed with `nrp_cleanup` and the repo deleted.

## 13. Change log

| Version | Date | Change |
|---|---|---|
| 0.1 | 2026-10-06 | First spec, from the design plan |
| 0.2 | 2026-10-06 | Code 0.3.0 built: all eight tools; P0-P3 live-tested; phase table, testing, build decision updated |
| 0.3 | 2026-10-06 | Code 0.4.0: P4 live-tested; auth-refresh retry; data helper joins the volume's run |
| 0.4 | 2026-10-06 | Code 0.4.1: first web publish (P4b); watch checks public URLs by run |
| 0.5 | 2026-10-06 | Code 0.5.0: ninth tool `nrp_setup` and `nrp-mcp setup` (laptop readiness and install) |
| 0.6 | 2026-10-06 | Code 0.6.0: public release; per-person cleanup (owner-id label); one namespace resolution for all tools; release binaries and install script |
| 0.6.1 | 2026-10-06 | Code 0.6.1: `setup --no-sign-in`; setup-e2e CI on four OS runners |
| 0.6.4 | 2026-10-06 | Code 0.6.2-0.6.4: single-typed schemas (Gemini), Slurm array ids, caps count concurrent pods, `tasks_per_run`; client tests |
| 0.7.0 | 2026-10-07 | Code 0.7.0: GitHub image builds by default (D4 revised), prebuilt images with `pull_secret`, in-place web updates, Flask+Dash detection; live test recorded; phase table cleaned (duplicate P5b) |
