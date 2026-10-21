# Start here: nrp-mcp in 20 minutes

This one-page guide takes you from a fresh laptop (macOS, Windows or Linux) to your first
Nautilus run, saved results, approval code, and cleanup, with no Kubernetes knowledge required.

## Contents

- [0. Before you start: NRP access and namespace request](#0-before-you-start-nrp-access-and-namespace-request)
- [1. Install nrp-mcp (and which download per OS)](#1-install-nrp-mcp-and-which-download-per-os)
- [2. Download your NRP config and run setup](#2-download-your-nrp-config-and-run-setup)
- [3. Set up job results (nrp-mcp s3 setup)](#3-set-up-job-results-nrp-mcp-s3-setup)
- [4. Connect your AI client (nrp-mcp client)](#4-connect-your-ai-client-nrp-mcp-client)
- [5. Your first run](#5-your-first-run)
- [6. Approval codes](#6-approval-codes)
- [7. Cleanup and the 14-day rule](#7-cleanup-and-the-14-day-rule)
- [Jargon table (plain-language glossary)](#jargon-table-plain-language-glossary)
- [If you get stuck](#if-you-get-stuck)

---

## 0. Before you start: NRP access and namespace request

You need a National Research Platform (NRP) account and membership in at least one
**namespace** (your lab's or course's project space on Nautilus).

1. **Sign in once at https://nrp.ai.** Click **Login**, choose your university or
   institution (CILogon), and accept the **Acceptable Use Policy (AUP)** on first sign-in.
   *Important order of steps:* you **must** sign in at https://nrp.ai at least once
   **before** your PI or instructor can add you. Until you sign in once, your account does
   not exist in the NRP portal for them to invite.
2. **Get added to (or request) a namespace at https://nrp.ai/namespaces:**
   - **Students, postdocs and lab members:** send your PI or namespace admin the exact email
     or institutional identity you used at https://nrp.ai and ask them to add you to the
     group's namespace at https://nrp.ai/namespaces.
   - **Faculty, staff and postdocs starting a new group:** request a namespace at
     https://nrp.ai/namespaces (or use a workshop join link if your instructor gave you one).
3. **UCR researchers:** see
   [Getting access to Nautilus (KB024)](https://ucr-research-computing.github.io/kb/kb024-nautilus-getting-access/)
   or email `research-computing@ucr.edu`.

---

## 1. Install nrp-mcp (and which download per OS)

### Option A: One-line installer (recommended)

**macOS and Linux** (open **Terminal**):

```bash
curl -fsSL https://raw.githubusercontent.com/UCR-Research-Computing/nrp-mcp/main/scripts/install.sh | sh
```

The installer picks the right archive for your OS and chip, checks `SHA256SUMS`, clears
macOS quarantine, installs `~/.local/bin/nrp-mcp`, detects your shell (`zsh` on macOS ->
`~/.zprofile`; `bash` on Linux -> `~/.bashrc`), asks before adding `~/.local/bin` to your
`PATH`, and prints the full binary path. Open a **new terminal window** after it finishes
(or run the full path `~/.local/bin/nrp-mcp` in your current window).

**Windows** (open **PowerShell**: press **Start**, type `PowerShell`, press **Enter**):

```powershell
irm https://raw.githubusercontent.com/UCR-Research-Computing/nrp-mcp/main/scripts/install.ps1 | iex
```

The PowerShell installer downloads the matching zip for your processor (`amd64` or `arm64`),
verifies `SHA256SUMS` with `Get-FileHash`, installs
`%LOCALAPPDATA%\Programs\nrp-mcp\bin\nrp-mcp.exe`, unblocks the file with `Unblock-File`,
and adds that folder to your User `Path` with
`[Environment]::SetEnvironmentVariable("Path", ..., "User")` (never use `setx PATH`, which
can truncate or corrupt your `PATH`). Open a **new PowerShell window** after it finishes.

### Option B: Manual download per OS

If you prefer not to run an installer script, download the archive for your computer from
[GitHub Releases](https://github.com/UCR-Research-Computing/nrp-mcp/releases/latest):

| Operating system and processor | Release file to download | Binary inside |
|---|---|---|
| **macOS (Apple Silicon: M1, M2, M3, M4)** | `nrp-mcp_<version>_darwin_arm64.tar.gz` | `nrp-mcp` |
| **macOS (Intel)** | `nrp-mcp_<version>_darwin_amd64.tar.gz` | `nrp-mcp` |
| **Windows (64-bit Intel / AMD)** | `nrp-mcp_<version>_windows_amd64.zip` | `nrp-mcp.exe` |
| **Windows (Snapdragon / ARM64)** | `nrp-mcp_<version>_windows_arm64.zip` | `nrp-mcp.exe` |
| **Linux (x86_64 / Intel / AMD)** | `nrp-mcp_<version>_linux_amd64.tar.gz` | `nrp-mcp` |
| **Linux (ARM64 / aarch64)** | `nrp-mcp_<version>_linux_arm64.tar.gz` | `nrp-mcp` |

### macOS Gatekeeper and Windows SmartScreen steps

Release binaries are attested with GitHub build provenance (`SHA256SUMS`), not signed with
an Apple or Microsoft commercial certificate. If you download an archive in a web browser
instead of using the installer script:

- **macOS Gatekeeper** (*"Apple could not verify nrp-mcp is free of malware"*):
  1. In **Terminal**, remove the browser quarantine attribute:
     ```bash
     xattr -d com.apple.quarantine ~/.local/bin/nrp-mcp
     ```
  2. Or in macOS Settings: open **System Settings > Privacy & Security**, scroll down to the
     **Security** section, and click **Open Anyway** next to `nrp-mcp`.
  *(Note: `install.sh` clears `com.apple.quarantine` automatically.)*

- **Windows SmartScreen and PowerShell**:
  1. `nrp-mcp.exe` is a command-line tool. Do **not** double-click `nrp-mcp.exe` in File
     Explorer (it prints usage text and closes the window immediately). Run it from
     **PowerShell**.
  2. If Microsoft Defender SmartScreen shows **"Windows protected your PC"**, click
     **More info**, then click **Run anyway**; or in PowerShell run:
     ```powershell
     Unblock-File "$env:LOCALAPPDATA\Programs\nrp-mcp\bin\nrp-mcp.exe"
     ```
  3. If you unpacked a zip manually and are inside that folder in PowerShell, type
     `.\nrp-mcp.exe` (PowerShell does not run programs from the current folder without `.\`).
  4. **University-managed Windows laptops:** if campus group policy (AppLocker or Smart App
     Control) blocks executables under `%LOCALAPPDATA%`, ask campus IT to allow
     `%LOCALAPPDATA%\Programs\nrp-mcp\bin`.

**Check:** run `nrp-mcp version` in a new terminal. It prints `nrp-mcp 1.0.0` (and tells you
if a newer release is available).

---

## 2. Download your NRP config and run setup

1. Visit **https://nrp.ai/config** in your browser and click **Get Config**. Leave the
   downloaded `config` file in your `Downloads` folder.
2. In your terminal (macOS/Linux) or PowerShell (Windows), run:
   ```bash
   nrp-mcp setup
   ```
3. Answer `y` when asked. `nrp-mcp setup`:
   - Installs `kubectl` and the `kubelogin` (`kubectl-oidc_login`) sign-in plugin into your
     user folder (`~/.local/bin` on macOS/Linux,
     `%LOCALAPPDATA%\Programs\nrp-mcp\bin` on Windows) from official releases after checking
     every SHA-256 digest (no admin rights needed).
   - Copies your downloaded NRP config into `~/.kube/config` (backing up any existing file).
   - Opens your browser so you can sign in through your university and finish MFA.
   - Checks that your account belongs to at least one Nautilus namespace.
4. Verify everything at any time with:
   ```bash
   nrp-mcp doctor
   ```
   `doctor` checks `kubeconfig`, `kubectl`, `sign-in`, `namespaces` (zero namespaces is a
   `FAIL`), `consent`, `s3-keys` (`yes` or `no`, never printing key values), and `release`.
   Every `FAIL` line ends with `->` and the exact command to run or person to ask.
   If you ever need to run a `kubectl` command yourself, run `nrp-mcp kubectl ...` (for
   example `nrp-mcp kubectl get pods`), which uses the installed `kubectl` and your NRP
   config automatically.

---

## 3. Set up job results (`nrp-mcp s3 setup`)

Batch jobs and sweeps run inside temporary container scratch (`/work`) that is wiped when
the job finishes. To keep your output files automatically:

1. Open **https://nrp.ai/s3token/** in your browser to create or view your NRP S3 access key
   and secret key.
2. In your own terminal, run once:
   ```bash
   nrp-mcp s3 setup
   ```
   Paste your access key and secret key (secret input is hidden). `nrp-mcp` saves them in
   `~/.config/nrp-mcp/s3.env` (Windows: `%USERPROFILE%\.config\nrp-mcp\s3.env`) with file
   permissions `0600`.
3. **How results come back:**
   - Your raw S3 keys are **never** sent to the cluster. For each job or sweep, `nrp-mcp`
     signs a short-lived S3 POST policy limited to `runs/<run-id>/` in your personal bucket
     (`nrp-results-<id>`).
   - When your job finishes, a lightweight sidecar uploads everything in `results/` (plus
     any new files your code wrote in `/work`).
   - Ask your assistant *"Copy the results back"* (calls `nrp_data action=down run=<id>`),
     and the files land in `<project>/results/<run-id>/` on your computer.
4. **What if you skip S3 setup?**
   - By rule **R23**, `nrp_plan` refuses `job` and `sweep` plans when S3 keys are not
     configured, so you never lose output files by surprise.
   - If a job writes all of its output to a shared cluster volume (`/data` via
     `data_volume=...`) or only prints to logs, tell the assistant *"yes, discard results"*
     and it will pass `discard_results=true`.

---

## 4. Connect your AI client (`nrp-mcp client`)

Run `nrp-mcp client <name>` once in your terminal to wire `nrp-mcp` into your AI app using
the full binary path (user scope, backing up any existing config file to `.bak`):

```bash
nrp-mcp client claude-desktop   # or: claude-code, vscode, cursor, gemini, opencode, hermes
```

Add `--namespace <your-namespace>` if you belong to several namespaces and want to pin a
default.

| AI client | Command to run | Config file written (user scope) | After running |
|---|---|---|---|
| **Claude Desktop** | `nrp-mcp client claude-desktop` | macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`<br>Windows: `%APPDATA%\Claude\claude_desktop_config.json`<br>Linux: `~/.config/Claude/claude_desktop_config.json` | Fully quit Claude Desktop (menu bar / system tray > **Quit**) and reopen |
| **Claude Code** | `nrp-mcp client claude-code` | `~/.claude.json` (`mcpServers.nrp`) | Restart `claude` |
| **VS Code** | `nrp-mcp client vscode` | macOS: `~/Library/Application Support/Code/User/mcp.json`<br>Windows: `%APPDATA%\Code\User\mcp.json`<br>Linux: `~/.config/Code/User/mcp.json` (uses top-level `"servers"`, not `"mcpServers"`) | Restart VS Code |
| **Cursor** | `nrp-mcp client cursor` | `~/.cursor/mcp.json` (`mcpServers.nrp`) | Restart Cursor |
| **Gemini CLI** | `nrp-mcp client gemini` | `~/.gemini/settings.json` (`mcpServers.nrp`) | Restart `gemini` |
| **OpenCode** | `nrp-mcp client opencode` | `~/.config/opencode/opencode.json` (`mcp.nrp`) | Restart `opencode` |
| **Hermes Agent** | `nrp-mcp client hermes` | `~/.hermes/config.yaml` (`mcp_servers.nrp`) | Restart `hermes` |

---

## 5. Your first run

Open your AI client and ask in plain language:

1. **"Where do I stand on Nautilus?"**
   - Calls `nrp_status`. You should see your identity, your namespace(s), GPU quotas,
     running workloads, shared volumes, public URLs, and policy warnings.
2. **"Run `train.py` in `~/proj/my-study` on a GPU."** (or *"Run the Slurm script in
   `examples/slurm-array-bootstrap`."*)
   - Calls `nrp_plan`. Planning creates nothing on the cluster.
   - Returns a plain-language `summary`, `resources` (`cpu`, `memory`, `gpu`, `hours` per
     object), `decisions`, `refusals` (if any rule was broken, with the fix), `code_files`
     copied, and `skipped_files`. Full Kubernetes YAML is omitted to keep chat context small;
     ask for `show_manifests=true` any time you want to inspect the raw YAML.
3. **"Run it."**
   - Calls `nrp_run`. Low-risk CPU batch plans run with the plan's single-use confirm token;
     GPU and public web plans ask for your approval code (see Step 6).
4. **"How is it going?"** / **"Copy the results back."**
   - `nrp_watch` checks pod progress, tails/greps logs (marked as untrusted data), and
     translates common failures (out of memory, missing Python/R package, missing file, slow
     image pull) into plain fixes.
   - `nrp_data action=down run=<id>` downloads saved results into `<project>/results/<id>/`.

---

## 6. Approval codes

Risky actions need **your** yes, not the AI's:
- Running a plan that uses a **GPU** (`gpu >= 1`)
- Publishing a **public web app** (`goal=web`)
- **Deleting** runs or volumes (`nrp_cleanup` step 2)
- **Uploading** files to a cluster volume (`nrp_data action=up`)
- Installing or updating local tools from chat (`nrp_setup fix=true`)

How it works:

1. If your AI client supports interactive MCP questions (elicitation), it pops up a yes/no
   prompt directly.
2. Otherwise, when the assistant calls the tool, `nrp-mcp` pauses (`NOT STARTED: waiting for
   the person's approval`) and generates a 6-character one-time code such as `ABC-234`.
3. `nrp-mcp` sends the code to a desktop notification banner and makes it available in your
   terminal. If you missed the notification banner, open a terminal or PowerShell window and
   run:
   ```bash
   nrp-mcp approve
   ```
4. Read what the assistant says it is about to do. If you agree, type the 6-character code
   into the chat. The assistant re-calls the tool with `approval_code=ABC-234`.

Codes last **10 minutes**, work **once**, and are bound to that exact plan and action. Never
share an approval code with anyone or paste a code you did not ask for.

---

## 7. Cleanup and the 14-day rule

### Cleaning up (`nrp_cleanup`)

Always clean up finished test runs, idle notebooks and unneeded web apps so other
researchers can use the shared cluster:

1. Ask: **"What runs do I have?"** (`nrp_cleanup` with no arguments lists every run labelled
   with your identity in the namespace).
2. Ask: **"Clean up run `<id>`."** `nrp_cleanup` lists the exact objects it will remove and
   waits for your approval code before deleting them.
3. **Volumes hold data:** if a run includes a shared volume (`PersistentVolumeClaim`), copy
   your files down with `nrp_data action=down` before approving its deletion.

### The 14-day rule (and workload lifetimes)

- **Public web apps (`goal=web`) expire after 14 days:** NRP policy (**R17**) removes
  long-running `Deployments` after 2 weeks unless the namespace is on the NRP exceptions
  list (https://nrp.ai/documentation/userdocs/running/long-idle/). `nrp-mcp` stamps every
  web app with an `nrp-mcp/expires` date 14 days out (configurable up to 14 days via
  `web_days`), and `nrp_status` shows how many days remain.
- **CPU notebook / VS Code sessions (`goal=session`, `gpu=0`):** run as private
  `Deployments` reached through `nrp-mcp kubectl ... port-forward` (never public) and are
  also subject to the 2-week Deployment limit. Stop them with `nrp_cleanup` when done.
- **GPU notebook / VS Code sessions (`goal=session`, `gpu>=1`):** run as time-limited
  `Jobs` (default **6 hours**, max **12 hours** via `gpu_session_hours`) because NRP policy
  (**R8**) forbids `Deployments` from requesting GPUs. Keep GPU utilization above 40% while
  the session is running.
- **Shared volumes (`goal=volume`):** persist across jobs, but NRP purges volumes that go
  untouched for **6 months**. Keep master copies of research data on campus or archival
  storage.

---

## Jargon table (plain-language glossary)

| Term you may see | What it means in plain language |
|---|---|
| **pod** | One running copy of your program (a single container worker on a cluster machine). A single job has 1 pod; a 50-task sweep runs up to 50 pods at a time. |
| **namespace** | Your lab's or class's shared project space on Nautilus (for example `ucr-research-computing`). Every object you create lives inside a namespace, and `nrp-mcp` labels your objects with your identity so you only delete your own work. |
| **volume** (`PVC`) | A persistent shared folder on the cluster (`PersistentVolumeClaim`, mounted at `/data`) that stays around after a job ends and can be read and written by many pods at once. |
| **public URL** (`Ingress`) | An `https://<name>.nrp-nautilus.io` web address reachable by anyone on the internet with no login (`goal=web`). Requires your approval code to publish, serves P1 data only, and expires after 14 days. |
| **P1** (`data_is_p1`) | Public or low-risk non-sensitive data (UC Protection Level 1). **Allowed on Nautilus.** Anything with personal identifiers, student records (FERPA), patient/clinical data (HIPAA), controlled human genomes, or restrictive data-use agreements is **not** P1 and must never be used on Nautilus. |
| **Job / sweep** (`Indexed Job`) | A batch run that starts, runs your script to completion, and stops. A **sweep** runs `count` numbered tasks (`0` to `count-1` in `$JOB_COMPLETION_INDEX` and `$SLURM_ARRAY_TASK_ID`), up to `parallel` pods at a time. |
| **Deployment** | An always-on service (used for CPU web apps and CPU notebook sessions). Cannot request GPUs on Nautilus and is removed after 14 days. |
| **opportunistic** | Preemptible low-priority mode (`priorityClassName: opportunistic`). Lets a batch job borrow quota-gated GPUs (such as A100/H100), but the pod can be stopped at any time when the hardware owner needs it; your script must checkpoint and resume. |
| **confirm token vs approval code** | A **confirm token** is a 10-minute token returned to the assistant for low-risk CPU plans. An **approval code** is a 6-character code (`nrp-mcp approve`) shown only to **you** for risky actions (GPU, public web, cleanup, upload, setup). |

---

## If you get stuck

| Symptom | Fix |
|---|---|
| `nrp-mcp: command not found` (or not recognized in PowerShell) | Open a **new** terminal/PowerShell window so the updated `PATH` loads, or run by full path (`~/.local/bin/nrp-mcp` on macOS/Linux; `& "$env:LOCALAPPDATA\Programs\nrp-mcp\bin\nrp-mcp.exe"` on Windows). |
| `doctor` says `FAIL namespaces` (0 namespaces) | Sign in once at https://nrp.ai and accept the AUP, then ask your PI/instructor to add your identity at https://nrp.ai/namespaces. |
| AI client shows "server disconnected" or no `nrp` tools | Run `nrp-mcp client <name>` in your terminal so the config uses the full binary path, then **fully quit and reopen** the AI app. |
| Missed the desktop notification for an approval code | Run `nrp-mcp approve` in your own terminal to print the current 6-character code (valid for 10 minutes). |
| Sign-in expired mid-chat (browser window opened) | Finish the university sign-in + MFA in the browser tab (up to 3 minutes), then repeat your question in chat; or run `nrp-mcp setup` in a terminal. |
| Need to update `nrp-mcp` | Re-run the Step 1 installer command, then quit and reopen your AI client. Check with `nrp-mcp version`. |

When asking for help, run `nrp-mcp doctor` and paste its output (it never prints secrets):
- **nrp-mcp bugs and questions:** [GitHub issues](https://github.com/UCR-Research-Computing/nrp-mcp/issues)
- **UCR researchers:** `research-computing@ucr.edu` and the [Nautilus guides](https://ucr-research-computing.github.io/kb/kb023-nautilus-researcher-guide/)
- **Nautilus cluster operations (accounts, namespaces, outages):** https://nrp.ai/contact (NRP Matrix support chat)
