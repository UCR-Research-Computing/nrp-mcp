# 09. Probing Confidence and Correctness Inside Open-Weight LLMs

**Field:** Trustworthy AI, machine learning interpretability | **UCR connection:** "Making AI more trustworthy" (UCR News, Sept. 23, 2026), a UCR computer science study showing that confidence and correctness arise from different internal features of open-weight LLMs, found with sparse autoencoders and tested by suppressing features at answer time: https://news.ucr.edu/articles/2026/09/23/making-ai-more-trustworthy | **Fit:** Good

## The science

Large language models can be confidently wrong and hesitantly right. The UCR study examined two open-weight models (Llama-3.1-8B and Gemma-2-9B) with sparse autoencoders (SAEs) and found features tied mostly to uncertainty, mostly to wrong answers, or to both ("confounded"). Suppressing confounded features raised accuracy by up to 1.1% and cut uncertainty by up to 75%; three such features from one middle layer predicted wrong answers well enough that abstaining on flagged questions raised accuracy from 62% to 81%. Natural next questions: do these features hold across more models, benchmarks and layers, and how do white-box signals compare with asking a hosted model how sure it is?

Compute is the bottleneck because the design is a grid: every model x benchmark cell needs forward passes over thousands of questions, SAE encoding at many layers, and then intervention runs. Each cell fits on one standard GPU, but there are dozens of cells: a GPU sweep of independent tasks.

## Who at UCR does this

The Department of Computer Science and Engineering (https://www.cs.ucr.edu/) in the Bourns College of Engineering. The study was led by CSE faculty, with collaborators at Arizona State University. Groups across CSE that work on interpretability, robustness and AI safety run studies with the same shape. None of these groups use nrp-mcp today; this is an illustration.

## The data

All public benchmarks, all P1:

- MMLU, https://huggingface.co/datasets/cais/mmlu (MIT license; about 14,000 test questions in 57 subjects).
- ARC, https://huggingface.co/datasets/allenai/ai2_arc (CC BY-SA 4.0).
- TruthfulQA, https://huggingface.co/datasets/truthfulqa/truthful_qa (Apache 2.0).
- TriviaQA, https://huggingface.co/datasets/mandarjoshi/trivia_qa (Hugging Face lists the license as unknown; check its terms before redistributing derived files).

Models and SAEs: Gemma-2-9B (https://huggingface.co/google/gemma-2-9b) and Llama-3.1-8B (https://huggingface.co/meta-llama/Llama-3.1-8B) are gated: each user accepts the license on Hugging Face. Public SAEs exist for both: Gemma Scope (https://huggingface.co/google/gemma-scope-9b-pt-res, CC BY 4.0) and Llama Scope (https://huggingface.co/OpenMOSS-Team/Llama-Scope, Apache 2.0). An ungated third model (e.g. Qwen2.5-7B-Instruct, Apache 2.0) can use linear probes on raw activations. Weights plus SAEs are roughly 100-200 GB.

Nothing here is sensitive. If a follow-up study probed models on real user chats, clinical notes or student writing, that text would be P3/P4 and would not go to Nautilus; it belongs in the UCR Secure Enclave or an Ursa Major Tier 2 secure GCP project.

## The conversation

**Researcher:** I want to extend the confidence/correctness analysis to three models and four benchmarks, every fourth layer. Where do I stand on Nautilus?

**Assistant:** [nrp_status] Your lab's namespace; A100/H100 quota is 0, so we stay on standard GPUs. Nothing is running.

**Researcher:** Make a 500 GB volume for weights and activations. Everything is public benchmarks and public models.

**Assistant:** [nrp_plan goal=volume size=500Gi data_is_p1=true] Plan: one shared CephFS volume "probe-data" (rook-cephfs-central, ReadWriteMany), mounted at /data by jobs and sessions. Note: unused volumes are purged after 6 months. Approve? **Researcher:** Yes. [nrp_run] Created.

**Assistant:** Llama and Gemma are gated, and nrp-mcp only wires a Secret for the NRP LLM token. The simplest path: start a private session, sign in to Hugging Face there, and download the weights and SAEs into /data once. [nrp_plan goal=session data_volume=probe-data cpu=4 memory=16Gi] then [nrp_session] gives you a port-forward command and a local Jupyter link. The token stays in that session, not in any spec.

**Researcher:** Done, weights are in /data/models. Now plan the sweep: extract.py reads SLURM_ARRAY_TASK_ID and picks a model x benchmark cell, looping over layers.

**Assistant:** [nrp_plan goal=sweep count=12 parallel=4 gpu=1 data_volume=probe-data] Summary: an Indexed Job, 12 tasks, 4 at a time, each on 1 standard GPU with 4 CPU and 16 GiB (limits equal requests), image pytorch/pytorch, requirements.txt installed at start, /data mounted, 8 GiB /dev/shm, 24 h limit, retries. Each task gets $SLURM_ARRAY_TASK_ID 0..11. 4 GPUs at the same time is within your gpus_per_run cap of 4, so the default caps pass; the 12 tasks run in three waves of 4. The 24 h limit covers the whole sweep, all three waves, not each task; at 1-3 h per cell that fits. Confirm token valid 10 minutes. **Researcher:** Go. [nrp_run] Running; run card written to .nrp/runs/.

**Researcher:** How is it going?

**Assistant:** [nrp_watch] 9 of 12 running or done. Tasks 4-7 (Gemma-2-9B) failed: exit 1, last log lines show "torch.OutOfMemoryError: CUDA out of memory" on an RTX A4000 (16 GB). This is GPU memory, not pod memory: 9B in bf16 plus SAEs needs more than 16 GB. Fix: request a 48 GB card. [nrp_plan goal=sweep count=4 gpu=1 gpu_type=a40 command="SLURM_OFFSET=4 python extract.py"] reruns just those four on A40s. **Researcher:** Yes. [nrp_run]

**Researcher:** Also run the black-box baseline: ask qwen3 and gpt-oss each question with a 0-100 confidence.

**Assistant:** [nrp_plan goal=llm-batch command="python blackbox.py" data_volume=probe-data] One CPU Job, no GPU; the NRP LLM token comes from the Secret nrp-llm-token (key token), never the spec; base URL https://ellm.nrp-nautilus.io/v1. Your script retries with backoff (fair use about 200K output tokens per minute per token and model). Approve? **Researcher:** Yes. [nrp_run]

**Researcher:** All done? Bring back the summaries and clean up, but keep the volume.

**Assistant:** [nrp_watch] All 12 cells and the baseline succeeded. [nrp_data down volume=probe-data path=results/] copied 310 MB of probe scores, AUROC tables and feature lists to ./results. [nrp_cleanup] lists 4 runs (two sweeps, the session, the LLM job); after your second yes they are deleted. The volume stays.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PersistentVolumeClaim (CephFS, RWX) | 500 GiB |
| Download gated weights | goal=session + nrp_session | Deployment (1 replica), port-forward | 4 CPU, 16 GiB, no GPU, hours |
| Activation + SAE extraction | goal=sweep | Indexed Job | 12 tasks x 1 GPU, 4 CPU, 16 GiB; 4 at a time; 1-3 h each |
| Rerun on 48 GB cards | goal=sweep gpu_type=a40 | Indexed Job | 4 tasks x 1 A40 |
| Feature-suppression runs | goal=sweep | Indexed Job | about 12 tasks x 1 GPU, 2-4 h each |
| Black-box baseline | goal=llm-batch | Job + Secret reference | 1 CPU, 2 GiB, a few hours |
| Results home | nrp_data down | helper pod | MBs |

## Compute and cost estimate

Assumptions: about 30,000 questions; 7-9B models in bf16; an A40-class GPU covers one model x benchmark cell in 1-3 hours. Extraction is about 24 GPU-hours; feature-suppression runs add 30-40. Total about 60 GPU-hours, roughly a day with 4 GPUs at a time. Nautilus is free to the user. A laptop cannot hold a 9B model with SAEs. The HPCC can run it, but GPU queues vary. Cloud A40/L40-class GPUs cost very roughly $1-3 per GPU-hour, so $60-200 plus storage, and need an account and a budget line.

## Limits and honest caveats

- Standard GPUs (16-48 GB) hold 7-14B models in bf16 when you pick the card (gpu_type=a40 or rtxa6000 for 48 GB). The default "any standard GPU" can land on a 16 GB card. 70B models need 4-bit quantization or several GPUs in one pod. Premium GPUs are quota 0; opportunistic priority can reach them, but pods can be preempted at any time, so checkpoint per layer.
- gpus_per_run (default 4) counts GPUs in use at the same time, so 12 GPU tasks at parallel=4 pass with default caps and run in three waves. A higher parallel needs the cap raised deliberately; be courteous on a shared cluster.
- GPUs must stay above 40% utilization; keep batch sizes large and do probe fitting (CPU work) in a separate CPU job.
- Gated weights need a Hugging Face sign-in; nrp-mcp does not wire arbitrary Secrets, hence the session step.
- Hosted endpoint models can change version; record the model id and date. Whether the endpoint returns log-probabilities was not verified; verbalized confidence always works.
- For many 70B-class models, a NAIRR Pilot allocation is the better route.

## Starter kit

Sketch: `extract.py`

```python
import os, itertools, torch
from transformers import AutoModelForCausalLM, AutoTokenizer
MODELS = ["gemma-2-9b", "llama-3.1-8b", "qwen2.5-7b-instruct"]
BENCH = ["mmlu", "arc_challenge", "truthfulqa_mc", "triviaqa"]
task = int(os.environ["SLURM_ARRAY_TASK_ID"]) + int(os.environ.get("SLURM_OFFSET", 0))
model_name, bench = list(itertools.product(MODELS, BENCH))[task]
out = f"/data/acts/{model_name}/{bench}"; os.makedirs(out, exist_ok=True)
tok = AutoTokenizer.from_pretrained(f"/data/models/{model_name}")
model = AutoModelForCausalLM.from_pretrained(f"/data/models/{model_name}",
            torch_dtype=torch.bfloat16, device_map="cuda")
layers = range(0, model.config.num_hidden_layers, 4)
for batch in load_questions(bench, batch_size=32):          # your loader
    enc = tok(batch.prompts, return_tensors="pt", padding=True).to("cuda")
    with torch.no_grad():
        o = model(**enc, output_hidden_states=True)
    probs = o.logits[:, -1].softmax(-1)                      # answer-letter confidence
    save_batch(out, batch, probs, {l: o.hidden_states[l][:, -1].cpu() for l in layers})
```

`requirements.txt`

```
transformers>=4.44
accelerate
datasets
sae-lens
```

Sketch: `blackbox.py` loops over questions with the `openai` client (it reads OPENAI_API_KEY and OPENAI_BASE_URL from the environment nrp sets), asks for an answer letter plus a 0-100 confidence, retries with exponential backoff on HTTP 429/5xx, and appends one JSON line per question to /data/blackbox/<model>.jsonl.

## README blurb

Interpretability research often runs the same analysis over a grid of models, benchmarks and layers. nrp-mcp turns a script that reads its task number into a GPU sweep on standard Nautilus GPUs and explains CUDA out-of-memory failures in plain language. The same project can add a black-box baseline against the NRP-hosted LLM endpoint without putting any token in a job spec. Results come back to the laptop and everything is cleaned up with two confirmations.
