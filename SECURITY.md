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
- Changes need a single-use confirm token bound to the exact plan (10 minutes). Publishing
  to the internet also needs the exact public URL typed back.
- `nrp_setup` downloads only from dl.k8s.io and the kubelogin GitHub releases, verifies the
  published SHA256 before installing, installs into your user folder, and keeps any old copy.
- Every change is written to a local audit log (`~/.local/state/nrp-mcp/audit.log`).

## Supported versions

Only the latest release gets fixes.
