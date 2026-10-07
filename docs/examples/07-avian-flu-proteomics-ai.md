# 07. AI peptide identification for avian influenza surveillance

**Field:** Virology, proteomics, animal health | **UCR connection:** a virology laboratory in Microbiology and Plant Pathology received a $1.8 million USDA APHIS grant to build a surveillance tool for high pathogenicity avian influenza (HPAI) on poultry farms that combines AI and proteomics (UCR News, June 2026, https://news.ucr.edu/articles/2026/06/23/preventing-next-egg-crisis-smarter-avian-flu-detection) | **Fit:** Good

## The science

Tests that detect viral RNA are sensitive, but they cannot always tell an active infection from leftover fragments or contamination. The UCR project looks instead at proteins. A replicating virus uses its host's cell machinery and carries host proteins along with it, so viral and host peptides in a sample can show that a virus is active and hint at which species it recently passed through. Doing that across "hundreds of millions of different proteins", as the UCR News story puts it, means searching mass-spectrometry data against a far broader space of organisms than a normal proteomics study, with independent cross-checks to guard against false hits.

Compute is the bottleneck twice over. Deep-learning de novo sequencing (reading a peptide straight from a spectrum, with no database) needs a GPU, at hundreds of spectra per second against runs of up to millions of spectra. A database search across chicken, duck, cattle, wild-bird and influenza proteomes at once is CPU- and memory-heavy, because the search space grows with every species. Benchmarking on public data before farm samples arrive is many independent per-file jobs.

## Who at UCR does this

Virology and host-pathogen groups in the College of Natural and Agricultural Sciences, including the Department of Microbiology and Plant Pathology, plus mass-spectrometry and proteomics expertise across campus. The project's principal investigator is in that department. None of these groups use nrp-mcp today; this is an illustration.

## The data

Public, P1 benchmark data (sizes from the PRIDE API, October 2026):

- PRIDE / ProteomeXchange (https://www.ebi.ac.uk/pride/archive/, https://proteomecentral.proteomexchange.org/):
  - PXD010358, H5N1 HPAI infection of chicken lung, 15 mzXML files, 40.7 GB, CC0 (https://www.ebi.ac.uk/pride/archive/projects/PXD010358).
  - PXD015475, influenza A infection proteome with human and chicken cells, 5 Thermo RAW files, 12.7 GB, CC0 (https://www.ebi.ac.uk/pride/archive/projects/PXD015475).
  - PXD067000, plasma of HPAI-resistant and -susceptible chickens, 19 RAW files, 9.6 GB, CC0, acquired as DIA (https://www.ebi.ac.uk/pride/archive/projects/PXD067000).
- Protein databases: UniProt reference proteomes, for example chicken UP000000539 (https://www.uniprot.org/proteomes/UP000000539), and influenza protein sequences from NCBI Virus (https://www.ncbi.nlm.nih.gov/labs/virus/vssi/), which replaced the older NCBI Influenza Virus Resource.
- Tools: Casanovo, transformer-based de novo sequencing, Apache-2.0, with pretrained weights on its GitHub releases (https://github.com/Noble-Lab/casanovo); Sage, a fast open-source database search engine, MIT (https://github.com/lazear/sage); ThermoRawFileParser for converting RAW files (https://github.com/compomics/ThermoRawFileParser).

P1 check and what stays off Nautilus:

- GISAID (https://gisaid.org/) holds many recent HPAI sequences, but access requires agreeing to its terms of use, which restrict redistribution and require acknowledging the data contributors. A GISAID-derived FASTA should not sit on a shared cluster volume unless your reading of the terms allows it. Use GenBank / NCBI Virus sequences on Nautilus.
- Farm-level surveillance data is not P1: sample locations, premises IDs, flock status, owners, and anything shared under a USDA or state agreement are business-sensitive and may be contractually restricted. Keep them off Nautilus. Talk to UCR Research Computing about a Tier 2 secure project on Ursa Major, or the UCR Secure Enclave (NIST 800-171) if the award marks any data as CUI.
- Any human clinical specimen (for example, spillover cases) is HIPAA territory and never goes to Nautilus. Public cell-line data such as PXD015475 is fine.

## The conversation

**Researcher:** I want to benchmark a pipeline on public HPAI proteomics. Download PXD010358 and PXD015475, run Casanovo de novo on every file on GPUs, run a Sage database search on CPUs against chicken, duck and influenza A as the cross-check, then compare.

**Assistant:** [nrp_status] Namespace `hpai-proteomics`, signed in. GPU quota: standard GPUs available; A100 and H100 quota 0. Your cap is 4 GPUs at the same time. Both datasets are CC0 on PRIDE. [nrp_plan goal=volume size=150Gi data_is_p1=true] then [nrp_plan goal=pull data_volume=hpai-ms subdir=raw urls=[20 https links on ftp.pride.ebi.ac.uk] data_is_p1=true] Plan: "A download Job pulling 20 file(s) into hpai-ms:/raw", inside the cluster, so 53 GB never touches your laptop. Approve?

**Researcher:** Yes.

**Assistant:** [nrp_run ...] [nrp_run ...] [nrp_watch target=run-1b07] Done in 41 minutes. Next, the five Thermo RAW files need converting to mzML. [nrp_plan goal=sweep count=5 parallel=5 cpu=2 memory=8Gi image=quay.io/biocontainers/thermorawfileparser:<tag> data_volume=hpai-ms] A 5-task CPU sweep, no GPU. Approve? [nrp_run ...] Done in 20 minutes.

**Researcher:** Now Casanovo.

**Assistant:** One file per task makes 20 GPU tasks; your `denovo.py` picks its file with `$JOB_COMPLETION_INDEX`. Your cap counts GPUs in use at the same time, so running all 20 at once would be refused ("20 GPUs at the same time is over your cap of 4"). At parallel=4 all 20 still run, 4 at a time, in 5 waves. [nrp_plan goal=sweep count=20 parallel=4 gpu=1 cpu=4 memory=24Gi hours=24 data_volume=hpai-ms] Plan summary: "A sweep of 20 tasks, 1 GPU each (any standard GPU), 4 CPU and 24 GiB, limits equal to requests. Indexed Job: 20 tasks, 4 at a time. Adds an 8 GiB /dev/shm so PyTorch data loaders do not crash. Image pytorch/pytorch; requirements installed at start." The 24-hour limit covers all 5 waves, not each task; at about an hour per file that leaves room. One note: PXD010358 was acquired on a Q-TOF, and Casanovo's released weights were trained mostly on Orbitrap data, so expect lower accuracy on those files. That is something your benchmark should measure. Approve?

**Researcher:** Approve.

**Assistant:** [nrp_run ...] Ninety minutes in: [nrp_watch target=run-5c3e] "Failed: backoff limit reached." Task 5 exited with an error, and the last 40 log lines end in `torch.OutOfMemoryError: CUDA out of memory`. It landed on a 16 GB GPU, and its file has unusually long spectra. The Job retried it twice with the same result, and because that used up the Job's retry budget, Kubernetes marked the whole Job failed and stopped the other running tasks too. Nothing finished is lost: 5 files already have their `.mztab`, and your script skips those. The fix is to lower Casanovo's `predict_batch_size` in `casanovo.yaml` from 1024 to 256. I will clean up the failed run and plan the same sweep with the new config; finished files exit in seconds. [nrp_cleanup run=run-5c3e] (two-step) [nrp_plan ...] [nrp_run ...] Four hours later: "20 of 20 succeeded", 20 mzTab files.

**Researcher:** Were the GPUs actually busy? I don't want the account flagged.

**Assistant:** The logs show about 300 spectra per second per task, with data loading overlapped with inference. The NRP's monitoring dashboards show per-pod GPU use; check that yours stayed above the 40% guideline. If a GPU sits idle while a large mzXML file is parsed, convert to MGF first or raise the number of data-loader workers. Now the cross-check. [nrp_plan goal=sweep count=20 parallel=20 cpu=8 memory=32Gi image=ghcr.io/lazear/sage:<tag> data_volume=hpai-ms] A 20-task CPU sweep, no GPU, running Sage against `db/chicken_duck_iav.fasta` with decoys, 1% FDR. Approve? [nrp_run ...] Done in 2 h 40 min.

**Researcher:** Bring the results back and clean up.

**Assistant:** [nrp_data down volume=hpai-ms path=/results/] and the 20 mzTab files (about 900 MB). Your comparison notebook can run locally, or I can open a private Jupyter on the volume with [nrp_plan goal=session]. Then [nrp_cleanup] lists the five runs and the volume; deleting needs your second confirmation. Keep the volume if PXD067000 is next. Note that it is DIA data, which Casanovo's DDA models do not handle; it needs a DIA tool instead.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PVC (CephFS, RWX) | 150 GiB |
| Download | nrp_plan goal=pull | Job | 20 files, 53 GB, ~40 min |
| RAW to mzML | nrp_plan goal=sweep | Indexed Job | 5 tasks x 2 CPU / 8 GiB |
| De novo (Casanovo) | nrp_plan goal=sweep | Indexed Job + ConfigMap | 20 tasks (4 at a time), 1 GPU / 4 CPU / 24 GiB each, ~5 h |
| Database search (Sage) | nrp_plan goal=sweep | Indexed Job | 20 tasks x 8 CPU / 32 GiB, ~2.5 h |
| Results | nrp_data down | helper pod + kubectl cp | ~1 GB |
| Cleanup | nrp_cleanup | deletes own labelled objects | two-step |

## Compute and cost estimate

Assumptions: about 1.5 million MS/MS spectra across the 20 files; Casanovo beam search at roughly 250 to 350 spectra per second on an A40/A6000-class GPU, so about 1.5 GPU-hours of inference plus file parsing. Real runs vary by file, which brings it to about 15 to 20 GPU-hours, or about 5 hours of wall-clock at 4 GPUs. Sage is roughly 50 CPU-hours for a multi-species database with open settings. Nautilus is free to the user.

Comparison: a laptop without a CUDA GPU needs days for the de novo step; one workstation GPU, about a day. The UCR HPCC's GPU partitions fit well if the lab has an account. On a commercial cloud, 20 GPU-hours on an L4/A10-class instance costs roughly $15 to $40 plus storage and egress. Nautilus earns its keep as the pipeline grows to dozens of public datasets and repeated benchmark runs.

## Limits and honest caveats

- Public data only. Farm, premises and regulatory surveillance data, GISAID-derived sequences (unless the terms allow it), and any human clinical material stay off Nautilus. RC can point you to Ursa Major Tier 2 or the Secure Enclave.
- GPU use is policed: keep utilization above 40%, and do not request GPUs for the CPU database search.
- The 4-GPU cap counts GPUs in use at the same time, so a GPU sweep runs one task per file at parallel=4 (the total count can stay) and sizes `hours` for all the waves, since the Job time limit covers the whole sweep. Raise `gpus_per_run` only on purpose, and be courteous on a shared cluster. Premium GPUs (A100/H100) are quota 0 at UCR; `opportunistic` priority can reach them but can be preempted at any time, so only use it with per-file outputs that are safe to rerun.
- Fine-tuning Casanovo for a new instrument type is a multi-hour GPU job: it fits, with checkpointing and likely an nrp_build image.
- DIA datasets need different tools from DDA de novo sequencing.
- A de novo hit to a host peptide is a hypothesis, not proof of a species jump; it needs independent confirmation.
- Volumes are purged after 6 months of inactivity. Deposit derived results in PRIDE or Zenodo.

## Starter kit

Sketch, not tested code.

`denovo.py`
```python
import os, pathlib, subprocess
i = int(os.environ["JOB_COMPLETION_INDEX"])           # 0..19, one file per task
files = sorted(p for p in pathlib.Path("/data/raw").iterdir()
               if p.suffix.lower() in {".mzml", ".mzxml", ".mgf"})
weights = "/data/models/casanovo_orbitrap_v5-2-0.ckpt"
outdir = pathlib.Path("/data/denovo"); outdir.mkdir(parents=True, exist_ok=True)
spec = files[i]
if not (outdir / (spec.stem + ".mztab")).exists():     # done; reruns are cheap
    subprocess.run(["casanovo", "sequence", str(spec), "--model", weights,
                    "--config", "casanovo.yaml", "--output_dir", str(outdir),
                    "--output_root", spec.stem], check=True)
```

`casanovo.yaml` (excerpt)
```yaml
predict_batch_size: 256   # lowered after a CUDA OOM on a 16 GB GPU
n_beams: 5
precursor_mass_tol: 50    # ppm; tune per instrument
```

`requirements.txt`
```
casanovo>=5.2
```

`sage.json` (excerpt, CPU search)
```json
{"database": {"fasta": "/data/db/chicken_duck_iav.fasta", "generate_decoys": true,
  "enzyme": {"missed_cleavages": 2}},
 "precursor_tol": {"ppm": [-20, 20]}, "fragment_tol": {"ppm": [-20, 20]},
 "output_directory": "/data/sage"}
```

## README blurb

Benchmark AI peptide identification on public mass-spectrometry data without moving terabytes through a laptop. Public PRIDE datasets are pulled straight into a cluster volume, a deep-learning de novo sequencer runs one file per GPU task, a few at a time within your GPU cap, and a CPU database search runs alongside it as an independent check. A GPU out-of-memory error is diagnosed in plain language and only the failed file is redone. Restricted data such as farm surveillance records, controlled sequence databases or clinical samples stays on secure systems.
