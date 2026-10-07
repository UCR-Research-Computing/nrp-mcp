# 11. A Monte Carlo Simulation Study for a New Joint Model, From Slurm Array to Nautilus

**Field:** Statistics and biostatistics (longitudinal and survival data) | **UCR connection:** The UCR Department of Statistics (https://statistics.ucr.edu/), ranked No. 41 in statistics by U.S. News in 2026 (Inside UCR, https://insideucr.ucr.edu/stories/2026/04/07/ucr-grad-programs-ranked-among-best ), where biostatisticians develop joint models of longitudinal outcomes, hospitalization and survival for patients with end-stage kidney disease, with finite-sample performance studied by simulation (example abstract on the UCR Data Science Center site: https://datascience.ucr.edu/news/2022/05/27/bayesian-multilevel-time-varying-framework-joint-modeling-hospitalization-and ) | **Fit:** Excellent

## The science

A joint model links a longitudinal biomarker (say, a repeated lab value) to the hazard of an event (hospitalization, death) through shared random effects. When a methods group proposes a new joint model or estimator (say an EM algorithm with a spline baseline hazard), reviewers expect a simulation study: generate data from a known truth many times, fit the new method and competitors, and report bias, empirical and model-based standard errors, 95% coverage and MSE across sample sizes and censoring levels.

Compute is the bottleneck: a joint-model EM fit with numerical integration over random effects takes 2-15 minutes for 500-1,000 subjects, and 3 sample sizes x 2 censoring rates x 500 replicates is 3,000 fits plus comparators. That is days on a laptop, again for every scenario a reviewer adds. Every replicate is independent: the textbook job array.

## Who at UCR does this

The Department of Statistics in the College of Natural and Agricultural Sciences, including biostatistics faculty who build joint models for chronic and end-stage kidney disease (hospitalization, mortality, kidney function), and methods groups elsewhere that evaluate new estimators by Monte Carlo, often already as Slurm arrays for the UCR HPCC. None of these groups use nrp-mcp today; this is an illustration.

## The data

The simulation study uses fully synthetic data, generated inside each task from parameters in the script. It describes no real person, so it is P1 and fine on Nautilus.

The motivating real-data analysis is different. The United States Renal Data System (USRDS, https://www.niddk.nih.gov/about-niddk/strategic-plans-reports/usrds) and cohort studies such as CRIC contain patient-level health data released under data use agreements. Those data are restricted and must NOT go to Nautilus in any form, including "de-identified" extracts or intermediate files with patient rows. At UCR the real-data analysis belongs in the UCR Secure Enclave (NIST 800-171) or an Ursa Major Tier 2 secure GCP project; ask UCR Research Computing. Keep real-data files out of the simulation folder so nrp_plan never sees them.

## The conversation

**Researcher:** This folder has jmsim.R and jmsim.sbatch. On the HPCC I run it with --array=1-3000%50. Can I run it on Nautilus without changing it?

**Assistant:** [nrp_status] No CPU quota limits, nothing running. [nrp_plan goal=sweep slurm_script=jmsim.sbatch] Each line becomes: --job-name -> object name jmsim; --cpus-per-task=1 -> cpu 1; --mem=2G -> memory 2Gi; --time=02:00:00 -> a 2 h Job time limit, but on Kubernetes that limit covers the whole sweep, not each task, and 60 waves of 5-15 minutes need far longer, so I set hours=16 instead; --array=1-3000%50 -> Indexed Job with 3,000 tasks, each with its own $SLURM_ARRAY_TASK_ID (1..3000), and %50 becomes parallelism 50; "module load R" -> replaced by the container image (rocker/r-ver); Rscript jmsim.R -> the command. The default caps pass: 50 pods at the same time is within pods_per_run (50), and 3,000 tasks is under tasks_per_run (10,000). With more than 100 pods in the run, R4 applies and nrp sets limits equal to requests.

**Researcher:** Good. Where do results go?

**Assistant:** I need somewhere to keep them: [nrp_plan goal=volume size=10Gi data_is_p1=true] (synthetic data only). **Researcher:** Yes to the volume. [nrp_run] Created "jmsim-results".

**Researcher:** Before 3,000 tasks, try 5.

**Assistant:** [nrp_plan goal=job command="Rscript jmsim.R --ids 1,2,3,4,5" data_volume=jmsim-results] One Job that fits replicates 1-5 in a row, 1 CPU and 2 GiB, image rocker/r-ver. **Researcher:** Go. [nrp_run] [nrp_watch] Failed, exit 1: "Error in library(JM): there is no package called 'JM'". rocker/r-ver is plain R; your HPCC module had the packages preinstalled. Installing them in 3,000 tasks would waste time and hammer CRAN; build an image once.

**Researcher:** Build one.

**Assistant:** [nrp_build] Two files for a project on NRP GitLab (https://gitlab.nrp-nautilus.io): a Dockerfile FROM rocker/r-ver:4.4 adding JM, JMbayes2, nlme, survival and splines2, and a .gitlab-ci.yml that builds it with kaniko into gitlab-registry.nrp-nautilus.io/<group>/jmsim:1.0. nrp_build writes nothing; you commit and GitLab builds it.

**Researcher:** Pipeline passed. Run the trial again with that image.

**Assistant:** [nrp_plan goal=job command="Rscript jmsim.R --ids 1,2,3,4,5" image=gitlab-registry.nrp-nautilus.io/<group>/jmsim:1.0 data_volume=jmsim-results] [nrp_run] [nrp_watch] Succeeded; the 5 fits took 6-9 minutes each; five files rep_0001.rds .. rep_0005.rds in /data/sim.

**Researcher:** Now the full 3,000.

**Assistant:** [nrp_plan goal=sweep slurm_script=jmsim.sbatch hours=16 image=gitlab-registry.nrp-nautilus.io/<group>/jmsim:1.0 data_volume=jmsim-results] Summary: an Indexed Job, 3,000 tasks, 50 at a time, 1 CPU and 2 GiB each (limits equal requests), 16 h limit for the whole sweep (60 waves). Each task gets $SLURM_ARRAY_TASK_ID 1..3000, which your script uses as the seed and to pick the scenario. Confirm token valid 10 minutes. **Researcher:** Go. [nrp_run] Running. Run card in .nrp/runs/.

**Researcher:** Status?

**Assistant:** [nrp_watch] 2,991 succeeded, 9 failed. All 9 are in scenario 6 (n = 1000, 60% censoring); exit 137 with reason OOMKilled: they reached the 2 GiB limit. Fix: rerun those 9 with 3 GiB (raise memory 50%). [nrp_plan goal=job memory=3Gi command="Rscript jmsim.R --ids 2507,2533,..."] One Job that refits the 9 replicates in a row (about 75 minutes). **Researcher:** Yes. [nrp_run] [nrp_watch] 9 of 9 succeeded.

**Researcher:** Combine and bring the table home, then clean up.

**Assistant:** [nrp_plan goal=job command="Rscript combine.R" image=...jmsim:1.0 data_volume=jmsim-results cpu=2 memory=4Gi] [nrp_run] Writes summary.csv (bias, ESE, ASE, coverage, MSE by scenario). [nrp_data down volume=jmsim-results path=summary/] 2 MB copied to ./summary. [nrp_cleanup] lists 5 runs; deleted after your second yes. The volume stays.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Slurm translation | nrp_plan slurm_script=jmsim.sbatch | (plan only) | - |
| Results storage | goal=volume | PVC (CephFS, RWX) | 10 GiB |
| R image | nrp_build | Dockerfile + .gitlab-ci.yml (built on NRP GitLab) | about 1.5 GB image |
| Trial | goal=job | Job | 1 CPU, 2 GiB, 5 replicates in a row |
| Full study | goal=sweep | Indexed Job | 3,000 tasks x 1 CPU, 2 GiB; 50 at a time; 5-15 min each |
| OOM reruns | goal=job | Job | 1 CPU, 3 GiB, 9 replicates in a row |
| Combine | goal=job | Job | 2 CPU, 4 GiB, minutes |
| Results home | nrp_data down | helper pod | about 2 MB |

## Compute and cost estimate

Assumptions: 6 scenarios x 500 replicates = 3,000 tasks; each task fits the new EM estimator plus one comparator (JM) in about 8 minutes on one core. Total about 400 CPU-hours. At 50 tasks at a time that is about 8 hours of wall clock, an overnight run; raising parallelism to 100 (and pods_per_run with it) roughly halves it. Nautilus is free to the user. An 8-core laptop would take 2-3 days. The HPCC runs this well ($1,000 per lab per year; same sbatch file); Nautilus helps when its queue is long or the lab has no account. A cloud VM at about $0.04 per core-hour costs about $16 per study, but needs an account and a budget.

## Limits and honest caveats

- Real patient data (USRDS, CRIC, EHR extracts, anything under a DUA) never go to Nautilus; synthetic data only.
- The default caps fit this array as is: pods_per_run (50) limits pods at the same time, which %50 matches, and tasks_per_run (10,000) limits the total. Running more than 50 at once means raising pods_per_run deliberately. The Job time limit covers all waves, so size hours for the whole array, not one replicate.
- The task id as seed is simple and reproducible; for stricter stream independence use L'Ecuyer-CMRG streams (parallel::nextRNGStream) indexed by it.
- Bayesian comparators (JMbayes2, MCMC) take far longer per fit; give them their own sweep with a longer --time.
- Multi-node MPI (Rmpi across nodes) does not fit; keep each replicate inside one pod.
- This is the nrp-mcp workshop example (examples/slurm-array-bootstrap: an R bootstrap with --array=0-9) scaled up: same file on Slurm and Nautilus, more tasks, a real image, and a results volume.

## Starter kit

Sketch: `jmsim.sbatch` (unchanged from the HPCC)

```bash
#!/bin/bash
#SBATCH --job-name=jmsim
#SBATCH --cpus-per-task=1
#SBATCH --mem=2G
#SBATCH --time=02:00:00
#SBATCH --array=1-3000%50
module load R
Rscript jmsim.R
```

Sketch: `jmsim.R`

```r
suppressPackageStartupMessages({ library(nlme); library(survival); library(JM) })
args <- commandArgs(TRUE)
ids  <- if (length(args) && args[1] == "--ids") as.integer(strsplit(args[2], ",")[[1]]) else
          as.integer(Sys.getenv("SLURM_ARRAY_TASK_ID"))
scen <- expand.grid(n = c(200, 500, 1000), cens = c(0.3, 0.6))      # 6 scenarios
out_dir <- if (dir.exists("/data")) "/data/sim" else "sim"; dir.create(out_dir, FALSE, TRUE)
for (id in ids) {
  set.seed(20261006 + id)
  s   <- scen[(id - 1) %/% 500 + 1, ]
  dat <- simulate_joint(n = s$n, cens = s$cens,                       # your generator (R/sim.R)
                        beta = c(10, -0.3), alpha = 0.5, sigma = 1)
  t0  <- Sys.time(); new <- fit_em_joint(dat)                          # your new estimator (R/em.R)
  lme1 <- lme(y ~ time, random = ~ time | id, data = dat$long)
  cox1 <- coxph(Surv(T, d) ~ 1, data = dat$surv, x = TRUE)
  old  <- jointModel(lme1, cox1, timeVar = "time", method = "piecewise-PH-aGH")
  saveRDS(list(id = id, scen = s, new = coef_table(new), jm = summary(old),
               secs = as.numeric(Sys.time() - t0, units = "secs")),
          file.path(out_dir, sprintf("rep_%04d.rds", id)))
}
```

`combine.R` reads every rep_*.rds, compares estimates with the truth, and writes summary.csv.

## README blurb

Statistics methods papers depend on simulation studies with thousands of independent replicates. nrp-mcp reads an existing Slurm array script, shows what each #SBATCH line becomes on Kubernetes, and runs it as an Indexed Job where every task keeps its own SLURM_ARRAY_TASK_ID, so the same file still runs on a campus cluster. When R packages are missing it suggests an image built on NRP GitLab, and when a few replicates run out of memory it names them so only those are rerun. Use it only with synthetic or public data; real patient data belong in a secure environment.
