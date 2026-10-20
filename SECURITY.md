# Security policy

## Reporting a vulnerability

Please report security problems privately, not in a public issue:

- GitHub: use **Report a vulnerability** on the Security tab of this repository, or
- email research-computing@ucr.edu with "nrp-mcp security" in the subject.

We aim to acknowledge reports within three working days.

## How nrp-mcp handles your credentials

- It runs on your own computer and acts as you, through `kubectl` and the NRP's kubelogin
  plugin. It never reads, stores, prints or sends your sign-in token; kubelogin keeps it in
  its own cache.
- It never reads your kubeconfig's secrets. `nrp_setup` reads only the cluster address and
  the public certificate authority from it, to ask the cluster for its version.
- Secrets your jobs need (S3 keys, an LLM token) belong in Kubernetes Secrets. The rules
  engine refuses plans that put a secret-looking value directly in a spec, because job specs
  can be visible to other cluster users.
- Risky steps (public web apps, GPU work, deleting, uploading, installing) need your own
  approval: a question from your AI app, or a one-time code that only you see (desktop
  notification, `nrp-mcp approve`). Other changes need a single-use confirm token bound to
  the exact plan (10 minutes). Known limit: an AI assistant that can run shell commands on
  your computer could read the code file (`~/.local/state/nrp-mcp/approve.txt`). Codes stop
  an assistant that only has nrp-mcp's tools, and prompt injection through job output;
  they do not protect a compromised computer.
- S3 keys for saving results stay on your computer (`~/.config/nrp-mcp/s3.env`, 0600).
  Pods get only a signed upload permission for one run's folder.
- `nrp_setup` downloads only from dl.k8s.io and the kubelogin GitHub releases, verifies the
  published SHA256 before installing, installs into your user folder, and keeps any old copy.
- Every change is written to a local audit log (`~/.local/state/nrp-mcp/audit.log`).

## Supported versions

Only the latest release gets fixes.
