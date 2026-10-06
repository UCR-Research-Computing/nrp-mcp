# Slurm array to Kubernetes (example)

A Slurm batch script with `--array=0-9` that bootstraps a trimmed mean in R, ten times with
ten different seeds. Used in workshops to show how a Slurm array becomes a Kubernetes
Indexed Job.

Ask your assistant: *"Run the Slurm script in this folder on Nautilus."* `nrp_plan` reads
`sim.sbatch` and shows what each line becomes:

| Slurm | Kubernetes |
|---|---|
| `--cpus-per-task=1`, `--mem=2G` | CPU and memory request (limits equal requests) |
| `--time=00:20:00` | `activeDeadlineSeconds` (rounded up to whole hours) |
| `--array=0-9` | Indexed Job with 10 tasks; each task gets its own `$SLURM_ARRAY_TASK_ID` |
| `module load R` | replaced by the container image (`rocker/r-ver`) |

The script keeps reading `$SLURM_ARRAY_TASK_ID`, so the same file runs on a Slurm cluster
and on Nautilus. `--array=1-50%10` would give ids 1..50, ten at a time.

CPU only; each task takes well under a minute.
