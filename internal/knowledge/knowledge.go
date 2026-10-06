// Package knowledge serves the NRP rules and UCR guides as MCP resources, so an
// assistant reads them instead of guessing. Dated and sourced; re-check each release.
package knowledge

// Reviewed is when this digest was last checked against the NRP docs.
const Reviewed = "2026-10-06"

// Policy is the NRP rule digest.
const Policy = `# NRP Nautilus rules nrp-mcp enforces (reviewed ` + Reviewed + `)

Source: https://nrp.ai/documentation/userdocs/start/policies/ unless noted.

- Data: non-sensitive only (UCR P1). No HIPAA, FERPA, PII, FISMA, CUI or data under a
  restrictive data-use agreement, in storage or in prompts to the hosted LLMs.
- Non-profit, non-commercial use only (NRP AUP).
- Batch work runs as a Job with right-sized requests. A Job whose command is sleep (or ends
  with sleep) gets the user banned.
- Bare pods count as interactive: deleted after 6 hours; at most 2 GPUs, 32 GB RAM, 16 cores.
- Deployments (long-running services) are removed after 2 weeks unless the namespace is on
  the exceptions list, and idle Deployments cannot request GPUs.
  (https://nrp.ai/documentation/userdocs/running/long-idle/)
- Limits must be within 20% of requests; with more than 100 pods, limit = request.
- Usage checks: a user may not run more than 4 pods with GPU use under 40%, CPU outside
  20-200% or memory outside 20-150% of the request (pods at 1 CPU / 2 GB are exempt).
- GPUs: up to 8 per pod in Jobs, 2 in bare pods. A100/H100/H200/GH200 are quota-gated
  (default 0). A100 access is requestable; the others are not. priorityClassName
  opportunistic bypasses the GPU quota but can be preempted at any time.
  (https://nrp.ai/documentation/userdocs/running/gpu-pods/,
  https://nrp.ai/documentation/userdocs/running/priority-classes/)
- Allowed priorityClassName: unset, armada-default, owner-no-preempt, opportunistic,
  opportunistic2. Anything else is rejected.
- Storage not accessed for 6 months can be purged without notice; not an archive. Delete big
  trees with find -delete, not rm -rf.
- Job specs can be visible to other users; never put secrets in specs. Use Secrets.
- HTTP services are exposed with an Ingress (class haproxy) at <name>.nrp-nautilus.io or your
  own domain with a certificate. Non-HTTP ports are not exposed.
  (https://nrp.ai/documentation/userdocs/running/ingress/)
- Moving data: never push large data through kubectl cp (it goes through the API server);
  use S3 or a pull Job. (https://nrp.ai/documentation/userdocs/storage/move-data/)
`

// GPUs is the GPU resource table.
const GPUs = `# GPU resources on Nautilus (reviewed ` + Reviewed + `)

| Resource | GPU | Access |
|---|---|---|
| nvidia.com/gpu | any standard GPU (RTX 2080 Ti/3090/4090, A10, A4000, ...) | default; Kubernetes picks a free one |
| nvidia.com/a40 | NVIDIA A40 | request by type |
| nvidia.com/rtxa6000 | RTX A6000 | request by type |
| nvidia.com/rtx8000 | Quadro RTX 8000 | request by type |
| nvidia.com/mig-small | A100 MIG 1g.10gb | request by type |
| nvidia.com/a100 | A100 | quota 0 by default; request access, or opportunistic |
| nvidia.com/h100, h200, gh200 | H100 / H200 / GH200 | not requestable; opportunistic only |
| nvidia.com/rtx6000bw | RTX PRO 6000 Blackwell | reserved |

Tested from a UCR namespace on 2026-10-06: 1 x nvidia.com/gpu landed on an RTX A4000 at SDSC,
about 40-61 TFLOPS fp16 in a PyTorch matmul check. Add an 8 GiB memory-backed /dev/shm for
PyTorch data loaders.
`

// Storage is the storage chooser.
const Storage = `# Storage chooser (reviewed ` + Reviewed + `)

| Need | Use | Notes |
|---|---|---|
| Temporary files during a job | container scratch (ephemeral) | default limit 50Gi per container |
| Shared files across pods, checkpoints | PVC, storageClass rook-cephfs-central (ReadWriteMany) | ~86 MiB/s per writer in a UCR test; not for pip/conda installs or many small files |
| One pod's database or small files | PVC, rook-ceph-block-central (ReadWriteOnce) | slow in tests (~5 MB/s write) |
| Datasets in and out, sharing | S3: https://s3-west.nrp-nautilus.io (default), s3-central, s3-east; in-cluster http://rook-ceph-rgw-nautiluss3.rook | keys from the NRP portal (User, then S3 Tokens); ObjectBucketClaims are forbidden for namespace users |
| Never | ceph-rbd | never provisions |

Keep master copies off Nautilus (CephRDS, HPCC storage, cloud archive). Volumes untouched for
6 months can be purged.
`

// KB lists the UCR guide series.
const KB = `# UCR Research Computing guides to Nautilus

1. Researcher guide (overview): https://ucr-research-computing.github.io/kb/kb023-nautilus-researcher-guide/
2. Getting access (accounts, namespaces, kubectl): https://ucr-research-computing.github.io/kb/kb024-nautilus-getting-access/
3. Notebooks, desktops and VS Code (JupyterHub, Coder): https://ucr-research-computing.github.io/kb/kb025-nautilus-jupyter-and-coder/
4. Batch jobs and GPUs: https://ucr-research-computing.github.io/kb/kb026-nautilus-batch-jobs-and-gpus/
5. Storage and moving data: https://ucr-research-computing.github.io/kb/kb027-nautilus-storage-and-data/
6. Hosted LLMs: https://ucr-research-computing.github.io/kb/kb028-nautilus-llm-api/
7. Hosting a web tool: https://ucr-research-computing.github.io/kb/kb029-nautilus-hosting-web-tools/
8. Teaching a class or workshop: https://ucr-research-computing.github.io/kb/kb030-nautilus-teaching-and-workshops/

UCR help: research-computing@ucr.edu. NRP help: https://nrp.ai/contact (Matrix).
`

// LLM describes the hosted LLM service.
const LLM = `# NRP hosted LLMs (reviewed ` + Reviewed + `)

- OpenAI-compatible endpoint: https://ellm.nrp-nautilus.io/v1 (chat and embeddings).
- Needs membership of a group with the LLM flag; personal token at https://nrp.ai/llmtoken.
- Fair use: 200,000 output tokens per minute per token and model (HTTP 429 beyond); per-user
  concurrency 2 (kimi, glm-5, deepseek-v4-flash), 8 (minimax-m2, qwen3-small, gemma,
  gemma-small), 16 (qwen3, gpt-oss, qwen3-embedding). Retry with backoff.
- Send a private cache_salt (extra_body) if prompts must not share the cache.
- P1 data only; non-commercial.
- In a Job, keep the token in a Secret (nrp-mcp goal llm-batch does this).
Models: https://nrp.ai/documentation/userdocs/ai/llm-managed/models/
`
