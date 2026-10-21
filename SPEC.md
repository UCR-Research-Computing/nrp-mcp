# nrp-mcp SPEC

Version 0.10.0 (2026-10-20; code 0.10.0). Owner: UCR Research Computing.
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
| D3 | Plan, approve, run. Risky steps (public web, GPUs, deleting, uploading, installing) need the person's own approval: an MCP elicitation question, or a single-use 6-character approval code shown only to the person (10 minutes, bound to the plan hash). Other runs take a single-use confirm token. consent: token keeps the 0.8 token-only flow for scripts | Chuck 2026-10-18; review 2026-10-15 |
| D4 | Builds run outside the namespace and nrp-mcp never holds registry credentials. Default (2026-10-07, Chuck: most researchers already keep code on GitHub): a GitHub Actions workflow builds into ghcr.io with the built-in GITHUB_TOKEN. Alternative: NRP GitLab CI with kaniko (2026-10-06). `nrp_build` suggests the Dockerfile and workflow; `nrp_plan image=` runs a built image as is (`prebuilt`: its own CMD, no code copy), with `pull_secret=` for private images | design, revised 2026-10-07; live-tested |
| D5 | Web hosting in v1 as a goal of `nrp_plan`; publishing needs the person's own approval (D3); `public_ack` equal to the exact public URL only with consent token | RC 2026-10-06; revised 2026-10-20 |
| D6 | The tool suggests names (2-3, checked unused, default first) from the app or project; no person or lab names unless the user asks | RC 2026-10-06 |
| D7 | Go, one static binary, `kubectl` (with `kubectl-oidc_login`) as the cluster client | design |
| D8 | NRP rules are code with a test per rule, and are also served as an MCP resource | design |
| D9 | Job results go to a per-person NRP S3 bucket; pods get a signed one-run upload policy, never keys | Chuck 2026-10-18 |
| D10 | Public web apps expire after 14 days by default (`web_days`) | Chuck 2026-10-18 |
| D11 | Workshops share one namespace for now; owner checks keep people to their own runs (a convenience, not a security boundary) | Chuck 2026-10-18 |

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
pods_per_run: 50               # pods running at the same time (a sweep's parallel)
gpus_per_run: 4                # GPUs in use at the same time
namespace_gpus: 8              # max GPUs in running pods across the namespace
tasks_per_run: 10000           # total tasks in one sweep
hours_per_run: 48
gpu_session_hours: 6           # GPU sessions run as Jobs and stop after this many hours (max 12)
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
| `nrp_plan` | no | - | inspect a project + goal; return summary, decisions, `resources`, rules applied, warnings, refusals, `code_files`, `skipped_files`, suggested names (web), `plan_id`, and, when runnable, a `confirm_token` (low-risk) or `needs_person_approval` (web, GPU). Full manifests only with `show_manifests=true`. No plan when the caller's identity cannot be confirmed |
| `nrp_build` | no | no | containerize: suggests a Dockerfile and a GitHub Actions workflow (`target=github`, default; image `ghcr.io/<owner>/<repo>` from the git remote) or a `.gitlab-ci.yml` (`target=gitlab`), plus steps and public/private image guidance; writes nothing |
| `nrp_run` | yes | the person (web, GPU; 5.2) or token (low-risk) | apply an approved plan; label; secrets; TTL; run card |
| `nrp_watch` | no | - | progress, logs (tail/grep), plain-language diagnosis with a suggested fix; per pod: node, minutes waiting (Pending), containers (image and state) and the last 5 events |
| `nrp_data` | yes for uploads | the person + `data_is_p1` for up | list / up (under 100 MB, kubectl cp through a small labelled helper pod) / down, and `down run=<id>` for a job's saved results (5.4); bigger data is pointed to `nrp_plan goal=pull` or NRP S3 |
| `nrp_session` | no | - | connect to a session created by `nrp_plan goal=session` + `nrp_run` (CPU Deployment or GPU Job, which carry the token): readiness, port-forward command, local URL and sign-in token; or point to the hosted JupyterHub |
| `nrp_cleanup` | yes | the person (step 2) | list and delete the caller's own nrp-mcp runs (objects labelled with their owner id); a namespace admin can pass `everyone=true` for all runs in the namespace |

### 5.1 `nrp_plan` goals

| Goal | Workload | Notes |
|---|---|---|
| `job` | `batch/v1 Job` | default; one run to completion |
| `sweep` | Indexed Job | `count` tasks, `parallel` at a time; task index in `JOB_COMPLETION_INDEX`, plus `SLURM_ARRAY_TASK_ID` (the Slurm `--array` id when translated from a script, else the index); >100 tasks forces limits = requests; CPU-only sweeps add required `nodeAffinity` away from GPU nodes (`nvidia.com/gpu.present NotIn [true]`) and offer `opportunistic=true` |
| `web` | Deployment + Service + Ingress | app type detection (Shiny 3838, Streamlit 8501, Dash 8050, Gradio 7860, FastAPI 8000, Flask 5000 incl. Flask with a mounted Dash app, plain Python `HTTPServer`/`serve_forever`/`http.server`/`app.run` with port from the code, static nginx 8080, Node 3000), readiness probe, no GPU, TLS, suggested hosts; a prebuilt image takes its port from the Dockerfile's `EXPOSE`; same name and owner = update in place |
| `session` | Deployment (CPU) or Job + Service (GPU) | used by `nrp_session`; GPU sessions run as a `batch/v1 Job` with `activeDeadlineSeconds` (default 6 h, max 12 h, config `gpu_session_hours`) and a Service for port-forward |
| `llm-batch` | Job | LLM token in a Secret; script retries with backoff; `cache_salt` advice |

Inputs: `project` (path), `goal`, `command` (what to run; inferred if omitted; when a folder has several scripts or notebooks, `next` asks a single guided question instead of failing), `gpu` (0..n
or a type), `cpu`, `memory`, `scratch` (ephemeral-storage, default `10Gi`, 1Gi..50Gi), `region` (`us-west`, `us-central`, `us-east`), `count`/`parallel` (sweep), `image` (override), `name`,
`namespace`, `host` (web), `port` (web), `data_volume`, `pull_secret` (private image),
`avoid_node` (comma list, at most 5: keeps job, sweep, llm-batch, web and session pods off
those nodes with a required node anti-affinity on `kubernetes.io/hostname`; it is in the
manifests, so the plan hash and confirm token cover it),
`llm_secret`, `size`/`urls`/`subdir`/`data_is_p1` (volume, pull), `session`, `opportunistic`, `discard_results`, `show_manifests`.

Image choice order: user `image` (with a Dockerfile or no project: run as built, port from
`EXPOSE`, no code copy or install); project Dockerfile without `image` (needs a build first;
suggests `ghcr.io/<owner>/<repo>:latest`); detected stack
-> pinned public image (PyTorch -> `pytorch/pytorch:2.5.1-cuda12.4-cudnn9-runtime`, TensorFlow ->
`tensorflow/tensorflow:2.18.0-gpu`, JAX -> `nvcr.io/nvidia/jax:24.10-py3`, Jupyter/data science ->
`quay.io/jupyter/scipy-notebook:python-3.13` or `quay.io/jupyter/pytorch-notebook:cuda12-python-3.13`, VS Code -> `codercom/code-server:4.96.4`, R -> `rocker/r-ver:4.4.2`, Shiny -> `rocker/shiny:4.4.2`, Julia -> `julia:1.11-bookworm`, Node -> `node:22-bookworm-slim`, static web -> `nginxinc/nginx-unprivileged:1.27-alpine`, Python ->
`python:3.12-slim-bookworm`); requirements (`requirements.txt`, `package.json`, `install.R`, `DESCRIPTION` `Imports:`, `renv.lock`) installed at start only for small projects, otherwise a
build is recommended. The code ConfigMap is mounted read-only at `/code` and unpacked in the main container (no `busybox` init container). `pull_secret` names a docker-registry Secret for a private image
(added as `imagePullSecrets`); without it, a ghcr.io or NRP GitLab image gets a note that a
private image needs one or must be made public.

Web plans reuse the run id of the person's own nrp-mcp Deployment of the same name, so a new
version updates it in place (a Deployment selector is immutable); a same-named Deployment that
someone else made is refused.

### 5.2 Approval

Risky actions need the person's own approval: `nrp_run` of a web plan or any plan that
asks for a GPU, `nrp_cleanup` step 2, `nrp_data up`, and `nrp_setup fix=true`. Config
`consent: ask|code|token` (default `ask`, env `NRP_CONSENT`) picks how:

- Question (`ask`, when the AI app supports MCP elicitation): nrp-mcp asks the person
  directly with one boolean field, `approve`. Accept + true runs; decline or cancel refuses.
  An answer counts only when it carries the request state nrp-mcp issued with that question
  (random, single use, 10 minutes, bound to plan id + plan hash + action), so a client
  cannot answer a question that was never asked.
- Code (`code`, or `ask` with an app that cannot ask): the first `nrp_run` returns
  `waiting_for_approval` and nothing starts. nrp-mcp shows a 6-character code (for example
  ABC-234) only to the person: desktop notification, stderr, `nrp-mcp approve`, and
  `~/.local/state/nrp-mcp/approve.txt`. The person types it into the chat and the
  assistant calls again with `approval_code`. Codes use an alphabet without look-alikes
  (no 0/O, 1/I/L), are stored as sha256, single use, valid 10 minutes, and bound to the
  plan id + plan hash + action. The assistant never receives a code from nrp-mcp, and tool
  results do not name the file the code is in.
- Token (`token`): the 0.8 flow for scripts and CI; every result carries a warning.

Low-risk runs (CPU job, sweep, pull, volume, CPU session) keep the confirm token: 24
random bytes (crypto/rand), base64url, returned once. The store keeps sha256(token), plan
id, plan hash (sha256 of the canonical manifests + goal + public URL), expiry (10 min)
and a used flag. `nrp_run`/`nrp_build`/`nrp_cleanup` recompute the plan hash and refuse
on mismatch, expiry or reuse.

### 5.4 Results (NRP S3)

- Bucket: `nrp-results-<first 12 hex of the owner id>` (config `s3_bucket`). Layout:
  `runs/<run>/`, and `runs/<run>/task-<n>/` for each sweep task.
- Keys live only on the person's computer in `~/.config/nrp-mcp/s3.env` (0600), written by
  `nrp-mcp s3 setup` (hidden input). No keys: the plan says results are not saved and how
  to set it up.
- Pods never get keys. `nrp_run` signs an S3 POST policy limited to the run's prefix, 5 GiB
  per file, expiring at the job deadline + 1 h, and stores the form fields in a run-time
  Secret `<job>-results`.
- For images that have `curl` or `python` (all default images), an `EXIT` trap on the main
  container's shell uploads `/work/results` when the container exits (no second container or
  image pull). Custom unknown images fall back to a native sidecar (initContainer with
  restartPolicy Always, image quay.io/curl/curl:8.23.0, 100m CPU / 64 MiB taken out of the
  main container's share so the pod total stays as planned) that uploads everything under
  /work/results when it gets SIGTERM, after the main container ends or at the deadline (300 s
  grace). Form values are sent literally and files are streamed, so any file name works; a
  failed upload is logged as "nrp-results: FAILED <key> (HTTP n)" and exits non-zero.
- No keys: job and sweep plans are refused (R23) until the person says "yes, discard
  results" and the assistant plans again with `discard_results=true`.
- `nrp_data action=down run=<id>` downloads to `<project>/results/<run>/` and never
  overwrites.

## 6. Rules (internal/rules)

Each rule has an id, a source, a severity (`refuse` or `warn`), and a fix. The engine is a
pure function `Check(manifests, ctx) -> []Finding`; refusals block the token.

| Id | Rule | Severity | Source |
|---|---|---|---|
| R1 | A Job's command must not be (or end with) `sleep` / `sleep infinity` / `tail -f /dev/null` | refuse | NRP Cluster Policies |
| R2 | Every container sets cpu, memory and ephemeral-storage requests and limits | refuse | Cluster Policies (resource allocation) |
| R3 | Limits within 20% of requests (cpu, memory, ephemeral-storage) | refuse | Cluster Policies |
| R4 | > 100 pods (sweep count, counted on the total, conservatively) requires limit = request | refuse | Cluster Policies |
| R5 | No banned `priorityClassName` (only unset, `armada-default`, `owner-no-preempt`, `opportunistic`, `opportunistic2`) | refuse | Opportunistic Use page |
| R6 | Special GPU (`nvidia.com/a100`, `h100`, `h200`, `gh200`) needs quota > 0 or `opportunistic` (missing H100/H200/GH200 quota entry counts as 0) | refuse | GPU Pods page + live quota |
| R6Q | Special GPU when the namespace quota cannot be read (not opportunistic) | refuse | nrp-mcp 0.9.1 |
| R7 | GPUs per pod: at most 8 for Jobs, 2 for bare pods/interactive | refuse | GPU Pods page |
| R8 | Deployments must not request GPUs (`goal=session` with `gpu>0` runs as a Job with `activeDeadlineSeconds`) | refuse | Long Idle Pods page |
| R9 | No bare Pod for batch work (use a Job) | refuse | Cluster Policies |
| R10 | Storage class `ceph-rbd` not allowed (never provisions) | refuse | tested 2026-10-05 |
| R11 | No secret-looking literal env values (keys, tokens, passwords) in specs; use Secrets | refuse | Scheduling page (specs visible) |
| R12 | Ingress: class `haproxy`, TLS host listed, HTTP only, host under `nrp-nautilus.io` or user domain with certificate | refuse | Exposing HTTP page |
| R13 | Web/publish needs the person's approval (5.2); with consent token, `public_ack` equal to the plan's URL | refuse (at run) | RC 2026-10-06; revised 2026-10-20 |
| R14 | Caps (config): pods and GPUs running at the same time (a Job counts min(parallelism, completions); Kubernetes defaults parallelism to 1), running GPUs across the namespace (`namespace_gpus`, default 8), total tasks per sweep, hours per run | refuse | nrp-mcp config |
| R15 | Jobs get `ttlSecondsAfterFinished` and `backoffLimit` | auto-fix | good citizen |
| R16 | Requests over 1 CPU / 2 GiB are subject to usage-violation checks (GPU < 40%, CPU outside 20-200%, RAM outside 20-150%) | warn | Cluster Policies |
| R17 | Deployments are removed after 2 weeks unless the namespace is on the exceptions list | warn | Long Idle Pods page |
| R18 | Storage not accessed for 6 months can be purged | warn | Storage intro |
| R19 | Data files in a project need data_is_p1 (refuse); volumes, pulls and uploads need it too | refuse | NRP AUP |
| R20 | A public web app may not run a notebook, IDE or web shell: anyone on the internet could run code in your namespace (use goal=session) | refuse | nrp-mcp review 2026-10-15 |
| R21 | A host name must not look like an official NRP address (reserved names, `nrp-*`, `nautilus-*`) | refuse | nrp-mcp review 2026-10-15 |
| R22 | The host must not already answer (someone is using that address); no answer = warning "could not confirm" | refuse/warn (at plan) | nrp-mcp review 2026-10-15 |
| R23 | A job or sweep without results storage (no S3 keys) is refused until the person says "yes, discard results" (`discard_results=true`) | refuse | Chuck 2026-10-20 |

## 7. Labels

Every object nrp-mcp creates carries:
`app.kubernetes.io/managed-by: nrp-mcp`, `nrp-mcp/run: <run_id>`, `nrp-mcp/plan: <plan_id>`,
`nrp-mcp/owner-id: <first 16 hex of sha256(cluster username)>`, and the annotation
`nrp-mcp/owner: <cluster username>`. `nrp_cleanup` selects on managed-by + owner-id, so in
a shared namespace (a workshop) each person sees and deletes only their own runs; a namespace
admin may pass `everyone=true`. Public web apps also carry the annotation
`nrp-mcp/expires: <YYYY-MM-DD>` (now + `web_days`); `nrp_status` shows the days left.
Owner labels prevent mistakes in a shared namespace; they are not a security boundary
(anyone in the namespace can set labels). Use one namespace per person when that matters.

## 8. Diagnosis (nrp_watch)

Pattern table on pod status, container states and events. Failure signals win over the
Pending progress rows; a Pending pod is never described as unscheduled once it has a node:

| Signal | Plain-language explanation | Suggested fix |
|---|---|---|
| Pending, `exceeded quota: a100-limit` | your namespace has no A100 quota | use `nvidia.com/gpu` or `opportunistic`, or request A100 access |
| Pending, `Insufficient nvidia.com/gpu` / `didn't match` | no free node fits right now | smaller GPU, fewer GPUs, or wait |
| Pending, `high-priority-ban` / `low-priority-ban` | banned priorityClassName | remove it |
| `OOMKilled` | ran out of memory at the limit | raise memory 50% and rerun |
| `ImagePullBackOff` / `ErrImagePull` | image missing or private | check the name and tag; a private ghcr.io/GitLab image needs `pull_secret=` or a public package (live-checked 2026-10-07) |
| `CrashLoopBackOff` / exit != 0 | the program failed | last 40 log lines |
| Pending, no node yet | waiting for a node (N min) | nothing under 10 min; then wait or loosen the request |
| Pending, on a node, init container busy | on <node>, preparing (unpacking your code) for N min | none |
| Pending, on a node, image pulling / ContainerCreating | on <node>, downloading <image> for N min; a first pull can take 5-10 min | do not redeploy (it restarts the download) |
| Pending, on a node more than 10 min | on <node> with no progress; the node may be slow or unhealthy | re-plan with `avoid_node=<node>`, then `nrp_run` |
| `ContainerCreating` long, `FailedMount` | volume can't mount on that node (seen with CephFS) | delete the pod so the Job reschedules |
| exit 137 without OOM | killed (deadline or preemption) | check activeDeadlineSeconds / opportunistic |
| Ingress up, HTTPS not 200 | app not ready or wrong port | check readiness probe and Service port |
| Running > 5 min, readiness probe failing | the app does not answer on its port | listen on 0.0.0.0 (not 127.0.0.1) and on port= |
| `Evicted`, ephemeral storage | wrote more to local disk than allowed | write big files to a volume or results/ |
| `Evicted`, node pressure | the node ran short of memory or disk | run again (Jobs retry elsewhere) |
| ImagePull with `toomanyrequests` / 429 | Docker Hub download limit | wait 15-30 min or use ghcr.io / quay.io |
| `CreateContainerError` | image downloaded, container cannot be created | check command= or the image (never shown as downloading) |
| event `admission webhook ... denied` | a Nautilus policy check refused the pod | fix what the message names |
| Job condition Failed, DeadlineExceeded | hit the time limit | raise hours= or checkpoint |
| Job condition Failed, BackoffLimitExceeded | failed and used its retries | read the logs, fix, plan again |
| Job/ReplicaSet FailedCreate (quota, forbidden) | no pod could be created | ask for less, or ask the namespace admin |
| Slow node, a Job | a Job is not moved in place | nrp_cleanup the run, then plan again with avoid_node |

nrp_watch reads events only for the run's own pods and their Job or ReplicaSet
(`--field-selector involvedObject.name=`), plus the Job or Deployment conditions. Any kubectl
failure other than not-found is an error with a next step (sign in, not allowed, no answer).

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
- Project packing (one policy, `inspect.Pack`): no symlinks; skips hidden, vendor, data,
  results and output folders (any case); refuses key and credential names; scans content
  for private keys, kubeconfigs, cloud keys and tokens; copies only source and text types;
  data files (csv, tsv, xlsx, parquet, ...) need `data_is_p1` (R19). Plans list
  `code_files` and `skipped_files` and never echo file contents or Secret values.
- Approval (5.2): risky steps need the person, by question or by a code the assistant
  never receives. Known limit: an AI assistant that can run shell commands on this
  computer could read approve.txt. Approval codes stop an assistant that only has
  nrp-mcp's tools, and stop prompt injection through workload text; they do not stop a
  fully compromised laptop.
- Workload text (logs, events, file listings) is cleaned of escape codes, capped, and
  returned as untrusted data. Pods do not mount the namespace API token.
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
| P6d | 0.8.0 | honest Pending diagnosis (node, pull, age, events) and `avoid_node` | done 2026-10-15; live test in section 12 |
| P6e | 0.9.0-0.9.1 | person approval (question or 6-char code), per-person S3 results, safe packing, web hardening, kubectl error handling and watch conditions | done 2026-10-20; live test in section 12 |
| P6f | 0.10.0 | GPU sessions as Jobs, pinned images with single-container unpack/upload, policy (ephemeral-storage, CPU sweep affinity, region storage, namespace_gpus, H100/H200/GH200 quota), plain apps and R packages, compact plans (< 6 KB) and operator tool descriptions | done 2026-10-20; live test in section 12 |
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

Slow node, 2026-10-15 (0.7.0, found in use): a one-file web page (python:3.12-slim) sat
about 4 minutes on k8s-haosu-20.sdsc.optiputer.net while the image pulled. `nrp_watch` said
"Waiting to be scheduled or for the image to download" the whole time, although the pod had
a node, so the assistant judged it stuck, deleted the pod with kubectl and redeployed, which
restarted the pull on the same node. Nothing was broken. Fixed in 0.8.0: Pending is split
into no node / preparing / downloading / slow node with node, minutes and events, and
`avoid_node` moves a pod off a bad node through the normal plan and token. Also fixed:
`scripts/gauntlet.sh` used `grep -P`, which macOS grep lacks, so the ASCII-docs check
silently never ran on Macs; it now uses perl (checked with a planted non-ASCII line).

Live web, Pending diagnosis and avoid_node, 2026-10-15 (`scripts/e2eweb`, a real stdio
client driving the 0.8.0 binary, RC namespace, a one-file P1 page, public host approved by
the namespace admin): plan + run put the pod on k8s-haosu-20.sdsc.optiputer.net. `nrp_watch`
said, in turn, "On k8s-haosu-20..., downloading for 0 min ... Don't redeploy", then
"preparing (unpacking your code)" for about 2 minutes, then Running with HTTP 200 at 139 s.
Re-planning with `avoid_node=k8s-haosu-20.sdsc.optiputer.net` (decision line shown, NotIn
affinity in the manifests) and `nrp_run` reused the same run id, rolled the Deployment in
place, and the new pod came up on hcc-nrp-shor-c5934.unl.edu with HTTP 200 about 25 s later;
two-step cleanup left 0 workloads. Found and fixed: an init container still Waiting was
described as "preparing (unpacking your code)" while its own image (busybox) downloaded for
about 2 minutes; it is now "downloading <init image>", and "preparing" is only shown while
the init container is Running.

Live 0.9.0, 2026-10-20 (real stdio clients driving the 0.9.0 binary, RC namespace
ucr-research-computing, synthetic P1 data only, public hosts approved by the namespace
admin). Keys: `nrp-mcp s3 setup` run by the person; file 0600. From this computer, nrp-mcp's
own S3 client created bucket nrp-results-ae880359f499 on s3-west; a signed POST inside
`runs/<run>/` was accepted (204), the same form aimed at another run's prefix was refused
(403 AccessDenied), and the download path returned the file intact. Packing (plan only):
a folder with run.py, tokenizer.py, a fake id_rsa and patients.csv was refused with R19;
with data_is_p1=true it was runnable; code_files = patients.csv, run.py, tokenizer.py;
skipped_files = id_rsa ("looks like a key"); no file text in the plan output.
`scripts/e2eresults`: a CPU job wrote results/hello.txt; the native sidecar logged
"nrp-results: saved runs/<run>/hello.txt" through the in-cluster endpoint
(rook-ceph-rgw-nautiluss3.rook); `nrp_data down run=` returned "results ok"; cleanup removed
the Job, ConfigMap and the `<job>-results` Secret. E2E RESULTS OK.
`scripts/e2eapprove` (consent code): the web plan said needs_person_approval with no
confirm token; the first nrp_run returned NOT STARTED / waiting_for_approval and wrote the
code only to the person's places; nrp_run with the code started the app; HTTPS 200 at about
30 s on k8s-haosu-20.sdsc.optiputer.net; cleanup step 1 gave no token, step 2 waited for
its own code, step 3 deleted; 0 workloads. E2E APPROVE OK.
`scripts/e2e` and `scripts/e2eweb` (consent token, the 0.8 flow): CPU job, 3-task sweep,
web plan-only refusal without public_ack, web publish, avoid_node move (haosu-20 ->
haosu-16, same run id, HTTP 200), two-step cleanup, 0 workloads.
Found and fixed in the live test: (1) the S3 keys link was https://nrp.ai/s3 (404); it is
https://nrp.ai/s3token/. (2) suggested host names could start with `nrp-` (from a temp
folder name) and then be refused by R21; SuggestNames now skips names R21 refuses (test
added). (3) the web plan's decision line and the server instructions still told the
assistant to use public_ack under the default consent; they now say the person approves
(public_ack only with consent token). Review fixes after the 0.9.0 panel (same day, unit-
tested, results re-run live): approval answers only for questions nrp-mcp asked; the waiting
note no longer names approve.txt; nrp_session refuses when identity cannot be confirmed;
R23; uploader share carved from the main container; uploader handles any file name and
reports failures. (4) On macOS, copying a new binary over the old one
made it die with "Killed: 9"; install by removing the old file first (install docs
already build to a fresh path).

Live 0.9.1, 2026-10-20 (real stdio clients driving the 0.9.1 binary, RC namespace
ucr-research-computing, CPU only, no public objects). No working kubeconfig (an empty
KUBECONFIG): nrp_status and nrp_cleanup are errors that name the fix (download the NRP
config, nrp_setup); neither says "0 workloads". The cleanup message was raw kubectl text in
the first run and was fixed to the same sign-in hint (test added). Plan-only: a Slurm script
with `--time=1-12:00 --mem=16GB --cpus-per-task=2` and a for-loop became 129600 s (36 h),
16Gi, 2 CPU, lines kept with set -e; memory "16" and gpu_type "a10" were refused with the
new messages. `scripts/e2eresults`: the pod landed on yge-nrp-01.kreonet.net, nrp_watch
reported the sidecar image (quay.io/curl/curl) downloading for about 2 min, then the result
was saved, downloaded and cleaned up. E2E RESULTS OK. `scripts/e2e`, first try: the job
pod sat on k8s-a6000-01.unm.edu downloading quay.io/curl/curl for over 12 minutes. 0.9.1's
nrp_watch said "Slow node ... Remove this run with nrp_cleanup run=<run id>, then plan
again with avoid_node=... (a Job is not moved in place)", where the 0.9.0 server still
running in Hermes said "the old pod is replaced" (wrong for Jobs, fixed in U06). The run was
cleaned up with the two-step token flow (0 workloads). Second try: CPU job, 3-task sweep and
web plan-only refusal all passed. E2E OK. Note for 0.10.0 (images): the results sidecar adds
a second image download per new node; pre-pulling or reusing the main image would remove it.

Live 0.10.0, 2026-10-20 (`scripts/e2e010` and `scripts/e2eresults`, real stdio clients
driving the 0.10.0 binary with `NRP_CONSENT=code`, RC namespace `ucr-research-computing`,
synthetic P1 data only, public host and 1-GPU session approved by the namespace admin):
(1) 20-task CPU sweep with `opportunistic=true` (`python:3.12-slim-bookworm`, 10 parallel,
1 CPU / 1Gi / 10Gi ephemeral-storage, required `nodeAffinity` `nvidia.com/gpu.present NotIn
[true]`, `priorityClassName: opportunistic`): reached 20 Succeeded in 66 s (SWEEP 20 OK) and
was cleaned up.
(2) R script with `install.R` (`rocker/r-ver:4.4.2`): installed `jsonlite` from CRAN via
`Rscript install.R` at start and ran `Rscript analysis.R` to Succeeded in 52 s on
`pas.sacr.net.internet2.edu`, then cleaned up (R INSTALL.R OK).
(3) Plain `http.server` web app with NO `command=` and NO `port=` on host
`nrp010-plain-test`: `nrp_plan` detected `python server.py` on port 8080, returned a
2,893-byte result (< 6 KB, `manifests` omitted), waited for the person's approval code,
answered HTTP 200 at `https://nrp010-plain-test.nrp-nautilus.io/` on
`k8s-haosu-20.sdsc.optiputer.net` in 20 s, and was removed with two-step code cleanup
(PLAIN WEB OK).
(4) 1-GPU session (`nvidia.com/gpu`, `hours=1`, `quay.io/jupyter/pytorch-notebook:cuda12-python-3.13`):
planned as a `batch/v1 Job` (`activeDeadlineSeconds: 3600`) + `Service` (never a Deployment),
started after the person's approval code, came up Ready on `k8s-haosu-12.sdsc.optiputer.net`
with `nrp_session` returning `kubectl --context nautilus -n ucr-research-computing
port-forward svc/e2e010-gpusess 8888:8888`, and was ended immediately with `nrp_cleanup`
(GPU SESSION OK).
(5) `scripts/e2eresults` (`python:3.12-slim-bookworm`, single-container inline Python EXIT
uploader, no `busybox` or `quay.io/curl/curl` sidecar image pull): ran on
`dtn-crispie.njedge.net`, logged `nrp-results: saved runs/1021-0030-mtvfm4/hello.txt`,
downloaded `hello.txt` (`results ok`) via `nrp_data action=down`, and cleaned up
(E2E RESULTS OK). Final `nrp_status`: 0 workloads (0 active), 0 volumes, 0 public URLs.
Found and fixed during the live test: (1) `rocker/r-ver:4.4.2` has neither `curl` nor
`python` installed by default, so R/Julia images now use the `quay.io/curl/curl:8.23.0`
sidecar uploader and `exitUploadBody` skips the upload when `/work/results` has no files
(test added); (2) a pod that had an earlier `FailedScheduling` warning before being assigned
to a node was still diagnosed from `FailedScheduling` while its image downloaded on the node;
`diagnose.Explain` now checks `FailedScheduling` only when `p.Node == ""` (test added).

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
| 0.8.0 | 2026-10-15 | Code 0.8.0: `nrp_watch` Pending diagnosis (node, preparing, downloading, slow node; node, age, containers, events in the result); `nrp_plan avoid_node`; gauntlet ASCII check portable; slow-node incident and live test recorded |
| 0.9.0 | 2026-10-20 | Code 0.9.0: person approval (elicitation or code) for risky steps; safe packing with packed/skipped lists; results to a per-person NRP S3 bucket; web hardening (R20-R22, no API token, 14-day expiry); untrusted text and owner checks; D3 revised, D9-D11 added |
| 0.9.1 | 2026-10-20 | Code 0.9.1: kubectl errors are errors; dry run before approval and run card first; no plan without identity, R6Q; per-command time limits; locked token/approval stores; nrp_watch Job conditions, scoped events and new diagnoses; input validation and Slurm fixes; 0.9.0 review leftovers |
| 0.10.0 | 2026-10-20 | Code 0.10.0: GPU sessions as Jobs (`gpu_session_hours`); pinned images with single-container unpack and exit results upload; policy (`ephemeral-storage` 10Gi default + R2, CPU-only sweep anti-GPU nodeAffinity + `opportunistic`, `region` storage classes, `namespace_gpus` running-GPU cap, missing H100/H200/GH200 quota = 0); plain Python web apps without `command=`, guided entry questions, R package install steps; compact plans (`resources`, `show_manifests=true`, < 6 KB) and < 600-char tool descriptions; live test recorded |
