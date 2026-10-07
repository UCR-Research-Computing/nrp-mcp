# 02. Atmospheric rivers across a CMIP6 climate-model ensemble

**Field:** Climate dynamics, hydroclimate, extreme weather | **UCR connection:** Modeled on "Slowing Atlantic current fueling stronger California storms" (UCR News, July 8, 2026, https://news.ucr.edu/articles/2026/07/08/slowing-atlantic-current-fueling-stronger-california-storms) and the Nature Communications paper "Atlantic meridional overturning circulation slowdown modulates atmospheric rivers in a warmer climate" (https://doi.org/10.1038/s41467-026-72555-w). A UCR climate dynamics doctoral study found that a weakening AMOC strengthens atmospheric rivers along the California coast by the end of the century. | **Fit:** Good

## The science

Atmospheric rivers (ARs) are long, narrow bands of water vapor that carry moisture from the tropics to higher latitudes. They supply much of California's water, and the strongest ones cause floods. The UCR study used coupled climate model simulations to isolate how a slowing Atlantic Meridional Overturning Circulation (AMOC) changes ARs. It found more frequent ARs and more AR-driven winter rain along the North American west coast, and fewer ARs over Greenland and the Arctic. A natural next question is how robust the result is across models. Do the CMIP6 models with the largest AMOC decline also show the largest change in California ARs? And how much does the answer depend on the detection method?

Compute is the bottleneck because of data volume, not arithmetic. Detecting ARs means first computing integrated vapor transport (IVT): specific humidity times wind, integrated through the depth of the atmosphere at every grid point and time step. That needs daily 3D fields for 150 years for every model. One model at about 1 degree is a few hundred gigabytes of input; thirty models are several terabytes. Moving that data is the slow part. Detection is a modest, independent CPU job per model.

## Who at UCR does this

The study comes from the climate dynamics group in Earth and Planetary Sciences (https://epsci.ucr.edu/research/global-climate), where faculty work on the AMOC, storm tracks, hydroclimate extremes and aerosols. Related work includes regional climate and air-quality modeling at CE-CERT. Such groups typically analyze multi-model archives in Python (xarray, dask). None of these groups use nrp-mcp today; this is an illustration.

## The data

- **CMIP6 on Google Cloud.** Pangeo and Columbia LDEO maintain an analysis-ready Zarr copy of CMIP6 in the public bucket `gs://cmip6`. It is free to read, and the catalog is at https://storage.googleapis.com/cmip6/pangeo-cmip6.csv (about 80 MB, 514,000 stores; overview at https://pangeo-data.github.io/pangeo-cmip6-cloud/ and https://console.cloud.google.com/marketplace/product/noaa-public/cmip6). When we checked in October 2026, 30 models had daily `hus`, `ua` and `va` on pressure levels for both `historical` and `ssp585`, including CESM2, GFDL-CM4, MPI-ESM1-2-HR, MIROC6, UKESM1-0-LL and EC-Earth3. Thirteen of those also publish the overturning streamfunction needed for an AMOC index. Pangeo notes that the copy is incomplete and that a "Zarr Data 2.0" catalog is in beta.
- **ESGF.** The original netCDF files are at https://metagrid.esgf-west.org/search/cmip6/ (background at https://pcmdi.llnl.gov/CMIP6/). This is the fallback for models or variables missing from the cloud copy.
- **Sizes.** At about 1 degree, the three daily variables on 8 pressure levels come to roughly 0.6 GB per variable per year uncompressed. That is about 300 GB per model for 1950-2100, and several times more for the 0.5-degree "HR" models. The derived output is much smaller: daily 2D IVT plus an AR mask is about 10-20 GB per model, compressed.
- **P1 check.** Public model output. Everything here is P1.

## The conversation

**Researcher:** I want IVT and AR masks for every CMIP6 model with daily hus/ua/va, historical plus SSP5-8.5, 1950 to 2100. My detector is in ~/proj/ar-ensemble. Then I want a notebook on the results.

**Assistant:** [nrp_status] You're in namespace `ucr-climate`, nothing is running, and you have no GPU work, which is right because this is CPU work. Your `detect.py` reads `models.txt` (30 lines) and picks its line from $JOB_COMPLETION_INDEX. It streams Zarr straight from `gs://cmip6` with gcsfs (anonymous), so there's nothing to pull first. The `pull` goal downloads plain files with curl, and a Zarr store is thousands of chunk files, so streaming is the right choice here. Plans: [nrp_plan goal=volume size=800Gi data_is_p1=true] creates a shared CephFS volume `ar-ensemble-data`. [nrp_plan goal=sweep count=30 parallel=30 gpu=0 cpu=4 memory=16Gi hours=24 data_volume=ar-ensemble-data] creates an Indexed Job of 30 tasks with image python:3.12-slim, requirements.txt installed at start, limits equal requests, and a 24 h limit on the whole sweep. One warning: requests over 1 CPU and 2 GiB are subject to the NRP usage checks, so the code should actually keep 4 cores busy. Your dask config uses 4 threads, so it will. Run both?

**Researcher:** Yes.

**Assistant:** [nrp_run] x2. Running. Each task processes one year at a time and writes `/data/ivt/<model>/<year>.zarr`, then the AR mask.

**Researcher:** How's it going?

**Assistant:** [nrp_watch] 22 of 30 finished in 3 to 5 hours each. Four are still running (the HR models). Task 6 failed: `OOMKilled`. It ran out of memory at its 16 GiB limit. That is CNRM-CM6-1-HR, at about 0.5 degree, so one year of three 3D variables is about four times the 1-degree size. Fix: raise memory 50% and rerun only that model. I'd go to 32 GiB to leave headroom. [nrp_plan goal=job command="MODEL_INDEX=6 python detect.py" cpu=4 memory=32Gi data_volume=ar-ensemble-data] A single Job, same image. Your script skips years already written. Run it?

**Researcher:** Go. And make me the notebook.

**Assistant:** [nrp_run] Then [nrp_plan goal=session data_volume=ar-ensemble-data cpu=2 memory=8Gi] Plan: a private JupyterLab (quay.io/jupyter/scipy-notebook) as a Deployment with 1 replica, mounting the volume at /data. It is never public. Two warnings: Deployments are removed after 2 weeks, and idle sessions should be shut down. [nrp_run] [nrp_session] It's ready. Run `kubectl -n ucr-climate port-forward deploy/... 8888:8888`, then open the local URL with its sign-in token.

**Researcher:** Done with the figures. Bring back the ensemble summary and tear down the compute.

**Assistant:** [nrp_data down path=summary/] This copied `ar_freq_change_by_model.nc` (12 MB) and the PNGs into ~/proj/ar-ensemble/summary. [nrp_cleanup] lists the sweep, the rerun Job and the session. [nrp_cleanup plan_id=... confirm_token=...] Deleted. I left the 600 GB of IVT on the volume. If you want to keep it past this paper, copy it to permanent storage, because volumes untouched for 6 months can be purged.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | volume | PVC, rook-cephfs-central, RWX | 800 GiB |
| IVT + AR detection | sweep | Indexed Job, 30 tasks, 30 parallel | 4 CPU, 16 GiB each, 0 GPU, 3-8 h per task |
| Rerun of one HR model | job | Job | 4 CPU, 32 GiB, about 10 h |
| Figures | session | Deployment (1 replica), port-forward only | 2 CPU, 8 GiB, days |
| Results home | nrp_data down | helper pod, kubectl cp | tens of MB |

## Compute and cost estimate

Assumptions: each task streams about 300 GB (1-degree models) to about 1.2 TB (HR models) from Google Cloud at 50-150 MB/s, and the IVT computation keeps 4 cores busy. Most models finish in 3 to 5 hours and the HR models in 8 to 10. The whole ensemble takes about 120 to 160 task-hours, or about 500-650 core-hours, and finishes overnight. It needs no GPUs, and Nautilus costs the user nothing.

For comparison: on a laptop the roughly 10 TB download alone takes weeks and does not fit. On the HPCC the compute is easy, but staging the data means days of transfers and terabytes of quota. Commercial cloud next to the bucket costs roughly $25-$40 for 600 core-hours, plus account setup. On Nautilus, only figures and a 12 MB summary reach the laptop.

## Limits and honest caveats

- **Daily data is coarser than standard AR practice.** Most AR detectors expect 6-hourly IVT on many levels, while the CMIP6 `day` table gives daily means on 8 pressure levels. Daily means smooth out short ARs. Treat the ensemble as a robustness check, not a replacement for the 6-hourly analysis. 6-hourly 3D fields mostly mean going to ESGF, with much larger downloads. The detection method itself is a known source of uncertainty (the ARTMIP project compares methods), so consider running two detectors.
- **Gaps in the cloud copy.** Not every model, member or variable is on `gs://cmip6`. Anything missing must come from ESGF as netCDF files. `goal=pull` can fetch a list of http(s) file URLs into the volume.
- **Network-bound tasks.** Throughput from Google Cloud depends on the node; nrp cannot pick nodes for bandwidth.
- **Volume purge and size.** CephFS is not an archive. Thousands of small Zarr chunk files also stress CephFS metadata, so use larger chunks (one year per store) or consolidate.
- **Sessions are short-lived.** The session is a Deployment, and the NRP removes it after 2 weeks. For zero-install notebooks without nrp, the NRP JupyterHub (https://jupyterhub-west.nrp-nautilus.io) is an alternative.
- **Not for running the climate model.** Coupled model integrations (CESM and similar) are tightly coupled MPI codes. They belong on the HPCC, Ursa Major, or a national allocation (ACCESS, NCAR), not on Nautilus.

## Starter kit

`models.txt`: 30 lines, one CMIP6 `source_id` per line, from the catalog query.

`requirements.txt`
```
xarray
zarr
gcsfs
dask
pandas
scipy
```

`detect.py` (sketch)
```python
import os, pandas as pd, xarray as xr, numpy as np
models = open("models.txt").read().split()
i = int(os.environ.get("MODEL_INDEX", os.environ["JOB_COMPLETION_INDEX"]))
model = models[i]
cat = pd.read_csv("https://storage.googleapis.com/cmip6/pangeo-cmip6.csv")
def open_var(exp, var):
    row = cat.query("source_id==@model and experiment_id==@exp and table_id=='day' "
                    "and variable_id==@var and member_id=='r1i1p1f1'").iloc[0]
    return xr.open_zarr(row.zstore, storage_options={"token": "anon"})[var]
for exp, years in [("historical", range(1950, 2015)), ("ssp585", range(2015, 2101))]:
    q, u, v = (open_var(exp, x) for x in ("hus", "ua", "va"))
    for y in years:
        out = f"/data/ivt/{model}/{y}.zarr"
        if os.path.exists(out):
            continue
        sl = dict(time=str(y), plev=slice(100000, 30000))
        qy, uy, vy = q.sel(**sl).load(), u.sel(**sl).load(), v.sel(**sl).load()
        dp = -np.gradient(qy.plev)                       # Pa
        ivt = np.hypot((qy*uy*dp[:, None, None]).sum("plev"),
                       (qy*vy*dp[:, None, None]).sum("plev")) / 9.81
        mask = detect_ar(ivt)                            # lab detector
        xr.Dataset({"ivt": ivt, "ar": mask}).to_zarr(out)
```

## README blurb

Climate ensembles without the download: a sweep with one CPU task per model streams public CMIP6 Zarr data from the cloud and computes integrated vapor transport and atmospheric-river masks into a shared Nautilus volume. A private Jupyter session on the same volume is used for figures. nrp-mcp explains failures such as out-of-memory on high-resolution models and suggests a size that fits. It brings back only the summary files and cleans up after a second confirmation.
