# 17. Matching millions of public MS/MS spectra against open libraries

**Field:** Metabolomics, natural products, environmental and microbiome chemistry | **UCR connection:** UCR biochemists and computer scientists build open metabolomics workflows and molecular networking tools ("New data science tool greatly speeds up molecular analysis of our environment", UCR News, Sept 2024, https://news.ucr.edu/articles/2024/09/20/new-data-science-tool-greatly-speeds-molecular-analysis-our-environment); UCR co-led Multiplexed Chemical Metabolomics (MCheM), Nature Communications 2025 (https://doi.org/10.1038/s41467-025-61240-z); the IIGB Metabolomics Core Facility (https://metabolomics.iigb.ucr.edu/); and a campus-wide hiring initiative in metabolomics and systems biology (a 2026 UCR faculty search, posting JPF02151, now closed) | **Fit:** Excellent

## The science

Untargeted LC-MS/MS measures thousands of small molecules in a sample, but most of them stay unnamed. The MCheM paper notes that on average fewer than 10% of features are confidently annotated. Two things raise that number. One is matching each spectrum against reference libraries, with cosine scores or learned similarity measures such as MS2DeepScore. The other is molecular networking: linking similar spectra across many samples and studies, so a known compound helps identify its unknown neighbors. Doing this across hundreds of public studies at once, not one study at a time, shows where a molecule occurs (in which plants, microbes, soils or seawater) and gives the unknowns more context.

The bottleneck is scale. One public study can hold millions of spectra, and the GNPS libraries list about 2.9 million reference spectra. Re-scoring 300 studies, embedding every spectrum and building a cross-study network is a few hundred independent CPU tasks plus some GPU training: too much for a laptop.

## Who at UCR does this

- **Biochemistry and Computer Science and Engineering.** The UCR News story describes a biochemistry group that led a Nature Protocols workflow for large metabolomics datasets and a computer science and engineering group that develops molecular networking software. Both groups are authors on the MCheM paper.
- **Institute for Integrative Genome Biology (IIGB).** The Metabolomics Core Facility runs targeted and untargeted LC-MS and GC-MS and offers bioinformatics support for campus and industry projects.
- **New faculty lines.** The recruitment for an Assistant Professor in Computational and/or Analytical Metabolomics describes "a strategic, campus-wide initiative to expand UCR's research capacity in metabolomics and systems biology", including machine learning and multi-omics integration, and a planned Master's program in metabolomics.

None of these groups use nrp-mcp today; this is an illustration.

## The data

| Source | What | Access | Size |
|---|---|---|---|
| GNPS spectral libraries, https://external.gnps2.org/gnpslibrary | Community and imported reference MS/MS spectra (MGF, MSP, JSON) | Public download | about 2.9 million spectra listed; the main MGF is a few GB |
| MassIVE, https://massive.ucsd.edu/ | Public LC-MS/MS datasets (MSV accessions), many tagged for GNPS | Anonymous FTP for public datasets | from MB to hundreds of GB per dataset |
| GNPS2 dataset explorer, https://explorer.gnps2.org/ | Index of metabolomics datasets across MassIVE, MetaboLights and Metabolomics Workbench | Web | metadata |
| MetaboLights, https://www.ebi.ac.uk/metabolights/ | EMBL-EBI metabolomics studies | HTTPS file tree at https://ftp.ebi.ac.uk/pub/databases/metabolights/studies/public/ | varies |

**P1 check.** Public deposited datasets are P1; many carry a CC0 license on the dataset page (record each one). Public, already de-identified human-sample datasets (plasma, stool) are fine to reanalyze. **Clinical metabolomics with patient identifiers, data under a data use agreement, or confidential industry samples are not P1** and go to the UCR Secure Enclave or an Ursa Major Tier 2 secure project instead.

## The conversation

> **Researcher:** I have a list of 300 public MassIVE and MetaboLights studies in `studies.csv`, already in mzML or mzXML. For each one, match every MS/MS spectrum to the GNPS library, compute MS2DeepScore embeddings, and save the hits. Then I want a notebook to build the network.

> **Assistant:** First a shared volume and the library. [nrp_plan goal=volume name=metab-data size=500Gi] creates a CephFS volume (rook-cephfs-central, ReadWriteMany) mounted at /data. Then [nrp_plan goal=pull data=https://external.gnps2.org/gnpslibrary/ALL_GNPS.mgf] downloads the library inside the cluster. Pulling needs you to confirm the data is P1. It is a public library, so is that a yes?

> **Researcher:** Yes, public data. Go ahead with both.

> **Assistant:** [nrp_run] volume Bound; [nrp_run] pull finished, 1 file on /data/library. Now the sweep. [nrp_plan goal=sweep count=300 parallel=50 cpu=4 memory=16Gi gpu=0]. Plan summary: Indexed Job, 300 tasks, 50 at a time, image python:3.12-slim with your requirements.txt installed at start, /data mounted, 24 h time limit for the whole sweep (300 tasks at 50 at a time is 6 waves of 30-90 minutes, so up to about 9 h), 2 retries. Each task reads `$JOB_COMPLETION_INDEX` (0-299), picks that row of studies.csv and downloads its files over FTP or HTTPS. No GPU: matchms and MS2DeepScore inference run fine on CPU. Note that requests above 1 CPU / 2 GiB are checked for usage, so keep the 4 cores busy. Confirm?

> **Researcher:** Confirm.

> **Assistant:** [nrp_run] Started run metab-sweep. Run card written to .nrp/runs/. Six hours later, [nrp_watch]: 281 succeeded, 16 running, 3 failed. Tasks 47, 112 and 260 were OOMKilled: they ran out of memory at the 16Gi limit. They are the three largest studies (over 4 million spectra each). Fix: raise memory by 50% and rerun only those.

> **Researcher:** Rerun just those three with more memory.

> **Assistant:** I put the three indices in `rerun.txt`. [nrp_plan goal=sweep count=3 parallel=3 cpu=4 memory=24Gi command="python task.py --index-file rerun.txt"]. Confirm? ... [nrp_run], then [nrp_watch]: all 3 succeeded. 300 per-study hit tables and embedding files are now on /data/results.

> **Researcher:** I would also like to fine-tune MS2DeepScore on the latest library, on a GPU.

> **Assistant:** [nrp_plan goal=job gpu=1 cpu=8 memory=32Gi command="python finetune.py"] picks pytorch/pytorch, one standard GPU (A4000/A5000/A40 class), no premium GPU. The script saves a checkpoint each epoch to /data/models, so a restart loses little. Confirm? ... [nrp_run]. Then [nrp_plan goal=session cpu=4 memory=16Gi] gives a private Jupyter on the same volume; [nrp_session] prints the port-forward command and a local URL with its sign-in token.

> **Researcher:** Network built. Bring back the summary tables and clean up, but keep the volume for a month.

> **Assistant:** [nrp_data down path=/data/summary] copied 1.2 GB of tables and the GraphML file to `results/`. [nrp_cleanup] lists the sweep, the rerun, the fine-tune job and the session; it deletes them after your second yes. The volume stays (purged after 6 months without use).

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Shared storage | `volume` | PVC, rook-cephfs-central, RWX | 500Gi |
| Library download | `pull` | Job | 1 CPU, 2Gi, minutes |
| Per-study matching and embedding | `sweep` | Indexed Job | 300 tasks, 50 parallel, 4 CPU / 16Gi each, 0 GPU, about 30-90 min each |
| Rerun of large studies | `sweep` | Indexed Job | 3 tasks, 4 CPU / 24Gi |
| Model fine-tuning | `job` | Job | 1 GPU, 8 CPU / 32Gi, 6-12 h |
| Network analysis | `session` | Deployment (1 replica) + port-forward | 4 CPU / 16Gi, no GPU |
| Results | `nrp_data down` | helper pod | - |

## Compute and cost estimate

Assumptions: 300 studies, about 150 million MS/MS spectra in total, 1 hour of 4-core work per study on average, including download. That is about 1,200 CPU-hours. At 50 tasks in parallel the sweep finishes in about 6-8 hours of wall clock. Fine-tuning adds 6-12 GPU-hours on one standard GPU.

- **Laptop (8 cores):** about 150 hours nonstop, plus terabytes of downloads. Not practical.
- **UCR HPCC:** a good fit for the CPU part with a lab share ($1,000/lab/year); queue time varies.
- **Commercial cloud:** roughly tens of US dollars of compute on demand, plus storage and setup.
- **Nautilus:** free to the user; data stays next to the compute and only summaries come back.

## Limits and honest caveats

- **Vendor raw files.** Many datasets hold vendor formats (.raw, .d). Converting them needs ProteoWizard msconvert, and some vendor readers are Windows-only. Start with studies that already have mzML or mzXML.
- **SIRIUS and CSI:FingerID.** SIRIUS is AGPL-3.0. Its structure search (CSI:FingerID), compound classes (CANOPUS) and MSNovelist run as web services hosted by FSU Jena. They are free for academic use but need a SIRIUS account login, and every query goes to their servers. nrp does not manage that login. If you use it, store the credentials in a Kubernetes Secret (never in the spec), send only a prioritized shortlist, and respect the service's capacity. Fragmentation trees and isotope analysis run locally without a license. The open models matchms, Spec2Vec and MS2DeepScore have no such limit (matchms and MS2DeepScore are Apache-2.0).
- **FTP.** `goal=pull` handles http(s) only. MassIVE serves public files by FTP, so each task downloads its own study. An FTP outage shows up as failed tasks, which nrp_watch names.
- **Large networks.** All-against-all over 150 million spectra needs approximate nearest-neighbor search on the embeddings, not pairwise cosine.
- **Storage.** CephFS measured about 86 MiB/s per writer in a UCR test; write one Parquet file per study. Volumes are not an archive, and NRP S3 for moving terabytes out is not yet tested by UCR RC.
- **Web viewers** are possible with `goal=web`, but NRP removes Deployments after 2 weeks, and at UCR publishing needs RC's OK first.

## Starter kit

```
studies.csv        # accession, url_prefix, format, license
task.py            # one study per sweep task
finetune.py        # optional GPU fine-tuning
network.ipynb      # session notebook
requirements.txt
```

`task.py` (sketch):

```python
import os, csv, pathlib
from matchms.importing import load_from_mgf, load_from_mzml
from matchms.filtering import default_filters, normalize_intensities
from matchms import calculate_scores
from matchms.similarity import CosineGreedy

i = int(os.environ["JOB_COMPLETION_INDEX"])
row = list(csv.DictReader(open("studies.csv")))[i]
out = pathlib.Path("/data/results") / row["accession"]
if (out / "done").exists():
    raise SystemExit(0)                    # resumable
files = download_study(row, "/tmp/study")  # FTP/HTTPS helper
lib = [normalize_intensities(default_filters(s))
       for s in load_from_mgf("/data/library/ALL_GNPS.mgf")]
query = [normalize_intensities(default_filters(s))
         for f in files for s in load_from_mzml(f)]
scores = calculate_scores(lib, query, CosineGreedy(tolerance=0.01))
write_top_hits(scores, out / "hits.parquet", min_score=0.7)
write_embeddings(query, "/data/models/ms2ds.pt", out / "emb.parquet")
(out / "done").touch()
```

`requirements.txt`:

```
matchms
ms2deepscore
pyarrow
pandas
```

## README blurb

Reanalyze hundreds of public metabolomics studies at once: nrp-mcp downloads a public spectral library into a shared volume, runs one sweep task per study to match and embed every MS/MS spectrum with open tools such as matchms and MS2DeepScore, reruns only the tasks that ran out of memory, and opens a private notebook on the same volume to build the molecular network. Only summary tables travel back to the laptop. Public deposited data only; clinical or identifiable samples belong in a secure environment.
