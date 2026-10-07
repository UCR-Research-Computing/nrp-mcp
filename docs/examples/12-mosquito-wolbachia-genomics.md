# 12. Wolbachia Strain Genomics for Plant-Mediated Mosquito Control

**Field:** Entomology, microbial genomics, vector biology | **UCR connection:** "A Powerful Biological Tool for Targeting Mosquitoes?" (UCR Department of Entomology news, May 5, 2026): a UCR entomology project is building a library of Wolbachia strains from insects such as bees and ants to find strains that survive on plants and transfer to mosquitoes feeding on the same flowers: https://entomology.ucr.edu/news/2026/05/05/powerful-biological-tool-targeting-mosquitoes | **Fit:** Good

## The science

Wolbachia is an intracellular bacterium carried by a large share of insect species. Some strains reduce mosquitoes' ability to transmit viruses, and current programs repeatedly release infected mosquitoes. The UCR work explores another route: bees may deposit Wolbachia on flowers, where mosquitoes feeding on the same plants pick it up, giving a self-sustaining spread. The genomics question is which strains in a growing library of bee, ant and other insect isolates carry the traits that matter (persistence outside the host, colonizing a new host, cytoplasmic incompatibility genes), and how they relate to known mosquito strains.

That means assembling and annotating every strain, then comparing them: average nucleotide identity, gene presence and absence (cif genes, prophage WO), and a core-genome phylogeny among hundreds of public genomes. Each assembly is independent and needs 8-16 cores and 16-64 GB for an hour or two, longer when host DNA dominates. With 150-300 samples that is thousands of CPU-hours: a per-sample sweep, then one big phylogeny job.

## Who at UCR does this

The Department of Entomology (https://entomology.ucr.edu/) and the Center for Infectious Disease and Vector Research (https://cidvr.ucr.edu/), where groups study mosquitoes, symbionts and vector-borne disease; microbial genomics groups across CNAS run similar pipelines. None of these groups use nrp-mcp today; this is an illustration.

## The data

- NCBI Datasets, Wolbachia (taxon 953): https://www.ncbi.nlm.nih.gov/datasets/genome/?taxon=953 . Over 2,000 assemblies, mostly 1.0-1.6 Mb, so hundreds of genomes are only a few GB.
- NCBI SRA (https://www.ncbi.nlm.nih.gov/sra) and ENA (https://www.ebi.ac.uk/ena/browser/home) for raw reads; ENA serves FASTQ over https, which nrp's pull goal can fetch. A whole-insect run is often 2-10 GB compressed, mostly host reads.
- PubMLST Wolbachia typing: https://pubmlst.org/organisms/wolbachia-spp
- Tools: SPAdes (https://github.com/ablab/spades), Bakta (https://github.com/oschwengers/bakta; light database 3.9 GB unzipped) or Prokka, FastANI, Panaroo (https://github.com/gtonkinhill/panaroo), IQ-TREE (https://iqtree.github.io/), minimap2, fastp.

Insect and bacterial sequence data are P1, including the lab's unpublished strains. Small files (under 100 MB) go up with nrp_data; full FASTQ sets come in through an in-cluster https download or NRP S3 (not yet tested by UCR RC). If a related project sequenced human blood meals from mosquitoes, those human reads would be identifiable and must not go to Nautilus.

## The conversation

**Researcher:** I have 180 samples: 120 public SRA runs from bees, ants and mosquitoes, and 60 of our own. I want each assembled, annotated and placed in a tree with public Wolbachia genomes.

**Assistant:** [nrp_status] No CPU quota limits; nothing running. First a volume: [nrp_plan goal=volume size=1Ti data_is_p1=true] One CephFS volume "wolb-data" (ReadWriteMany) at /data; purged after 6 months unused. **Researcher:** Yes, all insect and bacterial data. [nrp_run] Created.

**Researcher:** Pull the 120 SRA runs.

**Assistant:** I turned your accessions into 240 ENA https FASTQ links. [nrp_plan goal=pull data_volume=wolb-data subdir=reads urls=[240 links] data_is_p1=true] One download Job with retries, about 700 GB. **Researcher:** Yes. [nrp_run] [nrp_watch] Done in about 3 hours. A second pull fetches your 60 strains from the sequencing core's https page.

**Researcher:** assemble.sh runs fastp, minimap2 against a Wolbachia pan-reference to drop host reads, SPAdes, then Bakta. Plan a sweep.

**Assistant:** [nrp_plan goal=sweep count=180 parallel=30 cpu=8 memory=32Gi data_volume=wolb-data] The caps pass: 30 pods at the same time is within pods_per_run (50), and 180 tasks is under tasks_per_run. With more than 100 pods in the run, R4 applies and nrp sets limits equal to requests. But no image has these tools; installing SPAdes and Bakta in 180 pods is slow and fragile.

**Researcher:** Make me an image.

**Assistant:** [nrp_build] Two files for NRP GitLab: a Dockerfile installing the tools from bioconda, and a .gitlab-ci.yml that builds it with kaniko into gitlab-registry.nrp-nautilus.io/<group>/wolb-tools:1.0. Commit them and GitLab builds the image. The Bakta light database goes on the volume once (a small Job), not into the image.

**Researcher:** Built, and the database is in /data/db. Plan the sweep again.

**Assistant:** [nrp_plan goal=sweep count=180 parallel=30 cpu=8 memory=32Gi image=gitlab-registry.nrp-nautilus.io/<group>/wolb-tools:1.0 command="bash assemble.sh" data_volume=wolb-data] Summary: an Indexed Job, 180 tasks, 30 at a time, 8 CPU and 32 GiB each (limits equal requests), no GPU, /data mounted, 24 h limit for the whole sweep (6 waves of 30 at 1-3 h each fits). $SLURM_ARRAY_TASK_ID 0..179 picks a line of samples.tsv. Warning R16: requests over 1 CPU / 2 GiB are checked for use; SPAdes keeps 8 threads busy. Confirm token valid 10 minutes. **Researcher:** Go. [nrp_run] Running.

**Researcher:** How is it going?

**Assistant:** [nrp_watch] 171 succeeded, 9 failed. Six are OOMKilled at the 32 GiB limit in SPAdes: whole-ant runs where far more reads survived host filtering. Fix: raise memory 50%, to 48 GiB. The other three exited 1 with "No reads left after mapping": probably no Wolbachia, or a strain far from the pan-reference. That is a result, not a bug; metaSPAdes on unfiltered reads can confirm.

**Researcher:** Rerun the six at 48 GiB.

**Assistant:** [nrp_plan goal=job cpu=8 memory=48Gi command="bash assemble.sh --ids 12,47,48,90,131,166" ...] One Job, six samples in a row. **Researcher:** Yes. [nrp_run] [nrp_watch] All six assembled.

**Researcher:** Now the tree, with 400 public genomes.

**Assistant:** [nrp_plan goal=job cpu=32 memory=64Gi hours=36 command="bash phylo.sh" image=...wolb-tools:1.0 data_volume=wolb-data] One Job: FastANI, Panaroo core alignment, IQ-TREE with 1,000 ultrafast bootstraps. **Researcher:** Go. [nrp_run] [nrp_watch] Done in 19 hours. [nrp_data down volume=wolb-data path=results/] copied 1.2 GB of annotations, ANI matrix, gene table and tree to ./results. [nrp_cleanup] lists 6 runs; deleted after your second yes. The volume stays.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | goal=volume | PVC (CephFS, RWX) | 1 TiB |
| Public and lab reads | goal=pull (x2) | Job (curl) | 1 CPU, 2 GiB; hours; about 1 TB total |
| Tool image | nrp_build | Dockerfile + .gitlab-ci.yml, built on NRP GitLab | about 3 GB image |
| Bakta database | goal=job | Job | 1 CPU, 2 GiB, minutes |
| Per-sample assembly + annotation | goal=sweep | Indexed Job | 180 tasks x 8 CPU, 32 GiB; 30 at a time; 1-3 h each |
| OOM reruns | goal=job | Job | 8 CPU, 48 GiB, 6 samples in a row |
| Phylogenomics | goal=job | Job | 32 CPU, 64 GiB, about 20 h |
| Results home | nrp_data down | helper pod | about 1 GB |

## Compute and cost estimate

Assumptions: about 2 hours on 8 cores per sample, so 180 samples is about 2,900 CPU-hours, about 12 hours of wall clock at 30 tasks (240 cores) at a time. The phylogeny adds about 600 CPU-hours (32 cores for about 20 hours). Total about 3,500 CPU-hours, roughly two days end to end including downloads. Nautilus is free to the user. A 16-core workstation would need over a week. The HPCC handles this well with bioinformatics modules installed; Nautilus adds 240 cores at once next to a terabyte of reads. On a commercial cloud, 3,500 core-hours at $0.03-0.05 is $100-175 plus storage and transfer.

## Limits and honest caveats

- pods_per_run (default 50) limits pods at the same time, so 180 samples at parallel=30 pass with default caps. The 24 h Job limit covers all 6 waves, not each task; size hours for the whole sweep.
- Host filtering keeps memory reasonable, but some samples still need 48-64 GiB. Wolbachia genomes are repeat-rich, so short-read assemblies are fragmented; closed genomes need long reads (Flye plus polishing), which fits the same sweep.
- NRP S3 suits terabytes of the lab's own reads but is untested by UCR RC; https downloads with goal=pull work today.
- IQ-TREE on hundreds of genomes fits one big pod; multi-node MPI phylogenies belong on the HPCC or Ursa Major.
- CephFS writes ran about 86 MiB/s per writer in a UCR test; SPAdes scratch uses pod-local /tmp; only final outputs go to /data.
- Nautilus is not an archive: submit assemblies to GenBank.

## Starter kit

Sketch: `assemble.sh`

```bash
#!/bin/bash
set -euo pipefail
IDS=${2:-$SLURM_ARRAY_TASK_ID}                  # --ids 12,47,... or the array id
for i in ${IDS//,/ }; do
  read -r S R1 R2 < <(sed -n "$((i + 2))p" samples.tsv)   # header on line 1
  W=/tmp/$S; O=/data/asm/$S; mkdir -p "$W" "$O"
  fastp -w 8 -i /data/reads/$R1 -I /data/reads/$R2 -o $W/r1.fq.gz -O $W/r2.fq.gz -j $O/fastp.json
  minimap2 -t 8 -ax sr /data/ref/wolbachia_pan.fa $W/r1.fq.gz $W/r2.fq.gz \
    | samtools fastq -F 4 -1 $W/w1.fq.gz -2 $W/w2.fq.gz -
  [ -s $W/w1.fq.gz ] || { echo "No reads left after mapping: $S"; exit 1; }
  spades.py --isolate -t 8 -m 30 -1 $W/w1.fq.gz -2 $W/w2.fq.gz -o $W/spades
  bakta --db /data/db/db-light --threads 8 --prefix $S --output $O $W/spades/contigs.fasta
  cp $W/spades/contigs.fasta $O/
done
```

Sketch: `phylo.sh` runs `fastANI` (all vs all, 32 threads), `panaroo -a core`, then `iqtree2 -m MFP -B 1000 -T 32` on the core alignment.

Sketch: `Dockerfile` (as nrp_build would suggest)

```dockerfile
FROM mambaorg/micromamba:1.5
RUN micromamba install -y -n base -c conda-forge -c bioconda \
      fastp minimap2 samtools spades bakta checkm2 fastani panaroo iqtree && \
    micromamba clean -a -y
```

## README blurb

Bacterial genomics often means one assembly and annotation pipeline over hundreds of samples, then a large comparison. nrp-mcp downloads public reads into a cluster volume, suggests a bioinformatics image built on NRP GitLab, and runs the pipeline as a sweep, one sample per task. When samples run out of memory it names them and the fix, and a final job builds the phylogeny next to the data. Results come back to the laptop and everything else is cleaned up after a second yes.
