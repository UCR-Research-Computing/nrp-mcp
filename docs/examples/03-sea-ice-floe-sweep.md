# 03. A thousand sea-ice floe simulations from one Slurm script

**Field:** Granular physics, geophysical fluid dynamics, polar climate | **UCR connection:** Modeled on "Engineers solve puzzle of Arctic sea ice movement" (UCR News, Sept. 16, 2026, https://news.ucr.edu/articles/2026/09/16/engineers-solve-puzzle-arctic-sea-ice-movement) and the Physical Review Letters paper it links (https://journals.aps.org/prl/abstract/10.1103/g8y2-8ytt). The study simulated ice floes as colliding grains, pushed by turbulent wind and slowed by ocean drag, and reproduced Fram Strait observations of how fast ice spreads, how floe speeds are distributed, and how ice motion varies from hours to days. | **Fit:** Excellent

## The science

Arctic sea ice is made of floes, separate slabs from meters to kilometers across. Wind drives them, but the ice spreads more slowly and moves more irregularly than wind alone predicts. The UCR-led study proposed one simple explanation: in densely packed ice, floes hit their neighbors far more often than the wind changes, and each collision dissipates energy. With measured wind and ice conditions and one extra weakly influential parameter, the granular model matched the Fram Strait observations. The next questions are about parameters. How do spreading rate and the shape of the speed distribution change with ice concentration, floe size, restitution coefficient, wind strength and wind correlation time? Where does the system move from collision-dominated to wind-dominated? That is a parameter study.

One simulation of about 2,000 floes is cheap: a couple of cores for an hour. Mapping the parameter space means a thousand or more independent runs. A Slurm array script already exists for the campus cluster, but a 1,000-task array competes with everyone else in the queue. The need is throughput without changing code.

## Who at UCR does this

The study was led by UCR engineering researchers in materials science and mechanical engineering. Similar work happens across the Bourns College of Engineering and in Earth and Planetary Sciences: granular and suspension flows, particle-laden inks for 3D printing, landslide and avalanche analogs, and sea-ice remote sensing. The UCR News story names those applications. The Ice Floe Tracker algorithm, which produces floe trajectories from satellite images, was also developed at UCR as doctoral work. None of these groups use nrp-mcp today; this is an illustration.

## The data

- **Simulation inputs.** One parameter table, `params.csv` with 1,000 rows of concentration, floe radius, restitution, wind speed, wind correlation time and seed. Each run writes floe trajectories and summary statistics, about 20-50 MB per run before thinning, or about 30 GB in total.
- **Validation data.** NSIDC Polar Pathfinder Daily 25 km EASE-Grid Sea Ice Motion Vectors, Version 4 (NSIDC-0116, https://nsidc.org/data/nsidc-0116/versions/4, DOI https://doi.org/10.5067/INAWUWO7QH7B): daily netCDF, 1978 through 2025. Downloads require a free NASA Earthdata login, so a plain `goal=pull` (anonymous curl) cannot fetch them. A Fram Strait subset is small enough for `nrp_data up` (under 100 MB). Floe-scale trajectories for the Fram Strait come from the Ice Floe Tracker dataset mentioned above.
- **P1 check.** Synthetic simulation output and public satellite products. Everything here is P1.

## The conversation

**Researcher:** ~/proj/floes has floes.sbatch, the array script I use on the HPCC. Run it on Nautilus for all 1,000 rows of params.csv.

**Assistant:** [nrp_plan project=~/proj/floes] nrp found a Slurm script, and here is what each line becomes:

| Slurm | Kubernetes |
|---|---|
| `--job-name=floes` | object name `floes` |
| `--array=1-1000%50` | Indexed Job, 1,000 tasks; each gets its own `$SLURM_ARRAY_TASK_ID` (1..1000), set from `$JOB_COMPLETION_INDEX`; `%50` becomes parallelism 50 |
| `--cpus-per-task=2`, `--mem=4G` | 2 CPU, 4 GiB request and limit |
| `--time=02:00:00` | activeDeadlineSeconds 7200 (2 h) |
| `-p intel` | no equivalent: Kubernetes picks a node that fits |
| `--output=logs/%A_%a.out` | ignored (use nrp_watch for logs) |
| `module load python/3.11` | replaced by the container image |

Your caps are fine as they are: 50 pods at the same time is within `pods_per_run` (50), and 1,000 tasks is well under `tasks_per_run` (10,000). One thing matters before you run it. On Kubernetes, the time limit applies to the whole Job, not to each task the way `--time` does in Slurm. One thousand tasks at 50 at a time is 20 rounds, about 20 to 25 hours, so a 2-hour deadline would stop the sweep after the second round. I'd plan with hours=36, which is under your 48 h cap. Your script stays the same and still reads `$SLURM_ARRAY_TASK_ID`, so it keeps running unchanged on the HPCC.

**Researcher:** Makes sense. Use 36 hours.

**Assistant:** First a volume for the output: [nrp_plan goal=volume size=100Gi data_is_p1=true] [nrp_run]. Then [nrp_plan project=~/proj/floes hours=36 data_volume=floes-data] Plan: an Indexed Job `floes` with 1,000 tasks, 50 at a time, image python:3.12-slim with requirements.txt (numpy, numba, pandas) installed at start, 2 CPU and 4 GiB each with limits equal to requests (required above 100 pods), output to /data/runs, 36 h limit, and no GPU. Your code is CPU numba, and a GPU would sit idle and get the account flagged. Run it?

**Researcher:** Yes.

**Assistant:** [nrp_run] Started, with a run card in ~/proj/floes/.nrp/runs/. Average task time so far is 58 minutes.

**Researcher:** Status?

**Assistant:** [nrp_watch] 612 succeeded, 50 running, 7 failed. All 7 exited with code 1, and the last log lines are `FloatingPointError: floe velocities non-finite at step 41210`. [nrp_watch grep="SLURM_ARRAY_TASK_ID="] They are tasks 88, 176, 264, 352, 440, 528 and 616. In params.csv all of them have wind speed 20 m/s and restitution 0.95. That points to the explicit time step being too large for fast, nearly elastic collisions, not a cluster problem. Fix: cap dt by the collision time, for example dt = min(dt, 0.05 * t_contact). When the sweep finishes, I'll rerun just those IDs. Your script takes an `--ids` list, so a 7-task sweep maps $JOB_COMPLETION_INDEX onto them.

**Researcher:** Patched. Rerun them.

**Assistant:** [nrp_watch] Main sweep: 993 succeeded. [nrp_plan project=~/proj/floes count=7 parallel=7 hours=4 data_volume=floes-data command="python sim.py --ids 88,176,264,352,440,528,616"] [nrp_run] All 7 succeeded in about 70 minutes. [nrp_plan goal=job command="python collect.py" data_volume=floes-data] [nrp_run] collect.py merges the 1,000 summaries into `phase_map.parquet` (40 MB) and compares spreading rates against the NSIDC-0116 Fram Strait subset you uploaded. [nrp_data down path=summary/] The results are in ~/proj/floes/summary. [nrp_cleanup] lists 4 runs. Confirm?

**Researcher:** Confirm. Keep the volume a month.

**Assistant:** [nrp_cleanup plan_id=... confirm_token=...] Deleted. The volume `floes-data` stays. Untouched volumes are purged after 6 months.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | volume | PVC, rook-cephfs-central, RWX | 100 GiB |
| Validation subset | nrp_data up | helper pod, kubectl cp | under 100 MB |
| Parameter study | sweep (from `#SBATCH --array=1-1000%50`) | Indexed Job, 1,000 tasks, 50 parallel | 2 CPU, 4 GiB each; about 1 h per task; 36 h Job limit |
| Rerun of failed IDs | sweep | Indexed Job, 7 tasks | same, 4 h limit |
| Collect and compare | job | Job | 2 CPU, 8 GiB, about 20 min |

## Compute and cost estimate

Assumptions: about 1 hour per run on 2 cores (2,000 floes, about 10^6 time steps, numba). The study is 1,000 task-hours, or 2,000 core-hours, with no GPUs. At 50 at a time it takes about 20-25 hours wall-clock. Raising parallelism to 100 roughly halves that, if CPU is free, but it means raising `pods_per_run` to 100 on purpose. Nautilus costs the user nothing.

For comparison, on a 10-core laptop it would take about 8 days of continuous running. On the HPCC, 2,000 core-hours is routine, and with the lab's share it may well be just as fast. The point of Nautilus here is overflow capacity when the queue is busy or the study grows tenfold, not that the HPCC can't do it. On commercial cloud, 2,000 core-hours cost roughly $60-$100 on demand, or less on spot instances, plus setup.

## Limits and honest caveats

- **The time limit is per Job, not per task.** nrp-mcp maps `--time` to the Job's activeDeadlineSeconds, which covers the whole sweep. Always size `hours` for all the rounds. The default cap is 48 h per run, so a longer study needs a higher cap or several sweeps over ID ranges.
- **The pod cap counts pods running at the same time.** `%50` fits the default `pods_per_run` of 50, and the 1,000 total counts against `tasks_per_run` (default 10,000). A higher `%` needs a lower parallel or `pods_per_run` raised on purpose. Above 100 pods in a run the NRP requires limits equal to requests, and nrp-mcp sets that automatically.
- **No MPI.** Lines like `--nodes`/`--ntasks` get a note that multi-node MPI belongs on the HPCC or Ursa Major. A single very large simulation, such as millions of floes with domain decomposition, is a poor fit for Nautilus. Many independent small runs are ideal.
- **What does not carry over.** Partitions, accounts, QOS, `--output` paths, mail notifications and job dependencies have no equivalent here. Logs come from nrp_watch, and anything you want to keep must be written to /data.
- **Data behind a login.** NSIDC needs an Earthdata login, which the anonymous `pull` goal does not support. Use small uploads, or NRP S3 for larger staging. UCR RC has not yet tested S3 with nrp-mcp.

## Starter kit

`floes.sbatch` (runs unchanged on the HPCC and through nrp)
```bash
#!/bin/bash
#SBATCH --job-name=floes
#SBATCH --array=1-1000%50
#SBATCH --cpus-per-task=2
#SBATCH --mem=4G
#SBATCH --time=02:00:00
#SBATCH -p intel
#SBATCH --output=logs/%A_%a.out
module load python/3.11
python sim.py --row $SLURM_ARRAY_TASK_ID --out ${OUT:-/data/runs}
```

`sim.py` (sketch)
```python
import argparse, os, numpy as np, pandas as pd
from floes import Floes, step               # lab code (numba kernels)
ap = argparse.ArgumentParser()
ap.add_argument("--row", type=int); ap.add_argument("--ids"); ap.add_argument("--out", default="/data/runs")
a = ap.parse_args()
row = a.row if a.ids is None else int(a.ids.split(",")[int(os.environ["JOB_COMPLETION_INDEX"])])
p = pd.read_csv("params.csv").iloc[row - 1]           # array ids are 1-based
out = f"{a.out}/{row:04d}.npz"
if os.path.exists(out): raise SystemExit(0)
rng = np.random.default_rng(int(p.seed))
f = Floes(n=2000, phi=p.concentration, radius=p.radius, e=p.restitution, rng=rng)
dt = min(1.0, 0.05 * f.contact_time())
traj = []
for t in range(1_000_000):
    step(f, dt, wind=p.wind, tau=p.wind_corr, rng=rng)
    if not np.isfinite(f.v).all(): raise FloatingPointError(f"non-finite at step {t}")
    if t % 500 == 0: traj.append(f.x.copy())
np.savez_compressed(out, traj=np.array(traj), params=p.values)
```

`requirements.txt`
```
numpy
numba
pandas
```

## README blurb

Bring an existing Slurm array script and run it as is. nrp-mcp reads the `#SBATCH` lines, shows what each becomes on Kubernetes and what has no equivalent, and turns `--array=1-1000%50` into an Indexed Job where every task keeps its own `$SLURM_ARRAY_TASK_ID`, 50 at a time. It warns that the Kubernetes time limit covers the whole sweep. nrp_watch names the failed task IDs and why, so only those get rerun, and the same script keeps working on your campus cluster.
