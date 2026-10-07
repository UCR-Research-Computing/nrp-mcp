# 04. Genome-wide scans for plant breeding on public sequence data

**Field:** Plant genomics, quantitative genetics, crop breeding | **UCR connection:** Modeled on "Gene discovery could shave years off avocado breeding" (UCR News, July 28, 2026, https://news.ucr.edu/articles/2026/07/28/gene-discovery-could-shave-years-avocado-breeding). That PNAS study (https://www.pnas.org/doi/10.1073/pnas.2606876123) was led by UC Davis with UCR Botany and Plant Sciences, and identified SDMYB as the gene that sets A- or B-type flowering. UCR supplied hundreds of diverse trees and confirmed the flower type of more than 500 of them. Also modeled on the Givaudan Citrus Variety Collection at UCR (https://citrusvariety.ucr.edu/), with about 4,500 trees of nearly 1,100 citrus cultivars and relatives. | **Fit:** Good

## The science

Many breeding traits appear only after years: an avocado seedling takes five to ten years to flower and show whether it is A- or B-type. Finding the gene behind such a trait means scanning the genome: sequence a few hundred diverse trees, align the reads to a reference, call variants, and test every variant for association with the trait. With SDMYB known, flower type can be read from a seedling's DNA. Citrus breeders ask the same kind of question about disease tolerance, fruit traits or rootstock vigor, using the diversity held in the Givaudan collection.

The statistics are light. Turning raw reads into genotypes is the heavy part: alignment and sorting take hours of CPU per sample, so 200 samples is thousands of core-hours. Samples are independent, and so are chromosomes at the association step, which gives two Indexed Jobs.

## Who at UCR does this

The avocado breeding program and the avocado variety collection (https://avocado.ucr.edu/) in Botany and Plant Sciences, the Givaudan Citrus Variety Collection, and plant genetics groups across CNAS and the Institute for Integrative Genome Biology. Many of these groups already use the HPCC; Nautilus is overflow for large public-data reanalyses. None of these groups use nrp-mcp today; this is an illustration.

## The data

- **Reads.** NCBI SRA (https://www.ncbi.nlm.nih.gov/sra). A query for Persea americana returned 1,831 SRA records, 492 of them whole-genome sequencing (Oct. 2026), and citrus has several thousand WGS records. Public runs can be fetched over plain https from the SRA Open Data mirror, for example `https://sra-pub-run-odp.s3.amazonaws.com/sra/<SRR>/<SRR>` (see https://www.ncbi.nlm.nih.gov/sra/docs/sra-aws-download/). That is the anonymous https link the `pull` goal needs. Expect roughly 4-8 GB per sample as compressed .sra at about 15x coverage of the roughly 0.9 Gb avocado genome, and 3-4 times that as FASTQ.
- **References.** Avocado 'Hass', chromosome-level assembly GCA_031208685.1 (https://ftp.ncbi.nlm.nih.gov/genomes/all/GCA/031/208/685/GCA_031208685.1_ASM3120868v1/, genomic FASTA 251 MB compressed). Sweet orange Csi_valencia_1.0 (https://www.ncbi.nlm.nih.gov/datasets/genome/GCF_000317415.1).
- **Phenotypes.** A small table from the lab with sample ID, flower type and covariates.
- **P1 check.** Plant sequence data from public archives, plus the lab's own plant phenotypes. Everything here is P1. **Human genomic data is not.** dbGaP and other controlled-access human data, and patient or clinical sequencing, must not go on Nautilus. At UCR that work goes to the UCR Secure Enclave (NIST 800-171) or an Ursa Major Tier 2 secure GCP project. nrp-mcp asks you to confirm `data_is_p1` before any upload or download, but it cannot inspect your files. The responsibility is yours.

## The conversation

**Researcher:** I have 200 public avocado runs listed in ~/proj/avo-scan/samples.txt. Align them to the Hass reference, call variants and run my association test on flower type, chromosome by chromosome, in R.

**Assistant:** Your pipeline needs bwa-mem2, samtools, bcftools and sra-tools, and no stock image has them. [nrp_build project=~/proj/avo-scan/align] nrp suggests a Dockerfile and a .gitlab-ci.yml that builds with kaniko on NRP GitLab into gitlab-registry.nrp-nautilus.io. nrp writes nothing itself. I adjusted the Dockerfile to a micromamba base with the four bioconda tools and saved both files with my file tool. Push them to a new project on https://gitlab.nrp-nautilus.io and the pipeline builds the image. You don't need registry keys.

**Researcher:** Pushed. The pipeline passed.

**Assistant:** [nrp_plan goal=volume size=2Ti data_is_p1=true] gives a CephFS volume `avo-scan-data`. Then [nrp_plan goal=pull data_volume=avo-scan-data subdir=sra urls=[200 SRA links + Hass FASTA] hours=12 data_is_p1=true] gives one download Job that fetches the files one after another with 5 retries each, about 1.2 TB in total at roughly 90 MB/s, or 4 to 5 hours. Then the alignment sweep: [nrp_plan goal=sweep project=~/proj/avo-scan/align image=gitlab-registry.nrp-nautilus.io/avo-scan/align:latest count=200 parallel=40 cpu=8 memory=32Gi hours=24 data_volume=avo-scan-data]. Each task converts its .sra to FASTQ on the pod's local scratch, not on CephFS, then aligns, sorts and writes `/data/bam/<sample>.bam`. 40 pods at the same time is under your `pods_per_run` cap of 50, so all 200 can run 5 waves deep with no cap change, and because the run has more than 100 pods, limits are set equal to requests automatically. I'd start the sweep only after the pull finishes. Pods that wait at 8 CPU while doing nothing would trip the NRP usage checks.

**Researcher:** Good. Start the volume and the pull.

**Assistant:** [nrp_run] x2. [nrp_watch] The pull finished in 4 h 40 min, with 200 runs and the reference in /data. A small prep Job indexes the reference once. Then I re-plan the sweep, because the old token has expired after 10 minutes, and run it: [nrp_run]. Later: [nrp_watch] 200 of 200 alignment tasks succeeded, median 2 h 50 min each, in about 15 hours wall-clock.

Next, the per-chromosome stage: [nrp_plan goal=sweep project=~/proj/avo-scan/assoc count=12 parallel=12 cpu=4 memory=16Gi data_volume=avo-scan-data] An Indexed Job of 12 tasks. Image rocker/r-ver, because nrp detected an R entry point, `Rscript assoc.R`. Each task jointly calls variants for chromosome $JOB_COMPLETION_INDEX+1 across all 200 BAMs with bcftools, then fits the association model in R. Run it?

**Researcher:** Go.

**Assistant:** [nrp_run] [nrp_watch] All 12 tasks failed with exit 1. The last log lines show `Error in library(data.table): there is no package called 'data.table'`, and `bcftools: command not found` would have come next. rocker/r-ver is bare R. nrp installs requirements.txt at start for Python, but not R packages. Fix: build an image. Your folder has a renv.lock, so [nrp_build project=~/proj/avo-scan/assoc] suggests a Dockerfile from rocker/r-ver that runs `renv::restore()`. I added bcftools from apt. After the GitLab pipeline builds it, I'll re-plan with the new image.

**Researcher:** Built.

**Assistant:** [nrp_plan ... image=gitlab-registry.nrp-nautilus.io/avo-scan/assoc:latest] [nrp_run] [nrp_watch] 12 of 12 succeeded. The largest chromosome took 9 hours. [nrp_plan goal=job command="Rscript combine.R"] [nrp_run] Then [nrp_data down path=results/]: a Manhattan plot, the top 500 associations, and per-chromosome summary tables (60 MB) are in ~/proj/avo-scan/results. [nrp_cleanup] lists 5 runs. The BAMs stay on the volume unless you say otherwise.

**Researcher:** Delete the runs. I'll copy the VCFs to lab storage and then drop the volume.

**Assistant:** [nrp_cleanup plan_id=... confirm_token=...] Deleted. When the copy is done, ask me to clean up the volume's run as well.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Aligner image | nrp_build (NRP GitLab, kaniko) | none (files only) | one CI build |
| Storage | volume | PVC, rook-cephfs-central, RWX | 2 TiB |
| Reads + reference | pull | Job (curlimages/curl) | 1 CPU, 2 GiB, about 5 h |
| Alignment | sweep | Indexed Job, 200 tasks, 40 parallel | 8 CPU, 32 GiB each, about 3 h per task, 0 GPU |
| R image | nrp_build | none (files only) | one CI build |
| Calling + association | sweep | Indexed Job, 12 tasks | 4 CPU, 16 GiB each, 3-9 h |
| Combine | job | Job | 2 CPU, 8 GiB, minutes |

## Compute and cost estimate

Assumptions: about 15x short-read coverage per tree, and bwa-mem2 plus sorting at about 3 hours on 8 cores per sample. Alignment is 600 task-hours, or about 4,800 core-hours. Joint calling and association add about 70 task-hours, or about 280 core-hours. With 40 tasks at a time, the whole scan takes about a day of wall-clock time including the download, with no GPUs. Nautilus costs the user nothing.

For comparison, a 16-core workstation would need about 12 days for alignment alone. On the HPCC, 5,000 core-hours is normal work, and with its shared bioinformatics modules it is the natural home for groups already there. Nautilus adds capacity on top. On a commercial cloud, about 5,000 core-hours cost roughly $150-$250 on demand, plus around $50 a month to keep 2 TB of disk.

## Limits and honest caveats

- **Human and controlled-access genomes do not belong here.** This includes dbGaP, patient sequencing and anything under a data-use agreement. Use the UCR Secure Enclave or an Ursa Major Tier 2 project.
- **Storage is the hard part.** 200 BAMs are about 1.5 TB. Keep FASTQ on pod scratch (the default ephemeral limit is 50 GiB per container; raise it for deep samples). Move finished VCFs off Nautilus, which is not an archive and purges untouched volumes after 6 months.
- **Stock images are bare.** Bioinformatics tools and R packages mean an image built with nrp_build on NRP GitLab. nrp suggests the files and does not build them itself.
- **Download pacing.** A single pull Job downloads sequentially. For thousands of runs, have each alignment task fetch its own run instead, or stage through NRP S3, which UCR RC has not yet tested with nrp-mcp.
- **The statistics are yours.** Population structure, kinship and multiple testing belong in assoc.R; nrp does not review the method.

## Starter kit

`align/align.sh` (sketch)
```bash
#!/bin/bash
set -euo pipefail
S=$(sed -n "$((JOB_COMPLETION_INDEX+1))p" samples.txt)
[ -f /data/bam/$S.bam ] && exit 0
REF=/data/ref/hass.fa                                  # indexed once by a prep job
mkdir -p /tmp/w && cd /tmp/w                           # pod scratch, not CephFS
fasterq-dump --split-files -e 8 /data/sra/$S -O .
bwa-mem2 mem -t 8 -R "@RG\tID:$S\tSM:$S" $REF ${S}_1.fastq ${S}_2.fastq \
  | samtools sort -@ 4 -m 2G -o $S.bam -
samtools index $S.bam && mv $S.bam* /data/bam/
```

`align/Dockerfile` (after nrp_build, edited)
```
FROM mambaorg/micromamba:latest
RUN micromamba install -y -n base -c conda-forge -c bioconda \
    bwa-mem2 samtools bcftools sra-tools && micromamba clean -a -y
WORKDIR /work
COPY . /work
```

`assoc/assoc.R` (sketch)
```r
library(data.table)
chr <- as.integer(Sys.getenv("JOB_COMPLETION_INDEX")) + 1
bams <- list.files("/data/bam", "\\.bam$", full.names = TRUE)
vcf <- sprintf("/data/vcf/chr%02d.vcf.gz", chr)
region <- fread("/data/ref/hass.fa.fai", header = FALSE)$V1[chr]  # chromosome name
system(sprintf("bcftools mpileup -r %s -f /data/ref/hass.fa %s | bcftools call -mv -Oz -o %s",
               region, paste(bams, collapse = " "), vcf))
g <- fread(cmd = sprintf("bcftools query -f '%%POS[\\t%%GT]\\n' %s", vcf))
ph <- fread("phenotypes.csv")                           # sample, flower_type (A/B)
p <- apply(g[, -1], 1, function(x) {
  d <- sapply(strsplit(x, "[/|]"), function(a) sum(as.integer(a)))
  summary(glm(ph$flower_type == "B" ~ d, family = binomial))$coef[2, 4] })
fwrite(data.table(chr, pos = g[[1]], p), sprintf("/data/results/chr%02d.tsv", chr))
```

## README blurb

A two-stage genome scan on public sequence data: pull SRA runs and a reference into a Nautilus volume, align every sample as one task of a CPU sweep, then run joint variant calling and an R association test as a second sweep with one task per chromosome. nrp_build turns the bioinformatics toolchain into a reusable image on NRP GitLab, and nrp_watch explains failures such as a missing R package. This fits plant, animal and microbial data. Human genomic and controlled-access data must go to a secure environment instead.
