# 01. Screening tens of millions of molecules for honey bee repellents

**Field:** Chemical ecology, insect olfaction, machine learning | **UCR connection:** Modeled on "Machine learning helps identify chemicals that repel honey bees from pesticides" (UCR News, Sept. 24, 2026, https://news.ucr.edu/articles/2026/09/24/machine-learning-helps-identify-chemicals-repel-honey-bees-pesticides) and the eLife paper "Machine learning of honey bee olfactory behavior identifies repellent odorants in free-flying bees in the field" (https://elifesciences.org/articles/104831). The model screened more than 50 million compounds, found about 130 candidates, and all seven compounds tested in the field repelled foraging bees. | **Fit:** Good

## The science

Honey bees bring pesticide residues back to the hive when they forage on treated crops. One way to cut that exposure is to add an odor that keeps bees away. Bee olfaction is hard to model, though. The paper counts more than 170 odorant receptors, and almost nothing links a molecule's structure to whether bees avoid it. The UCR approach trains a model on known behavioral responses, tests its top predictions on real bees, feeds the results back in, and then scores a very large chemical space to find a short list worth testing in the lab and the field.

Training the model is cheap. Scoring is where the compute goes: tens of millions of structures, each scored by a model that may run several passes per molecule to estimate uncertainty. On a laptop that takes weeks. On a shared campus GPU queue it can wait days for enough GPUs. The work splits into fully independent chunks, which is the case Nautilus handles best.

## Who at UCR does this

The study was led by an insect olfaction lab in Molecular, Cell and Systems Biology, working with a honey bee lab in Entomology. The same pattern (small labeled set, large library to rank) fits UCR groups in chemical ecology, vector biology and computational chemistry. None of these groups use nrp-mcp today; this is an illustration.

## The data

- **Compound library.** PubChem publishes every compound's SMILES string in one file, https://ftp.ncbi.nlm.nih.gov/pubchem/Compound/Extras/CID-SMILES.gz (1.4 GB compressed, checked Oct. 2026; download notes at https://pubchem.ncbi.nlm.nih.gov/docs/downloads). A filter for small, volatile-like molecules cuts it down to a few tens of millions. ZINC-22 (https://zinc22.docking.org/, files at https://files.docking.org/zinc22/) is another source of purchasable compounds. Some ZINC-22 files are password-protected, and its license forbids redistributing large parts, so read the terms first. The eLife study screened the MolPort catalog, which requires a vendor download.
- **Model.** The lab's trained checkpoint, about 60 MB, and a small training table.
- **P1 check.** These are public chemical structures plus the lab's own behavior scores. None of it is sensitive, so it all qualifies as P1. One thing to watch: the UCR News story notes a patent application on the compounds. Unpublished hit lists you want to keep confidential belong on lab storage, not on a shared cluster.

## The conversation

**Researcher:** My model is in ~/proj/bee-screen. Score every odorant-like molecule in PubChem and give me the top 1,000. Split it into chunks on GPUs.

**Assistant:** First I'll make storage and pull the library into the cluster, so the 1.4 GB file never goes through your laptop. [nrp_plan goal=volume size=200Gi data_is_p1=true] Plan: a 200 GiB shared CephFS volume `bee-screen-data`. Then [nrp_plan goal=pull urls=[.../CID-SMILES.gz] data_volume=bee-screen-data subdir=library data_is_p1=true] Plan: a download Job (curl, 1 CPU, 2 GiB, retries each file 5 times, 12 h limit). Run both?

**Researcher:** Yes.

**Assistant:** [nrp_run] x2. Both done. The library is at /data/library. Your 60 MB checkpoint is too big for the code ConfigMap (200 KB per file), so I'll upload it: [nrp_data up path=model/ volume=bee-screen-data data_is_p1=true]. Next, a CPU Job that filters to odorant-like molecules and writes shards of 250,000: [nrp_plan goal=job command="python prep.py" data_volume=bee-screen-data cpu=2 memory=8Gi]. That run left 51.2 million molecules in 205 shards.

Before the GPU sweep, one note. Your `score.py` reads SMILES directly into the fine-tuned chemical language model, so a GPU does nearly all the work. If you rerun the paper's original pipeline (3D conformers, thousands of descriptors, SVM and boosted-tree ensemble), that part is CPU-only. It should run as a CPU sweep, because idle GPUs get accounts flagged.

[nrp_plan goal=sweep project=~/proj/bee-screen count=205 parallel=20 gpu=1 data_volume=bee-screen-data] This plan is **refused**. R14: 20 GPUs at the same time is over your cap of 4. Run fewer GPU tasks at once (lower the sweep's parallel), or raise `gpus_per_run` in ~/.config/nrp-mcp/config.yaml and restart the nrp server in your client. The 205 shards are fine: 20 pods at once is under `pods_per_run` (50), and 205 tasks is far under `tasks_per_run` (10,000). Two options. With parallel=4, all 205 shards still run, 4 at a time: about 52 waves of 10-15 minutes, or 9 to 13 hours, which fits the 24 h limit. Or raise `gpus_per_run` to 20 on purpose for a 2 to 3 hour run.

**Researcher:** Done, gpus_per_run is set to 20.

**Assistant:** [nrp_plan goal=sweep count=205 parallel=20 gpu=1 ...] Plan: an Indexed Job of 205 tasks, 20 at a time. Each task gets 1 standard GPU, 4 CPU, 16 GiB, an 8 GiB /dev/shm, and image pytorch/pytorch with requirements.txt installed at start. Each task reads its shard number from $JOB_COMPLETION_INDEX (0..204). Limits equal requests (required above 100 pods in a run), and there's a 24 h limit for the whole sweep. Run it?

**Researcher:** Go.

**Assistant:** [nrp_run] Started. Forty minutes in: [nrp_watch] 61 of 205 done, but 6 tasks failed with exit 1. The last log lines show `torch.OutOfMemoryError: CUDA out of memory`. All 6 ran on 11 GB cards. Nautilus mixes GPU models, and your batch size of 4096 fits a 16 GB A4000 but not an 11 GB card. Fix: set the batch size from `torch.cuda.get_device_properties(0).total_memory`. After you fix it, I'll run only the missing shards: `score.py` writes `missing.txt`, and a 6-task sweep maps its index onto that list.

**Researcher:** Fixed. Rerun the missing ones when the main sweep ends.

**Assistant:** [nrp_watch] 199 of 205 succeeded. [nrp_plan goal=sweep count=6 parallel=6 gpu=1 command="python score.py --from-list missing.txt"] [nrp_run] All 6 done in 14 minutes. [nrp_plan goal=job command="python merge_top.py --k 1000"] [nrp_run] Then [nrp_data down path=results/top1000.csv]. The file is in ~/proj/bee-screen/results: 1,000 molecules with mean score, uncertainty, and nearest training neighbor. Clean up? [nrp_cleanup] lists 5 runs: the two sweeps, the prep, merge and pull Jobs. The volume is kept unless you name its run.

**Researcher:** Delete the runs and keep the volume for the next round.

**Assistant:** [nrp_cleanup plan_id=... confirm_token=...] Deleted. The volume stays. It is purged after 6 months without access, so keep the master copy elsewhere.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | volume | PVC, rook-cephfs-central, RWX | 200 GiB |
| Library download | pull | Job (curlimages/curl) | 1 CPU, 2 GiB, about 15 min |
| Model upload | nrp_data up | helper pod, kubectl cp | 60 MB |
| Filter and shard | job | Job (python:3.12-slim) | 2 CPU, 8 GiB, about 1 h |
| Scoring | sweep | Indexed Job, 205 tasks, 20 parallel | 1 GPU, 4 CPU, 16 GiB each; 10-15 min per task |
| Rerun of failed shards | sweep | Indexed Job, 6 tasks | same |
| Merge and rank | job | Job | 2 CPU, 8 GiB, minutes |

## Compute and cost estimate

Assumption: the model scores about 400 molecules per second on an RTX A4000-class GPU, with 10 Monte Carlo dropout passes for uncertainty. At that rate 51 million molecules need about 35 GPU-hours. With 20 GPUs at a time, the scoring takes roughly 2 to 3 hours of wall-clock time, including image pulls; at the default 4 at a time it takes about 9 to 13 hours. The prep and merge steps add about an hour. Nautilus costs the user nothing.

For comparison: one laptop GPU, one to two days if the model fits. The HPCC can do 35 GPU-hours, but 20 GPUs at once depends on the queue. Commercial cloud T4/L4-class GPUs on demand cost roughly $25-$40 before storage and setup. The money is small anywhere; Nautilus saves waiting time and setup.

## Limits and honest caveats

- **The paper's original pipeline is CPU work.** 3D conformers, force-field optimization and thousands of descriptors per molecule are CPU-bound. For 50 million molecules that is roughly 1,000-3,000 core-hours. Run it as a CPU sweep (gpu=0). Also note that the descriptor software used in the paper (alvaDesc) is commercially licensed, so check the license before putting it in a container image.
- **Heterogeneous GPUs.** The card type depends on what is free, from about 11 GB up to 48 GB, so size batches from the device you get. Premium A100/H100 cards have quota 0 for UCR. The `opportunistic` priority can use them, but tasks can be preempted at any time.
- **The GPU cap counts GPUs in use at the same time.** A sweep's `parallel` is what has to fit under `gpus_per_run` (default 4); the total task count can stay. Raising the cap is a deliberate step. Nautilus is shared, so 20 GPUs at a time is courteous and 100 is not.
- **Volumes are not archives,** and CephFS is slow with millions of small files; keep a few hundred compressed shards.
- **Confidential chemistry.** Hit lists tied to a pending patent should not sit on a shared national cluster longer than needed.

## Starter kit

`requirements.txt`
```
rdkit
transformers
pandas
pyarrow
```

`score.py` (sketch)
```python
import os, sys, pandas as pd, torch
from model import load_model, encode   # lab code

shards = sorted(os.listdir("/data/shards"))
if "--from-list" in sys.argv:          # rerun: index into missing.txt
    shards = open("/data/missing.txt").read().split()
idx = int(os.environ["JOB_COMPLETION_INDEX"])
name = shards[idx]
out = f"/data/scores/{name}.parquet"
if os.path.exists(out):
    sys.exit(0)                        # already done
mem_gb = torch.cuda.get_device_properties(0).total_memory / 1e9
batch = 4096 if mem_gb > 15 else 1536
model = load_model("/data/model/ckpt.pt").cuda().eval()
df = pd.read_parquet(f"/data/shards/{name}")
scores = []
with torch.no_grad():
    for i in range(0, len(df), batch):
        x = encode(df.smiles[i:i + batch]).cuda()
        scores.append(model.mc_predict(x, passes=10).cpu())
df["mean"], df["sd"] = torch.cat(scores).T.numpy()
df.to_parquet(out)
```

`prep.py` streams CID-SMILES.gz and keeps neutral molecules under roughly 300 Da that have no metals. It writes 250,000-row Parquet shards to /data/shards. `merge_top.py` reads all scores, writes `missing.txt` for any shard without output, and keeps the top k.

## README blurb

Virtual screening with a trained model: pull a public compound library such as PubChem into a Nautilus volume, cut it into shards, and score every shard as one GPU task in an Indexed Job. nrp-mcp sizes the tasks and refuses plans that would use more GPUs at once than your cap, offering a lower parallel or a deliberate cap raise. It explains failures such as GPU out-of-memory on smaller cards, and brings back only the ranked hit list. CPU-heavy featurization belongs in a CPU sweep, and the plan tells you so.
