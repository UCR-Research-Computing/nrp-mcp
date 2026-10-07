# 19. Monte Carlo and bootstrap studies for econometric estimators

**Field:** Econometrics, financial econometrics, statistics | **UCR connection:** Econometrics is a listed faculty research area of the UCR Department of Economics (https://economics.ucr.edu/faculty/econometrics/), whose working paper series includes bootstrap and finite-sample econometrics papers (https://economics.ucr.edu/working-papers/); U.S. News 2026 ranked UCR No. 56 in economics, No. 63 in finance and No. 41 in statistics (Inside UCR, April 2026, https://insideucr.ucr.edu/stories/2026/04/07/ucr-grad-programs-ranked-among-best) | **Fit:** Excellent

## The science

A new estimator or test in econometrics is rarely accepted on asymptotic theory alone. Referees ask how it behaves in finite samples: bias, root mean squared error, coverage of its confidence intervals, and size and power of its tests, compared with the established alternatives, under many data-generating processes. A typical design grid crosses sample size (50 to 5,000), error distribution (normal, heavy-tailed, skewed), heteroskedasticity, serial or cross-sectional dependence, instrument strength or signal-to-noise, and a misspecification switch. Each cell needs thousands of replications, and when inference uses the bootstrap, each replication contains hundreds of bootstrap draws.

A grid of 400 design cells, 5,000 replications each, with a 499-draw bootstrap inside, is about a billion estimator fits. It is embarrassingly parallel, but on a laptop it takes weeks, and every referee request ("add a t(3) error design") starts the clock again.

## Who at UCR does this

The Department of Economics lists econometrics among its faculty research areas and runs an econometrics seminar series; finance faculty in the School of Business and the Department of Statistics do related work on financial econometrics and resampling. The kind of group is a faculty member with one or two PhD students writing a methods paper, or an applied economist checking an estimator in their setting. R, Python and Julia move easily; Stata and MATLAB less so.

None of these groups use nrp-mcp today; this is an illustration.

## The data

Most of a Monte Carlo study is synthetic, so the data question is mostly about calibration and the empirical illustration.

- **FRED (Federal Reserve Economic Data),** https://fred.stlouisfed.org/: hundreds of thousands of public macro and financial series, free with an API key, for calibrating a data-generating process and for an empirical application. FRED's terms note that some series are owned by third parties with their own conditions; check source notes before redistributing. Keep the API key out of job specs.
- **Simulated data:** generated inside each task from a seed; nothing is stored except results.

**P1 check.** Synthetic data and public FRED series are P1. **WRDS data (CRSP, Compustat and similar) are licensed, not public.** WRDS's Data Download and Analysis Policy asks users to store downloaded data in a private location that only they can access and says it is not appropriate to post it on a central server. A Nautilus namespace is shared, its volumes are readable by every member, and the NRP allows only non-sensitive data. So do **not** put WRDS data on Nautilus unless the vendor's license clearly allows it. Run WRDS-based applications on WRDS Cloud, the UCR HPCC, or a private Ursa Major project, and use Nautilus for the simulation part. Restricted microdata (Census RDC, administrative records with identifiers) belongs in the UCR Secure Enclave, never here.

## The conversation

> **Researcher:** I have an R Monte Carlo study for a new heteroskedasticity-robust IV estimator. On the HPCC I run `mc.sbatch` with `--array=1-2000%50`; each task is one design cell and one block of 500 replications. Can it run on Nautilus?

> **Assistant:** [nrp_plan goal=sweep slurm_script=mc.sbatch] Reading your script: `--array=1-2000%50` becomes an Indexed Job with 2,000 tasks, 50 at a time, and each task keeps its own `$SLURM_ARRAY_TASK_ID` (1..2000), so `mc.R` runs unchanged. `--cpus-per-task=1` and `--mem=2G` become 1 CPU / 2Gi with limits equal to requests (required above 100 tasks, and this size is exempt from the usage-violation checks). `--time=02:00:00` is a per-task limit in Slurm, but on Kubernetes the Job's time limit covers the whole sweep (all waves): 2,000 tasks at 50 at a time is 40 waves, so at a few minutes per task I override it with hours=8 rather than 2 h. `--mail-user` has no equivalent; use nrp_watch. Image: rocker/r-ver, with `install.R` run at start. Results go to a volume, so first [nrp_plan goal=volume name=mc-results size=20Gi]. Confirm both?

> **Researcher:** Confirm.

> **Assistant:** [nrp_run] volume Bound. [nrp_run] sweep mc-iv started; run card in .nrp/runs/. [nrp_watch] after 15 minutes: 0 succeeded, 50 running, but every pod's log shows the same thing: `install.packages` is compiling `sandwich`, `AER` and their dependencies from source, about 12 minutes per task. Across 2,000 tasks that wastes about 400 CPU-hours.

> **Researcher:** That's silly. How do I avoid it?

> **Assistant:** Build an image once. [nrp_build] writes a Dockerfile (FROM rocker/r-ver, then the packages from install.R, using the Posit binary repository so nothing compiles) and a .gitlab-ci.yml that builds it on NRP GitLab with kaniko into gitlab-registry.nrp-nautilus.io. It writes nothing itself; you commit the two files to a GitLab project. Meanwhile I can stop the current run with [nrp_cleanup].

> **Researcher:** Done, the image built. Clean up the old run and start again with the image.

> **Assistant:** [nrp_cleanup] lists mc-iv; deleted after your second yes. [nrp_plan goal=sweep slurm_script=mc.sbatch hours=8 image=gitlab-registry.nrp-nautilus.io/<group>/mc-iv:latest]. Confirm? ... [nrp_run]. [nrp_watch] an hour later: 412 succeeded, 50 running, packages load in 2 seconds, about 7 minutes per task.

> **Researcher:** Any failures?

> **Assistant:** [nrp_watch] at the end: 1,996 succeeded, 4 failed with exit code 1. All four are the n = 50, very-weak-instrument cells (tasks 1201-1204). Last log lines: `Error in solve.default(crossprod(Z)): system is computationally singular`. That is your estimator, not the cluster: with 50 observations and a nearly irrelevant instrument, some replications draw a singular matrix. Fix: catch the error, record the replication as a failure, and report the failure rate in that cell, which referees will want to see anyway.

> **Researcher:** Patched `mc.R` with tryCatch. Rerun those four.

> **Assistant:** [nrp_plan goal=sweep count=4 parallel=4 command="Rscript mc.R --tasks 1201,1202,1203,1204"] Confirm? ... [nrp_run], [nrp_watch]: all 4 succeeded; 0.8% of replications in the hardest cell are singular. [nrp_data down path=/data/results] brought back 2,000 small RDS files (140 MB); `collect.R` on your laptop builds the bias, RMSE, coverage and size tables.

> **Researcher:** Great. Clean up, but keep the volume until the revision comes back.

> **Assistant:** [nrp_cleanup] removes the two runs after your yes. The volume stays; it is purged after 6 months without access, so keep the RDS files and the seed log in your repository.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Results storage | `volume` | PVC, rook-cephfs-central, RWX | 20Gi |
| R image | `nrp_build` | none (Dockerfile + GitLab CI; builds on NRP GitLab) | - |
| Simulation grid | `sweep` (from `mc.sbatch`) | Indexed Job | 2,000 tasks, 50 parallel (40 waves), 1 CPU / 2Gi each, 0 GPU, 8 h limit for the whole sweep, about 7 min each |
| Rerun of failed cells | `sweep` | Indexed Job | 4 tasks |
| Results | `nrp_data down` | helper pod | 140 MB |
| Cleanup | `nrp_cleanup` | deletes the runs | - |

## Compute and cost estimate

Assumptions: 400 design cells x 5 blocks = 2,000 tasks; each task runs 500 replications, each with a 499-draw wild bootstrap, about 7 minutes on one core after the image fix. That is about 235 core-hours.

- **Laptop (8 cores, all busy):** about 30 hours; each referee round adds more.
- **UCR HPCC:** a natural fit, running the same sbatch file ($1,000/lab/year). Nautilus is overflow when the partition is busy or before a deadline.
- **Ursa Major or commercial cloud:** 235 vCPU-hours is roughly $10-$20 on demand, plus setup.
- **Nautilus:** free to the user. At 50 tasks in parallel, 2,000 tasks x 7 minutes is 40 waves, about 5 hours of wall clock. Running more at once (a higher `parallel`, which needs `pods_per_run` raised in the nrp-mcp config, since it caps pods running at the same time) shortens it; the NRP asks users to keep under about 400 Jobs at once.

## Limits and honest caveats

- **No GPUs.** Monte Carlo in R or base Python is CPU work. Requesting a GPU for it would be flagged (under 40% utilization). A JAX or CuPy rewrite with vectorized replications is a different project.
- **Reproducibility is your job.** Derive each task's random stream from the task id with a parallel-safe generator (R's L'Ecuyer-CMRG streams, NumPy's `SeedSequence.spawn`, Julia's `Random.seed!` per task), log the seed, the image digest and the package versions. Do not use the time of day.
- **Tasks can be retried or preempted.** Make each task write its results atomically and skip its work if the output exists, so a retry does not double-count.
- **Small files.** Thousands of tiny files on CephFS are slow to list; one file per task is fine, one per replication is not.
- **Stata and MATLAB** need licenses that generally cannot be used in containers on a national cluster; port to R, Python or Julia, or keep those runs on the HPCC.
- **Tightly coupled models** (a large MPI-based structural estimation) fit the HPCC or Ursa Major better.
- **Licensed financial data** (WRDS and similar) stays off Nautilus unless the license allows it.

## Starter kit

```
mc.sbatch        # existing Slurm array script
mc.R             # one task = one design cell x one block
designs.csv      # n, error, hetero, pi, block
install.R        # packages (used by nrp_build)
collect.R        # laptop: tables from the RDS files
```

`mc.sbatch`:

```bash
#!/bin/bash
#SBATCH --job-name=mc-iv
#SBATCH --array=1-2000%50
#SBATCH --cpus-per-task=1
#SBATCH --mem=2G
#SBATCH --time=02:00:00
Rscript mc.R
```

`mc.R` (sketch):

```r
library(sandwich); library(AER)
id  <- as.integer(Sys.getenv("SLURM_ARRAY_TASK_ID"))
d   <- read.csv("designs.csv")[id, ]
out <- sprintf("/data/results/task_%04d.rds", id)
if (file.exists(out)) quit(save = "no")
RNGkind("L'Ecuyer-CMRG"); set.seed(20261006)
s <- .Random.seed
for (k in seq_len(id)) s <- parallel::nextRNGStream(s)
.Random.seed <- s
res <- replicate(500, tryCatch({
  dat <- simulate_dgp(d$n, d$error, d$hetero, d$pi)
  c(new = new_estimator(dat, B = 499), tsls = tsls_ci(dat))
}, error = function(e) NA), simplify = FALSE)
saveRDS(list(design = d, res = res, seed = s), paste0(out, ".tmp"))
file.rename(paste0(out, ".tmp"), out)
```

## README blurb

Finite-sample studies of econometric estimators: hand nrp-mcp an existing Slurm array script and it becomes a 2,000-task Indexed Job on Nautilus, each task keeping its `SLURM_ARRAY_TASK_ID` for a reproducible random stream. nrp_build turns slow package installs into a reusable R image, nrp_watch separates cluster problems from estimator failures, and only the small result files come back. Synthetic and public data (such as FRED) only; licensed financial databases stay where their license allows.
