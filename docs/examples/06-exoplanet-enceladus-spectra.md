# 06. Exoplanet biosignature model grids and Enceladus ice-grain spectra

**Field:** Astrobiology, planetary science | **UCR connection:** exoplanet atmosphere and biosignature modeling in Earth and Planetary Sciences (Alternative Earths Astrobiology Center, https://altearths.ucr.edu/), and the Cassini Cosmic Dust Analyzer study of Enceladus plume grains with a UCR co-author (Science Advances, Sept. 2026; UCR News, https://news.ucr.edu/articles/2026/09/28/saturns-moon-offers-clues-search-extraterrestrial-life) | **Fit:** Good

## The science

Will a telescope be able to tell a living Earth-like planet from a lifeless one? Answering that means modeling many possible worlds. A biosignature gas such as nitrous oxide or methyl bromide only builds up to detectable levels for some combinations of star type, biological production rate, ultraviolet flux, clouds and background atmosphere. Each case needs a photochemical model run to steady state and then a radiative-transfer calculation of the spectrum a telescope would see. One case takes minutes to an hour on one core. A useful study crosses five or six parameters, which quickly becomes thousands of independent cases.

Closer to home, Enceladus sprays its subsurface ocean into space. The new study examined nearly 1,000 salt-rich ice grains recorded by Cassini's Cosmic Dust Analyzer (CDA) and found that freezing sorts salts, and perhaps organics, into different grains. The lesson for future missions is to analyze grains one by one, because a biosignature may sit in only a few. Turning that into numbers means classifying every grain spectrum and testing how often rare compositions show up by chance. The spectra are small, but resampling and comparing them against libraries of laboratory analog spectra multiplies the work. Both problems are many independent CPU tasks, which is what Nautilus does well.

## Who at UCR does this

The Department of Earth and Planetary Sciences hosts exoplanet atmosphere modeling (climate, photochemistry, radiative transfer, synthetic spectra) and the Alternative Earths Astrobiology Center, which uses Earth's history to guide the search for life elsewhere. A new Planetary Geochemistry and Astrobiology Laboratory in the department (Inside UCR, https://insideucr.ucr.edu/stories/2026/08/21/new-instruments-put-ucr-cutting-edge-search-life-beyond-earth) runs laboratory experiments on icy-moon chemistry and how spacecraft mass spectrometers could tell biological from non-biological signals. Physics and Astronomy groups that model planets and stars have similar grid problems. None of these groups use nrp-mcp today; this is an illustration.

## The data

- Model codes (all public): NASA's Planetary Spectrum Generator (https://psg.gsfc.nasa.gov/), which also ships as an installable Docker image (https://hub.docker.com/r/nasapsg/psg); petitRADTRANS, MIT-licensed (https://petitradtrans.readthedocs.io/); and the Atmos coupled photochemistry-climate code in Fortran (https://github.com/VirtualPlanetaryLaboratory/atmos; the repository states no license, so check terms with its maintainers before redistributing a built image).
- Opacity and cross-section tables for the radiative-transfer code: several to tens of GB depending on species and resolution, downloaded from the code's documented sources.
- Stellar parameters: the NASA Exoplanet Archive (https://exoplanetarchive.ipac.caltech.edu/).
- Cassini CDA data: NASA Planetary Data System, data set CO-D-CDA-3/4/5-DUST-V1.0 (https://pds.nasa.gov/ds-view/pds/viewProfile.jsp?dsid=CO-D-CDA-3%2F4%2F5-DUST-V1.0), served by the PDS Small Bodies Node (https://sbn.psi.edu/pds/archive/cassini.html). It includes time-of-flight mass spectra signal tables for individual impacts. Early deliveries note that some derived quantities were held back pending calibration, so check which products the current archive contains. A curated grain-by-grain set like the one in the paper may come from the instrument team rather than PDS.
- P1 check: all public NASA data and public code. Nothing sensitive. Unpublished collaborator data covered by a team agreement should not go to Nautilus without the team's agreement, even if it isn't sensitive.

## The conversation

**Researcher:** My project folder has `grid.py`, which runs Atmos photochemistry and then petitRADTRANS for one parameter set, plus `params.csv` with 1,200 rows: star type, N2O flux, CH3Br flux, cloud cover and surface pressure. Run all of them on CPU.

**Assistant:** [nrp_status] Namespace `exo-biosig`, signed in, nothing running. I need a shared volume for the opacity tables and the outputs. [nrp_plan goal=volume size=200Gi data_is_p1=true] then [nrp_plan goal=pull urls=[...opacity tables...] data_volume=exo-grid subdir=opacities data_is_p1=true] Plans: a 200 GiB CephFS volume, then an in-cluster download Job, so the tables never pass through your laptop. Approve both?

**Researcher:** Yes.

**Assistant:** [nrp_run ...] [nrp_run ...] Both done; 31 GB of tables in `/data/opacities`. Now the grid, one task per row: `grid.py` reads `$JOB_COMPLETION_INDEX` and runs that row. [nrp_plan goal=sweep count=1200 parallel=50 cpu=2 memory=4Gi hours=24 data_volume=exo-grid] Plan summary: "A sweep of 1200 tasks, no GPU, 2 CPU and 4 GiB each, limits equal to requests. Indexed Job: 1200 tasks, 50 at a time. Each task gets its own number in $SLURM_ARRAY_TASK_ID (0..1199) and $JOB_COMPLETION_INDEX (0..1199). Copies 4 project file(s) into the pod with a ConfigMap. Image python:3.12-slim; requirements installed at start." This fits the default caps: 50 pods at the same time, 1,200 tasks in total. Because the run has more than 100 pods, the NRP requires limits to equal requests, and nrp-mcp sets that for you. That is 24 waves of 50 (100 cores at peak) at 10 to 20 minutes per case, about 6 hours; the 24-hour limit covers the whole sweep, not each task. Approve?

**Researcher:** Go.

**Assistant:** [nrp_run ...] [nrp_watch target=run-c41e] The Job failed within two minutes: every task hit the same error, the retry budget ran out, and Kubernetes stopped the rest. Diagnosis: "The program failed", and the last 40 log lines show `make: gfortran: No such file or directory`. Atmos is Fortran and the slim Python image has no compiler. Installing a compiler at every start would also be slow. Better to build an image once. [nrp_build project=.] It suggests a Dockerfile and a `.gitlab-ci.yml` that builds with kaniko on NRP GitLab into gitlab-registry.nrp-nautilus.io; nrp_build writes nothing itself, so I saved both into your folder and added two lines to the Dockerfile: install gfortran, and compile Atmos at build time. Push the folder to an NRP GitLab project, wait for the pipeline, and tell me the image name.

**Researcher:** It's `gitlab-registry.nrp-nautilus.io/exo-biosig/atmos-prt:v1`.

**Assistant:** [nrp_cleanup run=run-c41e] lists the failed run; a second yes deletes it. Then [nrp_plan goal=sweep count=1200 parallel=50 cpu=2 memory=4Gi hours=24 image=gitlab-registry.nrp-nautilus.io/exo-biosig/atmos-prt:v1 data_volume=exo-grid] [nrp_run ...] Six hours later: [nrp_watch target=run-d902] "Failed: backoff limit reached. 1150 succeeded." Task 731 was `OOMKilled` five times: that high-pressure, cloudy case at full resolution needs more than 4 GiB, and its retries used up the Job's retry budget, so Kubernetes stopped the tasks still running. Suggested fix: raise memory 50%. Your script skips cases that already have output, so I will clean up this run and plan the same 1,200-task sweep with 6 GiB; the finished cases exit in seconds and only the 50 unfinished ones do real work. [nrp_cleanup run=run-d902] [nrp_plan goal=sweep count=1200 parallel=50 memory=6Gi image=... data_volume=exo-grid] [nrp_run ...] Done in 50 minutes. Your script also wrote `converged=false` for 38 photochemistry cases; those are physics to look at, not cluster errors.

**Researcher:** Now I want a notebook for the plots and the CDA grain clustering.

**Assistant:** [nrp_plan goal=session data_volume=exo-grid cpu=4 memory=16Gi] A private Jupyter on the same volume, reached by port-forward, never public. [nrp_run ...] [nrp_session run=run-e1a7] gives `kubectl port-forward ...` and a local URL with its sign-in token. The CDA spectra tables are small; for the resampling test (10,000 bootstrap draws of grain classes) we can come back to a sweep if the notebook is slow. At the end: [nrp_data down volume=exo-grid path=/out/spectra_summary.parquet] then [nrp_cleanup] for the session.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PVC (CephFS, RWX) | 200 GiB |
| Opacity tables | nrp_plan goal=pull | Job (download) | ~31 GB, minutes to an hour |
| First grid try | nrp_plan goal=sweep | Indexed Job + ConfigMap | 1,200 tasks (50 at a time) x 2 CPU / 4 GiB; failed (no compiler) |
| Image | nrp_build | none (Dockerfile + GitLab CI) | built on NRP GitLab |
| Model grid | nrp_plan goal=sweep | Indexed Job | 1,200 tasks (50 at a time) x 2 CPU / 4 GiB, ~6 h |
| Rerun | nrp_plan goal=sweep | Indexed Job | 1,200 tasks x 2 CPU / 6 GiB (1,150 skip), <1 h |
| Analysis | nrp_plan goal=session + nrp_session | Deployment + Secret (port-forward) | 4 CPU / 16 GiB, private |

## Compute and cost estimate

Assumptions: 1,200 cases at 10 to 20 minutes each (photochemistry to steady state plus one spectrum) on 2 cores, so roughly 300 core-hours. With 50 tasks at once (100 cores at peak), 24 waves take about 6 hours of wall-clock; nrp-mcp's default 24-hour limit, which covers all the waves, leaves room. No GPUs. Free to the user.

Comparison: a 12-core workstation would take about a day and a half, and longer if the photochemistry struggles to converge. The UCR HPCC handles this well as a Slurm array ($1,000 per lab per year) and is the better home if the lab already has an account and wants storage next to its other work. Ursa Major on Google Cloud would cost very roughly $10 to $30 in compute for 300 core-hours, depending on machine type and discounts. Nautilus wins when the lab has no allocation or needs a burst of a few thousand cores for a revision deadline.

## Limits and honest caveats

- The pod cap (50 by default) limits how many pods run at the same time, not the total, so a 1,200-case grid runs one case per task, 50 at a time (up to 10,000 tasks per sweep). The Job time limit covers the whole sweep, so size `hours` for all the waves.
- 3D climate models (GCMs) run as tightly coupled MPI jobs and do not fit Nautilus well. Use the HPCC or Ursa Major for those; Nautilus suits the many 1D cases around them.
- PSG's public API is not for bulk grids. Run the installable PSG image as a sidecar or use a local radiative-transfer code. nrp-mcp does not set up sidecars; that needs a hand-written manifest.
- License check: Atmos has no stated license. Ask before pushing a public image that contains it, or keep the registry project private (then the namespace needs a pull secret).
- CDA archive contents: confirm which spectra products the PDS archive holds before planning around them.
- Volumes are purged after 6 months of inactivity. Copy the final grid to UCR storage or a data repository (for example Zenodo) with the paper.

## Starter kit

Sketch, not tested code.

`grid.py`
```python
import csv, json, os, pathlib, subprocess
ROWS_PER_TASK = 1                                 # one case per task
i = int(os.environ.get("JOB_COMPLETION_INDEX", "0"))
rows = list(csv.DictReader(open("params.csv")))[i*ROWS_PER_TASK:(i+1)*ROWS_PER_TASK]
out = pathlib.Path("/data/out"); out.mkdir(parents=True, exist_ok=True)

for r in rows:
    tag = f"case{r['id']}"
    if (out / f"{tag}.json").exists():
        continue                                  # resume after a retry
    subprocess.run(["./run_atmos.sh", r["star"], r["n2o_flux"], r["ch3br_flux"],
                    r["clouds"], r["psurf"], tag], check=True)
    from spectrum import emission_and_transit       # petitRADTRANS wrapper
    spec, converged = emission_and_transit(f"atmos_out/{tag}", "/data/opacities")
    spec.to_parquet(out / f"{tag}.parquet")
    (out / f"{tag}.json").write_text(json.dumps({**r, "converged": converged}))
```

`Dockerfile` (what nrp_build would suggest, simplified)
```dockerfile
FROM python:3.12
RUN apt-get update && apt-get install -y gfortran make && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/VirtualPlanetaryLaboratory/atmos /opt/atmos \
 && cd /opt/atmos && make -f PhotoMake
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
```

`requirements.txt`
```
petitRADTRANS
numpy
pandas
pyarrow
```

## README blurb

Run a grid of thousands of planetary atmosphere models, with photochemistry and radiative transfer for each parameter set, as one indexed sweep on free national CPUs. Large opacity tables are downloaded straight into a shared volume, a Fortran code is built once into an image on NRP GitLab, and a failed or out-of-memory task is diagnosed and rerun alone. A private Jupyter session on the same volume handles the plots and the analysis of public mission spectra.
