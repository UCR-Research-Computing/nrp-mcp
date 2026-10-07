# 13. Open Neuroimaging Pipelines: fMRIPrep and MRIQC on OpenNeuro Datasets

**Field:** Cognitive neuroscience, neuroimaging methods | **UCR connection:** UCR cognitive neuroscience and neuroimaging-in-aging groups in Psychology and the Neuroscience Graduate Program, supported by the UCR Center for Advanced Neuroimaging (3 T Siemens Prisma, https://can.ucr.edu/); modeled on methods-development and replication work that uses public data | **Fit:** Good

## The science

Before a lab spends scanner time on a new aging study, it can test an analysis on public data: does a new denoising strategy change task effects, does a published age difference in functional connectivity replicate, how much do preprocessing choices move the result? Answering honestly means preprocessing every subject the same way with standard, versioned pipelines: fMRIPrep for anatomical and functional preprocessing, MRIQC for image quality metrics.

Compute is the bottleneck: fMRIPrep with FreeSurfer surface reconstruction takes several hours per subject on 8 cores and wants 16-32 GB of memory, and a methods study reruns the same 80-300 subjects with different versions or options. On a laptop that is weeks per pass; on a cluster it is a per-subject sweep that finishes in about a day.

## Who at UCR does this

The Department of Psychology (https://psychology.ucr.edu/) and the interdepartmental Neuroscience Graduate Program (https://neuroscience.ucr.edu/) include cognitive neuroscience groups studying memory, attention and emotion, including how these change with age. The Center for Advanced Neuroimaging (CAN) is UCR's multidisciplinary MRI center (3 T Siemens Prisma, MRI-compatible EEG), with faculty from psychology, neurology, MRI physics, engineering and computer science.

None of these groups use nrp-mcp today; this is an illustration.

## The data

- **OpenNeuro** (https://openneuro.org): a public archive of BIDS-formatted brain imaging data, shared under CC0 and mirrored on the AWS Open Data registry (https://registry.opendata.aws/openneuro/, bucket `s3://openneuro.org`, readable anonymously over HTTPS).
- Example dataset: **ds000201**, "The Stockholm Sleepy Brain Study: Effects of Sleep Deprivation on Cognitive and Emotional Processing in Young and Old" (https://openneuro.org/datasets/ds000201): 86 subjects, about 172 GB, young and older adults. Another option is ds000030 (UCLA CNP LA5c, 272 subjects, about 85 GB, CC0).
- Containers: `nipreps/fmriprep` and `nipreps/mriqc` on Docker Hub (fMRIPrep image about 2.5 GB). Pin a version tag (for example `nipreps/fmriprep:25.2.6`) so reruns are reproducible.

**P1 check:** OpenNeuro data is de-identified (defaced) and released CC0, so it is P1 and allowed on Nautilus. Your own lab's MRI data is different. Scans collected under a UCR IRB protocol are human-subjects data governed by that protocol and are not P1: they must not go to Nautilus, even after defacing. Talk to UCR Research Computing about the right home: Ursa Major Tier 2 secure GCP projects, the UCR Secure Enclave for controlled data, or HPCC where your protocol and data classification allow it. Datasets under data use agreements (for example ABCD or UK Biobank) also stay off Nautilus.

## The conversation

**Researcher:** I want to run MRIQC and then fMRIPrep with FreeSurfer on all 86 subjects of OpenNeuro ds000201, for a denoising methods paper. Can Nautilus do that?

**Assistant:** Yes, it is public CC0 data, so it is fine on Nautilus. [nrp_status] You are in namespace `ucr-neuro-methods`, nothing running, no GPU needed for this. fMRIPrep also needs a FreeSurfer license file (free from https://surfer.nmr.mgh.harvard.edu/registration.html). Keep `license.txt` outside the project folder so it is never copied into the job spec.

**Researcher:** Done. The project folder has `fetch_subject.py`, `run_fmriprep.sh` and `subjects.txt`.

**Assistant:** First a shared volume for outputs, then the license. [nrp_plan goal=volume name=ds201 size=500Gi data_is_p1=true] Plan: one 500 GiB CephFS volume (ReadWriteMany), mounted at /data by jobs; purged after 6 months without use, so it is not an archive. Approve? **Researcher:** Yes. [nrp_run] Created. [nrp_data action=up volume=ds201 local=~/fs/license.txt path=/license data_is_p1=true] The license is at /data/license/license.txt.

**Researcher:** Start with a two-subject pilot. (An MRIQC sweep, 4 CPU / 8 GiB per subject, already ran the same way.)

**Assistant:** [nrp_plan goal=sweep count=2 parallel=2 cpu=8 memory=16Gi image=nipreps/fmriprep:25.2.6 data_volume=ds201 hours=24 command="bash run_fmriprep.sh"] Summary: an Indexed Job with 2 tasks, 8 CPU and 16 GiB each, limits equal to requests, no GPU. Each task picks its subject from `subjects.txt` via $SLURM_ARRAY_TASK_ID, downloads it into pod scratch, and writes derivatives to /data/derivatives. Warning R16: requests over 1 CPU / 2 GiB are checked for usage, and FreeSurfer is partly single-threaded, so CPU use will dip. Approve? **Researcher:** Yes. [nrp_run] Running; run card written to `.nrp/runs/`.

**Researcher:** How is it going? (next morning)

**Assistant:** [nrp_watch target=<run>] Task 0 succeeded in 7 h 40 min. Task 1 was OOMKilled: it ran out of memory at the 16 GiB limit during spatial normalization. Fix: raise memory by 50% and rerun. I will set 24 GiB and pass `--mem-mb 22000` so fMRIPrep stays under the limit (`--low-mem` is another option).

**Researcher:** Fine. Run all 86, as fast as is polite.

**Assistant:** 43 at a time is within the default cap of 50 pods at once, and the total of 86 tasks is well under the per-sweep task cap, so no config change is needed. The time limit covers the whole sweep, so for two waves of 8-10 h I will set 36 h. [nrp_plan goal=sweep count=86 parallel=43 cpu=8 memory=24Gi image=nipreps/fmriprep:25.2.6 data_volume=ds201 hours=36] Summary: 86 tasks, 43 at a time, 8 CPU / 24 GiB each, about 344 cores at peak, no GPU. Approve? **Researcher:** Yes. [nrp_run]

**Researcher:** Status?

**Assistant:** [nrp_watch target=<run> grep="fMRIPrep finished"] 84 succeeded, 2 failed with exit 1. Last log lines for both: "No BOLD images found". That points at the input, not the cluster: check whether those two subjects have functional runs in this snapshot, and if not, record them as exclusions in your methods. My script also copied each subject's HTML report and confounds TSVs to /data/summary. [nrp_data action=down volume=ds201 path=/summary local=./results] Reports and confounds are in `./results`; the full derivatives (about 200 GB) stay on the volume for your denoising jobs.

**Researcher:** Clean up the jobs but keep the volume.

**Assistant:** [nrp_cleanup] Step 1 lists your runs: the pilot, the full sweep, the MRIQC sweep and the volume run. I will remove only the three job runs. [nrp_cleanup run=<sweep-run>] (and the same for the other two) Each step 1 lists the Job and its ConfigMap. Confirm? **Researcher:** Yes. [nrp_cleanup plan_id=... confirm_token=...] Deleted; the ds201 volume is untouched.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Output storage | nrp_plan goal=volume | PersistentVolumeClaim (rook-cephfs-central, RWX) | 500 GiB |
| License file | nrp_data up | helper pod (kubectl cp), file on the volume | 1 file, under 1 KB |
| QC pass | nrp_plan goal=sweep (nipreps/mriqc) | Indexed Job + ConfigMap | 86 tasks, 4 CPU / 8 GiB, about 1 h each |
| Pilot | nrp_plan goal=sweep | Indexed Job + ConfigMap | 2 tasks, 8 CPU / 16 GiB |
| Full fMRIPrep | nrp_plan goal=sweep | Indexed Job + ConfigMap | 86 tasks, 43 parallel, 8 CPU / 24 GiB, 36 h limit |
| Results | nrp_data down | helper pod | HTML reports, confounds (a few GB) |
| Cleanup | nrp_cleanup | deletes the caller's Jobs, ConfigMaps | - |

## Compute and cost estimate

Assumptions: 8-10 h per subject at 8 cores with FreeSurfer, about 2 GB input and 2-3 GB derivatives per subject.

- fMRIPrep: 86 subjects x 9 h x 8 cores, about 6,200 core-hours. At 43 parallel, about 18-20 h wall-clock. MRIQC adds about 350 core-hours.
- GPU-hours: zero; neither pipeline uses a GPU.
- Nautilus: free.
- Laptop (8 cores, 32 GB, one subject at a time): about 32 days of continuous runtime per pass.
- HPCC: also a good fit (Slurm array, $1,000/lab/year), subject to queue time.
- Commercial cloud: 8 vCPU / 32 GB on demand at roughly $0.40/h is about $350 per pass plus storage; four passes, over $1,400.

## Limits and honest caveats

- **Data sensitivity is the main boundary.** Only public, de-identified data (OpenNeuro CC0) belongs here. Your own IRB-governed scans, and any dataset under a data use agreement, go to Ursa Major Tier 2, the Secure Enclave or HPCC as RC advises.
- **FreeSurfer license:** nrp-mcp has no option to mount an arbitrary Kubernetes Secret into a Job (it does so only for the LLM token). The volume route above works but is readable by your namespace; a Secret created with kubectl and mounted by hand is cleaner. Never put `license.txt` in the project folder, which is copied into a ConfigMap.
- **Scratch disk:** fMRIPrep's work directory can reach tens of GB per subject. The sketch uses the pod's local scratch (`/work`), which is fast but has no ephemeral-storage request, so a very full node could evict a task. Scratch on CephFS is safer but slow (about 86 MiB/s per writer in a UCR test).
- **Usage checks:** single-threaded FreeSurfer phases can pull average CPU toward the 20% floor of an 8-core request; 6 cores is a reasonable compromise.
- **Sweep time limit** covers all waves (cap 48 h unless raised).
- **Not an archive:** volumes are purged after 6 months idle.

## Starter kit

`subjects.txt` (one label per line, e.g. `9001`), plus these sketches:

```python
# fetch_subject.py - sketch: download one OpenNeuro subject over public HTTPS
import os, re, urllib.request
DS, BASE = "ds000201", "https://s3.amazonaws.com/openneuro.org"
sub = open("subjects.txt").read().split()[int(os.environ["SLURM_ARRAY_TASK_ID"])]
listing = urllib.request.urlopen(f"{BASE}?list-type=2&prefix={DS}/sub-{sub}/").read().decode()
keys = re.findall(r"<Key>([^<]+)</Key>", listing)  # one subject is well under 1000 keys
for k in keys + [f"{DS}/dataset_description.json", f"{DS}/participants.tsv"]:
    dest = "/work/bids/" + k.split("/", 1)[1]
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    urllib.request.urlretrieve(f"{BASE}/{k}", dest)
print(sub)
```

```bash
# run_fmriprep.sh - sketch
set -euo pipefail
SUB=$(python fetch_subject.py)
fmriprep /work/bids /data/derivatives/fmriprep participant \
  --participant-label "$SUB" -w /work/scratch \
  --fs-license-file /data/license/license.txt \
  --nthreads 8 --omp-nthreads 4 --mem-mb 22000 \
  --output-spaces MNI152NLin2009cAsym:res-2 --notrack
mkdir -p /data/summary
cp /data/derivatives/fmriprep/sub-"$SUB".html /data/summary/
cp /data/derivatives/fmriprep/sub-"$SUB"/func/*confounds_timeseries.tsv /data/summary/ 2>/dev/null || true
```

The MRIQC pass is the same pattern with `mriqc /work/bids /data/derivatives/mriqc participant --participant-label "$SUB"` in `nipreps/mriqc`, followed by one `group` Job.

## README blurb

Preprocess a public OpenNeuro dataset with fMRIPrep and MRIQC as a per-subject sweep: each task downloads one subject in the cluster, runs a pinned NiPreps container on 8 CPU cores, and writes derivatives to a shared volume. A pilot catches memory problems early, and nrp_watch explains failures such as out-of-memory kills in plain language. Only public, de-identified data belongs on Nautilus; IRB-governed scans need a secure environment instead.
