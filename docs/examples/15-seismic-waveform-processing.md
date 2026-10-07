# 15. Deep-Learning Earthquake Detection on Years of Southern California Waveforms

**Field:** Seismology, earthquake physics | **UCR connection:** UCR Earth and Planetary Sciences' Earthquakes and Geophysics program, whose earthquake source seismology work studies slow earthquakes, tremor and low-frequency earthquakes with seismic array techniques (https://epsci.ucr.edu/research/earthquakes-geophysics) | **Fit:** Excellent

## The science

Standard earthquake catalogs miss most small earthquakes. Deep-learning phase pickers such as PhaseNet and EQTransformer, run over continuous seismic recordings, routinely find several times more events than the routine catalog, and those small events light up fault structure, foreshock and aftershock sequences, swarms and slow-slip-related seismicity. The research question here is how seismicity along a fault system evolves over years: where small earthquakes cluster, how they migrate, and whether they change before and after larger events.

Compute is the bottleneck because the input is enormous and the work is perfectly independent. One three-component broadband station records about 60 MB of data per day at 100 samples per second; 40 stations over 5 years is about 73,000 station-days and over 4 TB of waveforms. Each station-day can be read, picked and written without looking at any other. On one workstation that is weeks of downloading and inference; spread across a cluster with the data read in place, it is about a day.

## Who at UCR does this

The Department of Earth and Planetary Sciences (https://epsci.ucr.edu/) runs an Earthquakes and Geophysics program covering earthquake source seismology, dynamic rupture and tsunami modeling, space geodesy, fault system simulation, neotectonics and Earth structure. The department's program page lists faculty working in earthquake source seismology, including slow earthquakes, tremor, low-frequency earthquakes and new seismic array techniques.

None of these groups use nrp-mcp today; this is an illustration.

## The data

- **SCEDC on the AWS Open Data registry** (https://registry.opendata.aws/southern-california-earthquakes/): waveforms from the Southern California Seismic Network archived by the Southern California Earthquake Data Center, in the public bucket `s3://scedc-pds`, readable anonymously over HTTPS. Layout and file naming are documented at https://scedc.caltech.edu/data/cloud.html: `continuous_waveforms/YYYY/YYYY_DOY/` holds one miniSEED file per channel per day, for example `continuous_waveforms/2019/2019_185/CIADO__HHZ___2019185.ms` (about 20 MB for a 100 Hz channel-day). Data may contain gaps.
- **Station metadata:** SCEDC FDSN station service (https://service.scedc.caltech.edu/fdsnws/station/1/).
- **Reference catalog** for comparison: SCEDC earthquake catalogs (https://scedc.caltech.edu/eq-catalogs/).
- **Models:** SeisBench (https://github.com/seisbench/seisbench, docs https://seisbench.readthedocs.io/) provides PhaseNet and EQTransformer with pretrained weights, including PhaseNet weights trained on SCEDC data (`PhaseNet.from_pretrained("scedc")`).
- **Association:** GaMMA (https://github.com/AI4EPS/GaMMA) or PyOcto (https://github.com/yetinam/pyocto) turn picks into located events.

**P1 check:** Public network waveforms and catalogs are P1. The SCEDC license allows use and public display; cite SCEDC (doi:10.7909/C3WD3xH1) and SCSN (doi:10.7914/SN/CI). Data from a temporary deployment on private land under an access agreement, or embargoed data, should be checked with RC first.

## The conversation

**Researcher:** I want PhaseNet picks for 40 broadband stations around the San Jacinto fault for 2019 through 2023, then a catalog to compare with SCEDC.

**Assistant:** This is a good Nautilus job: public data, independent pieces. [nrp_status] Namespace `ucr-seismo`, nothing running. The work unit is a station-day, but 73,000 tiny pods would spend more time starting than computing, so each task should take one station-year and loop over its days, skipping days already done. That is 40 x 5 = 200 tasks.

**Researcher:** Should we use GPUs? PhaseNet is a neural network.

**Assistant:** PhaseNet is small. On 4 CPU cores a station-day takes about 20-30 seconds including the download, and a GPU would mostly wait on the network; the NRP flags GPUs under 40% utilization. Start on CPU. Also, nrp-mcp's default cap is 4 GPUs in use at the same time, so a 200-task GPU sweep would run at most 4 tasks at once (parallel=4, 50 waves) unless you raise `gpus_per_run`.

**Researcher:** OK. Here is the folder: `pick_station_year.py`, `stations.txt`, `requirements.txt`.

**Assistant:** A small volume for results and a model cache first. [nrp_plan goal=volume name=seis size=200Gi data_is_p1=true] Approve? **Researcher:** Yes. [nrp_run] Now a 4-task pilot (one station, 4 years). [nrp_plan goal=sweep count=4 parallel=4 cpu=4 memory=8Gi data_volume=seis command="python pick_station_year.py"] Summary: Indexed Job, `python:3.12-slim` with `requirements.txt` installed at start, no GPU, each task reads $SLURM_ARRAY_TASK_ID and streams its days from the SCEDC bucket into the pod, writing picks to /data/picks. Approve? **Researcher:** Yes. [nrp_run]

**Researcher:** How did the pilot go?

**Assistant:** [nrp_watch target=<run>] 3 succeeded, 1 failed after 2 retries, exit 1. Last log lines: `ConnectionError ... hifis-storage.desy.de ... Read timed out` while downloading PhaseNet weights. Each pod also spent several minutes installing SeisBench and PyTorch. With 200 tasks, that is 200 installs and 200 weight downloads from a shared server. Fix: build an image with SeisBench preinstalled, and keep the weights on the volume by setting `SEISBENCH_CACHE_ROOT=/data/sbcache`. [nrp_build project=.] I wrote a `Dockerfile` (FROM pytorch/pytorch, pip install seisbench) and a `.gitlab-ci.yml`. Push the folder to a project on gitlab.nrp-nautilus.io; kaniko builds `gitlab-registry.nrp-nautilus.io/<you>/seis-picker:latest`. A one-off Job then pre-fetches the weights into the cache.

**Researcher:** Pushed and built. Run the whole thing.

**Assistant:** [nrp_plan goal=sweep count=200 parallel=50 cpu=4 memory=8Gi hours=24 image=gitlab-registry.nrp-nautilus.io/<you>/seis-picker:latest data_volume=seis] 50 at a time is within the default cap of 50 pods at once, so no config change is needed. Over 100 pods in the run, the NRP requires limits equal to requests, which nrp sets. Summary: 200 tasks, 50 at a time, 4 CPU / 8 GiB each (200 cores at peak), limits equal to requests, 24 h limit for the whole sweep (about 4 waves of 3-4 h). Approve? **Researcher:** Yes. [nrp_run]

**Researcher:** Status? (next day)

**Assistant:** [nrp_watch target=<run>] 198 succeeded, 2 failed. Logs: both are station-years where the station was not yet installed, so every day returned 404. Not a bug. Picks total about 9 million P and S picks. Next, the association Job: [nrp_plan goal=job cpu=16 memory=64Gi image=<same image> data_volume=seis command="python associate.py"] PyOcto over all picks, writes `catalog.csv`. Approve? **Researcher:** Yes. [nrp_run] Done in 2 h 10 min. [nrp_data action=down volume=seis path=/out local=./results] `catalog.csv` (about 38 MB) and a summary of matches against the SCEDC catalog are in `./results`.

**Researcher:** Clean up the jobs; keep the picks for now.

**Assistant:** [nrp_cleanup] lists your runs; I will delete the pilot, the sweep and the association Job but not the volume run. Confirm? **Researcher:** Yes. Deleted. The picks stay on the volume, which is purged after 6 months without use.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PVC (rook-cephfs-central, RWX) | 200 GiB |
| Image | nrp_build | Dockerfile + .gitlab-ci.yml (kaniko on NRP GitLab) | about 4 GB image |
| Pilot | nrp_plan goal=sweep | Indexed Job + ConfigMap | 4 tasks, 4 CPU / 8 GiB |
| Weight cache | nrp_plan goal=job | Job | 1 CPU / 2 GiB, minutes |
| Picking | nrp_plan goal=sweep | Indexed Job + ConfigMap | 200 tasks, 50 parallel, 4 CPU / 8 GiB, 24 h limit |
| Association | nrp_plan goal=job | Job + ConfigMap | 16 CPU / 64 GiB, about 2 h |
| Results | nrp_data down | helper pod | about 40 MB |

## Compute and cost estimate

Assumptions: 40 stations x 1,826 days = 73,000 station-days; about 25 s per station-day on 4 cores; about 60 MB read per station-day.

- Picking: 73,000 x 25 s x 4 cores, about 2,000 core-hours; about 3.5 h per task; at 50 parallel, roughly 14-16 h wall-clock.
- Data streamed: about 4.4 TB read in place from the public bucket; nothing stored except picks (a few GB).
- Association: about 35 core-hours.
- GPU-hours: zero in this design. A GPU variant (EQTransformer, batched days) can cut inference time but needs enough data in flight to keep each GPU above 40%.
- Nautilus: free to the user. Laptop: about 3 weeks of continuous running plus 4 TB of downloads over campus Wi-Fi. HPCC: a good alternative for the compute; the bottleneck there is pulling 4 TB to campus storage. Commercial cloud in the same region as the bucket: about 2,000 vCPU-hours at $0.04-0.05/vCPU-hour, roughly $100, plus engineering time.

## Limits and honest caveats

- **Network is the limiter.** Each task reads from AWS; throughput varies by Nautilus node location. Pilot first, and keep parallelism modest (50) so the run is polite to the bucket.
- **Caps count pods at the same time.** `pods_per_run` (default 50) limits how many tasks run at once (a sweep's `parallel`) and `gpus_per_run` (default 4) limits GPUs in use at once; the total task count is capped separately by `tasks_per_run` (default 10,000).
- **Sweep time limit** covers the whole Job (48 h cap by default); size `hours` for all waves, and make tasks restartable (skip days already written).
- **Model choice matters.** Pretrained weights differ by training region; compare a few (`scedc`, `original`, `stead`) on a hand-checked week before the full run.
- **Not for real-time monitoring.** This is retrospective catalog building; a continuously running detector would hit the 2-week Deployment limit.
- **Not an archive:** copy the catalog and picks to permanent storage.
- **S3 tools:** the sketch uses plain HTTPS. Bulk S3 tools may be faster but S3 workflows have not been tested by UCR RC.

## Starter kit

```text
requirements.txt   seisbench  obspy  pandas  pyocto
stations.txt       one network.station per line, e.g. CI.ADO
```

```python
# pick_station_year.py - sketch: one task = one station-year
import os, io, datetime as dt, urllib.request
os.environ.setdefault("SEISBENCH_CACHE_ROOT", "/data/sbcache")  # before importing seisbench
import obspy, pandas as pd, seisbench.models as sbm
YEARS = [2019, 2020, 2021, 2022, 2023]
stations = open("stations.txt").read().split()
i = int(os.environ["SLURM_ARRAY_TASK_ID"])
net, sta = stations[i // len(YEARS)].split("."); year = YEARS[i % len(YEARS)]
model = sbm.PhaseNet.from_pretrained("scedc")
base = "https://scedc-pds.s3.amazonaws.com/continuous_waveforms"
os.makedirs(f"/data/picks/{sta}", exist_ok=True)
day = dt.date(year, 1, 1)
while day.year == year:
    doy, out = day.timetuple().tm_yday, f"/data/picks/{sta}/{year}_{day.timetuple().tm_yday:03d}.csv"
    if not os.path.exists(out):
        st = obspy.Stream()
        for ch in ("HHE", "HHN", "HHZ"):
            name = f"{net}{sta:_<5}{ch}___{year}{doy:03d}.ms"
            try:
                st += obspy.read(io.BytesIO(urllib.request.urlopen(f"{base}/{year}/{year}_{doy:03d}/{name}").read()))
            except Exception:
                pass
        picks = model.classify(st).picks if len(st) else []
        pd.DataFrame([(p.trace_id, p.phase, str(p.peak_time), p.peak_value) for p in picks],
                     columns=["trace", "phase", "time", "prob"]).to_csv(out, index=False)
    day += dt.timedelta(days=1)
```

## README blurb

Run a deep-learning phase picker over years of public continuous seismic data as a sweep: each task takes one station-year, streams the daily files from a public cloud bucket inside the cluster, and writes picks to a shared volume, skipping days already done. A pilot surfaces setup problems early, nrp_build bakes the picker into an image, and a final Job associates picks into an earthquake catalog. No data passes through the laptop until the finished catalog comes back.
