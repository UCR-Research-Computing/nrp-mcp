# 10. Cloud Droplet Activation: Parcel-Model Sweeps Over Trace-Gas Effects

**Field:** Atmospheric chemistry, aerosol-cloud interactions | **UCR connection:** "Trace gases play unseen role in cloud droplet formation" (UCR News, Feb. 9, 2026): a UCR-led Science Advances study at CE-CERT, with atmospheric chemists in the Bourns College of Engineering, showing that stripping trace organic gases from air samples changes how readily particles become cloud droplets: https://news.ucr.edu/articles/2026/02/09/trace-gases-play-unseen-role-cloud-droplet-formation ; paper: https://www.science.org/doi/10.1126/sciadv.adx0960 | **Fit:** Excellent

## The science

Cloud droplets form when rising air becomes slightly supersaturated and water condenses on aerosol particles, the cloud condensation nuclei (CCN). Kappa-Koehler theory says activation depends on particle size, hygroscopicity (kappa) and droplet surface tension. The UCR study, part of the DOE ARM Eastern Pacific Cloud Aerosol Precipitation Experiment (EPCAPE) near La Jolla, measured CCN activity with and without a denuder that removed trace organic gases. Removing them shifted CCN-derived hygroscopicity by up to 50%, up or down depending on relative humidity and gas concentration, and organic acids correlated with the effect. The modeling question: if a kappa perturbation of this size is real in the atmosphere, how much does it change droplet number in a rising cloud parcel, and under which updrafts and aerosol populations does it matter?

That is answered with an adiabatic parcel model over a large grid (updraft, aerosol number and size, kappa, kappa perturbation, surface-tension treatment). Each run takes seconds to a minute on one core, but a useful grid is 10,000-50,000 runs plus a particle-based cross-check: days on a laptop, hours on a few hundred cores. Every run is independent, so it is an ideal sweep.

## Who at UCR does this

The College of Engineering - Center for Environmental Research and Technology (CE-CERT, https://www.cert.ucr.edu/), which works on air pollution, emissions, renewable energy and transportation, and the Bourns College of Engineering's chemical and environmental engineering program. The study's co-authors include an atmospheric chemistry group at CE-CERT that combines field CCN measurements with Koehler-theory analysis. Other UCR atmospheric and environmental science groups run similar parcel and box models. None of these groups use nrp-mcp today; this is an illustration.

## The data

The sweep itself needs almost no input data: it is model physics plus parameter values.

- pyrcel, an adiabatic cloud parcel model implementing kappa-Koehler theory, BSD-3-Clause: https://github.com/darothen/pyrcel (docs https://pyrcel.readthedocs.io/). Version 2.0 has a JAX core; pin "pyrcel<2" if you need the 1.3 API.
- PySDM, a particle-based (super-droplet) microphysics package with parcel examples and several surface-tension models (constant, Szyszkowski-Langmuir, compressed film), GPL-3.0: https://github.com/open-atmos/PySDM (docs https://open-atmos.github.io/PySDM/).
- Optional observational constraints: EPCAPE campaign data from the ARM Data Center, https://www.arm.gov/research/campaigns/amf2023epcape (public after registration with ARM). Size distributions and CCN spectra from there can seed realistic aerosol modes. Download them into a volume with goal=pull where direct links exist.

All of this is P1. The lab's own instrument data stay wherever the lab keeps them unless already public; nothing here requires uploading them.

## The conversation

**Researcher:** I have parcel.py, which runs one pyrcel simulation from a parameter row. I want a full grid: 8 updrafts, 6 aerosol numbers, 5 median diameters, 5 kappas, 7 kappa perturbations from -50% to +50%, 2 surface-tension cases. Can Nautilus do it?

**Assistant:** That is 16,800 runs. [nrp_status] No CPU quota limits in your namespace; nothing running. I suggest 50 tasks of 336 grid points each, all 50 at once. One task per grid point would mean 16,800 container starts, each installing requirements and compiling JAX for a run of seconds, and 16,800 tasks is also over the default tasks_per_run cap of 10,000. Packing avoids both and stays within the default caps.

**Researcher:** Fine. Make each task read SLURM_ARRAY_TASK_ID and take its slice of the grid.

**Assistant:** Here is the change to parcel.py: build the grid with itertools.product, take rows task::50, and write one Parquet file per task to /data/runs/. Now storage: [nrp_plan goal=volume size=20Gi data_is_p1=true] Plan: one CephFS volume "parcel-runs", ReadWriteMany, mounted at /data. **Researcher:** Yes. [nrp_run] Created.

**Researcher:** Plan the sweep.

**Assistant:** [nrp_plan goal=sweep count=50 parallel=50 cpu=1 memory=2Gi data_volume=parcel-runs] Summary: an Indexed Job, 50 tasks, all at once, 1 CPU and 2 GiB each (limits equal requests; exempt from usage-violation checks), image python:3.12-slim, requirements.txt installed at start, /data mounted, 24 h limit. Each task gets $SLURM_ARRAY_TASK_ID 0..49. No GPU: the code is CPU-only. Confirm token valid 10 minutes. **Researcher:** Go. [nrp_run] Running. Run card in .nrp/runs/.

**Researcher:** How is it going?

**Assistant:** [nrp_watch] 41 tasks done, 7 running, 2 failed. Task 17 and task 33 exited 1. Last log lines: "SolverError: integration failed" at updraft 0.05 m/s with 5000 particles per cm3 and the largest kappa perturbation; the parcel never reached peak supersaturation before t_end. Fix: catch the error, retry that row with a longer t_end, and record a status column instead of crashing.

**Researcher:** Patched. Rerun only those two.

**Assistant:** [nrp_plan goal=job command="python parcel.py --tasks 17,33" data_volume=parcel-runs] One Job that redoes both slices in a row, 1 CPU and 2 GiB. **Researcher:** Yes. [nrp_run] [nrp_watch] Both succeeded; 16,800 rows, 23 marked "long_tend".

**Researcher:** Now combine everything into one file and add the PySDM cross-check on 500 points.

**Assistant:** [nrp_plan goal=job command="python combine.py" data_volume=parcel-runs cpu=2 memory=8Gi] combines the 50 Parquet files into grid.parquet with droplet number, peak supersaturation and activated fraction. Then [nrp_plan goal=sweep count=50 parallel=50 cpu=2 memory=4Gi command="python pysdm_check.py" data_volume=parcel-runs] runs 10 PySDM parcels per task. Both approved and run.

**Researcher:** Bring it home and clean up, keep the volume for a week.

**Assistant:** [nrp_data down volume=parcel-runs path=summary/] 48 MB: grid.parquet, pysdm_check.parquet, logs. [nrp_cleanup] lists 4 runs; after your second yes they are deleted. The volume stays; open analysis.ipynb locally.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PVC (CephFS, RWX) | 20 GiB |
| Optional EPCAPE inputs | goal=pull | Job (curl) | 1 CPU, 2 GiB, minutes |
| Main grid | goal=sweep | Indexed Job | 50 tasks x 1 CPU, 2 GiB; 50 at once; 336 runs each, about 1-3 h |
| Rerun failures | goal=job | Job | 1 CPU, 2 GiB, 2 slices |
| Combine | goal=job | Job | 2 CPU, 8 GiB, minutes |
| PySDM cross-check | goal=sweep | Indexed Job | 50 tasks x 2 CPU, 4 GiB |
| Results home | nrp_data down | helper pod | about 50 MB |

## Compute and cost estimate

Assumptions: a pyrcel run with a 50-200 bin aerosol takes about 5-30 s on one core (longer at low updraft and high number); call it 20 s average. 16,800 runs x 20 s is about 93 CPU-hours. Spread over 50 tasks that is about 2 hours of wall clock, plus JAX compile time at each task's start. The PySDM check (500 runs at a few minutes each) adds roughly 25-40 CPU-hours. Total about 130 CPU-hours, done in an afternoon. Nautilus is free to the user. An 8-core laptop would be tied up most of a day per grid. The HPCC does this well as a job array; Nautilus is overflow when its queue is busy or the group has no HPCC account. A cloud VM at about $0.04 per core-hour costs about $5, but needs an account and someone to manage it.

## Limits and honest caveats

- The default caps limit pods running at the same time (pods_per_run, 50) and the number of tasks in one sweep (tasks_per_run, 10,000). Packing grid points per task, as here, stays inside both and avoids thousands of short container starts; one task per point would exceed tasks_per_run for this grid.
- Parcel models are not cloud-resolving; multi-node MPI large-eddy runs belong on the HPCC or Ursa Major.
- The mechanism is not yet identified; modeling it as a kappa perturbation or surface-tension change is an assumption to state clearly.
- ARM data need an ARM account; check for direct links before using goal=pull.
- Nautilus purges inactive volumes after 6 months and is not an archive; copy results home.
- pyrcel 2.0 changed the API; pin the version so all tasks use the same code.

## Starter kit

Sketch: `parcel.py`

```python
import os, sys, itertools, pandas as pd, pyrcel as pm
V = [0.05, 0.1, 0.2, 0.5, 1, 2, 3, 5]              # updraft, m/s
N = [100, 300, 1000, 2000, 3500, 5000]              # cm-3
MU = [0.02, 0.04, 0.06, 0.08, 0.1]                  # median radius, um
KAPPA = [0.1, 0.2, 0.3, 0.45, 0.6]
DK = [-0.5, -0.3, -0.15, 0, 0.15, 0.3, 0.5]         # relative kappa change
SIGMA = ["water", "reduced"]
grid = list(itertools.product(V, N, MU, KAPPA, DK, SIGMA))
tasks = [int(t) for t in sys.argv[2].split(",")] if "--tasks" in sys.argv \
        else [int(os.environ["SLURM_ARRAY_TASK_ID"])]
for task in tasks:
    rows = []
    for v, n, mu, k, dk, sig in grid[task::50]:
        aer = pm.AerosolSpecies("mix", pm.Lognorm(mu=mu, sigma=1.8, N=n), kappa=k*(1+dk), bins=100)
        try:
            out = pm.ParcelModel([aer], V=v, T0=283.0, S0=-0.02, P0=95000.0).run(t_end=600.0 / v, output_dt=1.0, terminate=True)
            rows.append(dict(v=v, n=n, mu=mu, kappa=k, dk=dk, sigma=sig, smax=out.summary["S_max"], nd=out.Nd, status="ok"))
        except Exception as e:                       # retry logic goes here
            rows.append(dict(v=v, n=n, mu=mu, kappa=k, dk=dk, sigma=sig, status=type(e).__name__))
    os.makedirs("/data/runs", exist_ok=True)
    pd.DataFrame(rows).to_parquet(f"/data/runs/task_{task:03d}.parquet")
```

(The "reduced" surface-tension case needs a custom Koehler curve or PySDM's surface-tension models; the sketch only carries the label.)

`requirements.txt`

```
pyrcel>=2,<3
pandas
pyarrow
```

`combine.py` reads /data/runs/*.parquet with pandas, concatenates, and writes /data/summary/grid.parquet plus a CSV of failed rows.

## README blurb

Parcel and box models are cheap per run but useful only over large parameter grids. nrp-mcp turns a script that reads its task number into a CPU sweep on Nautilus, packs thousands of runs into a few dozen tasks, and names the tasks that failed and why so only those are rerun. Results land on a shared volume, get combined in one more job, and come back to the laptop as a single file. Everything is planned, shown and confirmed before anything runs, and cleaned up the same way.
