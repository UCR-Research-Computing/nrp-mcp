# nrp-mcp

[![CI](https://github.com/UCR-Research-Computing/nrp-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/UCR-Research-Computing/nrp-mcp/actions/workflows/ci.yml)
[![Setup on Linux, macOS, Windows](https://github.com/UCR-Research-Computing/nrp-mcp/actions/workflows/setup-e2e.yml/badge.svg?branch=main)](https://github.com/UCR-Research-Computing/nrp-mcp/actions/workflows/setup-e2e.yml)
[![Release](https://img.shields.io/github/v/release/UCR-Research-Computing/nrp-mcp)](https://github.com/UCR-Research-Computing/nrp-mcp/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/UCR-Research-Computing/nrp-mcp)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Run your research on the National Research Platform's Nautilus cluster by talking to
your AI assistant.**

`nrp-mcp` is a small [MCP](https://modelcontextprotocol.io) server that runs on your own
computer. Point it at your Nautilus sign-in and it turns *"here is my code, run it on a
GPU"* into a reviewed plan and a running job. It knows the NRP's rules, so you don't
have to learn Kubernetes first.

```
You:        Run train.py in ~/proj/leafnet on a GPU.
Assistant:  Plan: one Job using pytorch/pytorch, 1 standard GPU, 4 cores, 16 GiB, 24-hour
            limit. It installs requirements.txt and runs `python train.py`. A100s need a
            quota you don't have, so this asks for any free standard GPU. Run it?
You:        Yes.
Assistant:  Running on an RTX A4000... Finished in 41 minutes. Copy results/ back?
```

Built by [UCR Research Computing](https://ucr-research-computing.github.io/). Works with
Hermes Agent, Gemini CLI, OpenCode, Claude Code, Claude Desktop, VS Code, Cursor and any other MCP client.

## Contents

- [What you can do](#what-you-can-do)
- [Quick start](#quick-start)
- [The tools](#the-tools)
- [Safety](#safety)
- [Configuration](#configuration)
- [How it works](#how-it-works)
- [Limits](#limits)
- [Development](#development)
- [Help](#help)

## What you can do

| You say | nrp-mcp does |
|---|---|
| "Is my laptop ready for Nautilus?" | Checks kubectl, the sign-in plugin and your NRP config, and installs what's missing (with your yes) |
| "Where do I stand?" | Your namespaces, GPU quotas, what's running, public URLs, and policy warnings |
| "Run this script on a GPU" | Reads your project, picks an image and size, explains the plan, runs it after you agree |
| "Run it 200 times with different seeds" | A parameter sweep (Indexed Job); your script reads its task number |
| "Move my Slurm job here" | Translates `#SBATCH` lines and shows what has no equivalent |
| "Why did it fail?" / "Why is it stuck?" | Plain-language diagnosis with a fix: out of memory, no free GPU, quota, image pull, crashes |
| "Give me a GPU notebook" | A private Jupyter or VS Code session through port-forward |
| "Download this dataset to the cluster" | A shared volume and an in-cluster download, so big files skip your laptop |
| "Put my lab's tool on the web" | Deployment, Service and HTTPS address; suggests names; publishes only after you type the URL back |
| "Containerize this" | A Dockerfile and a GitLab CI file that builds it on NRP GitLab |
| "Clean up" | Lists what you made and deletes it after a second yes |

## Quick start

You need a Nautilus account and a namespace. Sign in at https://nrp.ai with your
institution; students are added to a namespace by their PI, faculty can request one.
UCR researchers: see [Getting access](https://ucr-research-computing.github.io/kb/kb024-nautilus-getting-access/).

**1. Install nrp-mcp**

Linux or macOS:

```
curl -fsSL https://raw.githubusercontent.com/UCR-Research-Computing/nrp-mcp/main/scripts/install.sh | sh
```

Windows: download `nrp-mcp_<version>_windows_amd64.zip` from
[Releases](https://github.com/UCR-Research-Computing/nrp-mcp/releases), unzip it and keep
`nrp-mcp.exe` somewhere handy. With Go installed, `go install
github.com/UCR-Research-Computing/nrp-mcp/cmd/nrp-mcp@latest` works everywhere.

**2. Download your NRP config** at https://nrp.ai/config (it lands in Downloads).

**3. Get your computer ready**

```
nrp-mcp setup
```

`setup` checks everything first and asks before changing anything. Then it installs kubectl
and the kubelogin sign-in plugin (official releases, SHA256-checked, into your user folder,
no admin rights), puts the NRP config in `~/.kube/config` (backing up an existing one), and
signs you in through your browser. It ends with *"This computer is ready for Nautilus."*

**4. Add it to your AI client**

Tested end to end (live cluster, Gemini API key) with each of these:

| Client | Add nrp |
|---|---|
| [Hermes Agent](https://hermes-agent.nousresearch.com/) | `hermes mcp add nrp --command nrp-mcp --args serve` |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | `gemini mcp add nrp nrp-mcp serve` |
| [OpenCode](https://opencode.ai/) | add `"mcp": {"nrp": {"type": "local", "command": ["nrp-mcp", "serve"]}}` to `opencode.json` |
| Claude Code | `claude mcp add nrp -- nrp-mcp serve` |

Claude Desktop, Cursor, VS Code and others (`mcpServers` block):

```json
{"mcpServers": {"nrp": {"command": "nrp-mcp", "args": ["serve"]}}}
```

Use the full path to `nrp-mcp` if your client doesn't see your shell's PATH, and add
`"--namespace", "<your-namespace>"` to `args` to pick a default.

**5. Ask:** *"Where do I stand on Nautilus?"*

## The tools

Nine tools, each named for what you want rather than for Kubernetes objects. Every result
starts with a plain-language summary and, where useful, the `kubectl` equivalent so you
learn as you go.

| Tool | Changes things? | What it does |
|---|---|---|
| `nrp_setup` | only with `fix=true` | Laptop readiness: kubectl (version vs the cluster), kubelogin, NRP config, sign-in; installs or updates them with your yes |
| `nrp_status` | no | Identity, namespaces, GPU quotas, workloads and what they ask for, volumes, public URLs, warnings |
| `nrp_plan` | no | Inspects a project and a goal (`job`, `sweep`, `web`, `session`, `llm-batch`, `volume`, `pull`), applies every rule, returns a summary, decisions, refusals, manifests and a confirm token |
| `nrp_run` | yes, with a token | Runs an approved plan; web plans also need `public_ack`; writes a run card to `<project>/.nrp/runs/` |
| `nrp_watch` | no | Progress, logs (tail/grep), diagnosis with a fix, public URL check |
| `nrp_cleanup` | yes, two steps | Lists your runs, then deletes one after a second confirm |
| `nrp_session` | no | Readiness, port-forward command and local link for a notebook or VS Code session |
| `nrp_data` | uploads only | List, upload small non-sensitive files, download results from a volume |
| `nrp_build` | no | Suggests a Dockerfile and an NRP GitLab CI (kaniko) file |

Also served: resources `nrp://policy`, `nrp://gpus`, `nrp://storage`, `nrp://llm`,
`nrp://kb`, and prompts `first_run`, `port_slurm_script`, `parameter_sweep`,
`my_job_failed`, `host_web_tool`, `teach_workshop`.

## Safety

- **Runs as you, on your computer.** It uses your kubeconfig and the NRP's own sign-in. It
  never reads or stores your token, and nothing leaves your computer except the Kubernetes
  calls you approve.
- **Nothing happens without a yes.** Planning creates nothing. Running and cleanup need a
  single-use token bound to the exact plan, valid for 10 minutes.
- **Nothing goes public by accident.** A web plan publishes only when you type its exact
  URL back.
- **The NRP rules are built in**, each with a test and a link to its source:
  - non-sensitive data only
  - Jobs instead of sleep loops
  - limits within 20% of requests
  - quota-gated GPUs (A100/H100/H200/GH200)
  - no GPUs on long-running services
  - allowed priority classes only
  - secrets kept out of specs (other users can see specs)

  A plan that breaks a rule is refused, with the reason and the fix.
- **Shared namespaces are safe.** Every object is labelled with its owner, and cleanup only
  sees your own runs unless you are the namespace admin and ask for everyone's.
- **Caps** on pods and GPUs running at once, tasks per sweep, and hours per run.
- **Audit log** of every change at `~/.local/state/nrp-mcp/audit.log`.
- **Setup only touches your user folder.** kubectl and kubelogin go into `~/.local/bin`
  (Windows: `%LOCALAPPDATA%\Programs\nrp-mcp\bin`). They come only from dl.k8s.io and the
  kubelogin GitHub releases, and only when the published SHA256 matches.

See [SECURITY.md](SECURITY.md) for details and how to report a problem.

## Configuration

All optional. `nrp-mcp init` writes an example to `~/.config/nrp-mcp/config.yaml`:

```yaml
kubeconfig: ~/.kube/config    # the NRP config from https://nrp.ai/config
context: nautilus
namespace: my-lab             # default namespace
kubectl: kubectl              # full path if kubectl is not on PATH
pods_per_run: 50              # pods at the same time; plans over these caps are refused
gpus_per_run: 4               # GPUs in use at the same time
tasks_per_run: 10000          # total tasks in one sweep
hours_per_run: 48
upload_gb_per_run: 50
```

Environment overrides: `NRP_KUBECONFIG`, `NRP_CONTEXT`, `NRP_NAMESPACE`, `NRP_KUBECTL`.
Command-line flags for `serve`, `setup` and `doctor`: `--kubeconfig`, `--context`,
`--namespace`, `--config`.

| Command | What it does |
|---|---|
| `nrp-mcp serve` | The MCP server on stdio (what your AI client starts) |
| `nrp-mcp setup` | Check and ready this computer (asks first; `--yes` to skip the question, `--no-sign-in` to stop before the browser) |
| `nrp-mcp doctor` | Check kubectl, sign-in, namespaces and quotas |
| `nrp-mcp init` | Write an example config (never overwrites) |
| `nrp-mcp version` | Print the version |

## How it works

```
AI client (Hermes Agent, Gemini CLI, OpenCode, Claude Code, ...)
   | MCP over stdio
nrp-mcp serve                    on your computer
   |-- inspect   reads your project folder (language, GPU use, entry point, web app, Slurm script)
   |-- plan      builds manifests from facts + goal (pure, tested)
   |-- rules     checks every NRP policy (pure, tested)
   |-- store     plans, hashed confirm tokens, audit log
   `-- kube      runs kubectl as you (kubelogin handles the sign-in)
   |
Nautilus (Kubernetes): Jobs, Deployments, volumes, Ingress in your namespace
```

Small projects are copied into the pod as a ConfigMap and their requirements installed at
start. Bigger ones get an image built on NRP GitLab (`nrp_build`). The full design, every
rule and the test record are in [SPEC.md](SPEC.md).

## Limits

- Code is copied as text files under 200 KB each (900 KB total). Bigger projects need an
  image (`nrp_build`) or git.
- `nrp_build` suggests files; it doesn't build. Builds run on NRP GitLab.
- S3 uploads are guidance only; S3 keys come from the NRP portal.
- Sessions are port-forwarded and never public. For zero install, the NRP runs a hosted
  JupyterHub at https://jupyterhub-west.nrp-nautilus.io.
- `nrp-mcp setup` installs and checks kubectl and kubelogin on Linux, Windows and macOS
  (Intel and Apple Silicon) in CI on every change; the browser sign-in step can't run in
  CI and is tested by hand. Please [report](https://github.com/UCR-Research-Computing/nrp-mcp/issues)
  anything that goes wrong.

## Development

```
bash scripts/gauntlet.sh                                 # gofmt, vet, tests, build (same as CI)
go build -o nrp-mcp ./cmd/nrp-mcp
go run ./scripts/e2e  -bin ./nrp-mcp -ns <namespace>     # live: job, sweep, web plan, cleanup
go run ./scripts/e2e2 -bin ./nrp-mcp -ns <namespace>     # live: volume, data, Jupyter, cleanup
```

The live tests create small, labelled, CPU-only workloads and clean them up; they never
create GPU pods or public endpoints. See [CONTRIBUTING.md](CONTRIBUTING.md) and
[CHANGELOG.md](CHANGELOG.md). Example app: [examples/leaf-area-explorer](examples/leaf-area-explorer).

## Help

- Questions and bugs: [GitHub issues](https://github.com/UCR-Research-Computing/nrp-mcp/issues)
- UCR researchers: research-computing@ucr.edu and the
  [Nautilus guides](https://ucr-research-computing.github.io/kb/kb023-nautilus-researcher-guide/)
- The cluster itself: https://nrp.ai/contact

Nautilus is operated by the National Research Platform (UC San Diego / SDSC). This is a
community tool from UCR Research Computing, not an NRP product. Please follow the
[NRP policies](https://nrp.ai/documentation/userdocs/start/policies/).

## License

[MIT](LICENSE)
