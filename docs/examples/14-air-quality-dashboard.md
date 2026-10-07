# 14. Low-Cost Air Sensor Correction and a Public Dashboard

**Field:** Atmospheric science, environmental engineering | **UCR connection:** CE-CERT's Atmospheric Processes and Air Quality research and its Air Quality, Climate, and Equity (ACE) program, which runs community monitoring networks and mobile air pollution mapping (https://www.cert.ucr.edu/ACE-Research); also CE-CERT's 2026 mobile monitoring study of particle levels across Southern California transit environments (https://www.cert.ucr.edu/news/2026/06/25/new-study-examines-air-quality-across-urban-transit-environments) | **Fit:** Good

## The science

Low-cost optical particle sensors such as PurpleAir now outnumber regulatory monitors many times over, which makes neighborhood-scale air quality visible for the first time. But raw readings are biased: a US-wide EPA evaluation found raw PurpleAir PM2.5 overestimates regulatory measurements by about 40%, with errors that depend on humidity and aerosol type (EPA evaluation in Atmospheric Measurement Techniques, 2021, https://amt.copernicus.org/articles/14/4617/2021/). The research question is how well a correction model trained at sensors collocated with regulatory monitors transfers to nearby uncollocated sensors, and what corrected maps reveal about exposure differences between communities.

Compute is not huge, but it is spread across three awkward steps: pulling a year of hourly data for hundreds of sensors and monitors, fitting and cross-validating correction models (linear with humidity, then gradient boosting), and showing the result to community partners in an interactive dashboard. A laptop handles each piece slowly; the cross-validation grid and a shareable web address are where a cluster helps.

## Who at UCR does this

CE-CERT (Bourns College of Engineering) is UCR's largest research center. Its Atmospheric Processes and Air Quality area includes the Atmospheric Processes Laboratory and the ACE program, which links community air monitoring, mobile monitoring and mapping, and exposure research with community partners. A 2026 CE-CERT study used portable instruments to compare ultrafine particle and PM10 levels on rail platforms, buses, airport walkways and streets across Southern California. Groups like these combine sensor networks, reference instruments and public-facing results.

None of these groups use nrp-mcp today; this is an illustration.

## The data

- **EPA AQS pre-generated files** (https://aqs.epa.gov/aqsweb/airdata/download_files.html): hourly FRM/FEM PM2.5 (parameter 88101), one zip per year, about 69 MB for 2025, no key needed. The AQS API (https://aqs.epa.gov/aqsweb/documents/data_api.html) needs a free key requested by email.
- **AirNow API** (https://docs.airnowapi.org/): near-real-time data, free key required. AirNow data are preliminary; use AQS for model training.
- **PurpleAir API** (https://www.purpleair.com/api): real-time and historical data from 35,000+ sensors. You need an API key (created at https://develop.purpleair.com with a Google sign-in), and calls consume prepaid points. The Terms of Service (https://www.purpleair.com/policies/terms-of-service) and Data License (https://www.purpleair.com/license) allow showing data to end users of an app but not reselling the data or an API built from it, and require attribution.

**P1 check:** Outdoor sensor readings and regulatory monitor data are public and P1. Indoor sensors in homes, participant-worn monitors, health outcomes or survey responses linked to addresses are not: those belong in an Ursa Major Tier 2 secure project or the UCR Secure Enclave, never on Nautilus or a public dashboard.

## The conversation

**Researcher:** I have a year of PurpleAir data for about 300 outdoor sensors in the Inland Empire and want to correct it against AQS monitors, then show it in a Streamlit dashboard for our community partners.

**Assistant:** Good fit, all P1. One point first: your PurpleAir key should stay on your laptop. nrp-mcp skips files named like keys or secrets, and specs are visible to other cluster users, so download PurpleAir data locally with your key and upload the result. [nrp_status] Namespace `ucr-aq-sensors`, no runs, CPU quota fine.

**Researcher:** `purpleair_2025.csv.gz` is 61 MB.

**Assistant:** [nrp_plan goal=volume name=aqdata size=20Gi data_is_p1=true] then [nrp_plan goal=pull data_volume=aqdata urls=["https://aqs.epa.gov/aqsweb/airdata/hourly_88101_2025.zip"] subdir=aqs data_is_p1=true] Two plans: a 20 GiB shared volume, then an in-cluster download of the AQS file, so nothing large crosses your laptop. Approve both? **Researcher:** Yes. [nrp_run] [nrp_run] Done. [nrp_data action=up volume=aqdata local=./purpleair_2025.csv.gz path=/purpleair data_is_p1=true] Uploaded (under the 100 MB limit for uploads).

**Researcher:** Now fit the models. I want leave-one-site-out cross-validation over the 24 collocated sites.

**Assistant:** First a short Job runs `make_pairs.py` to join sensors to monitors within 100 m into /data/pairs.parquet (2 minutes). Then a sweep: one task per held-out site, each fitting a linear model with humidity and a gradient-boosting model, writing scores to /data/cv. [nrp_plan goal=sweep count=24 parallel=24 cpu=2 memory=4Gi data_volume=aqdata command="python fit_cv.py"] Summary: Indexed Job, 24 tasks at once, 2 CPU / 4 GiB each, `python:3.12-slim` with your `requirements.txt` installed at start, no GPU (the code does not use one). Approve? **Researcher:** Yes. [nrp_run]

**Researcher:** Done?

**Assistant:** [nrp_watch target=<run>] 23 succeeded, 1 failed (task 17). Last log lines: `ValueError: Input contains NaN` - that site has a two-month AQS outage and the script does not drop missing hours. Add `dropna` on the joined frame and rerun just that site. [nrp_plan goal=job cpu=2 memory=4Gi data_volume=aqdata command="SITE_INDEX=17 python fit_cv.py"] [nrp_run] Succeeded. Mean held-out RMSE: 2.9 ug/m3 linear-with-humidity, 2.4 boosting.

**Researcher:** Fit the final model on all sites and correct every sensor.

**Assistant:** [nrp_plan goal=job cpu=4 memory=8Gi data_volume=aqdata command="python correct_all.py"] One Job, 4 CPU / 8 GiB, writes `corrected_hourly.parquet` and a compact `daily_summary.csv` for the dashboard. Approve? **Researcher:** Yes. [nrp_run] Finished in 11 min. [nrp_data action=down volume=aqdata path=/out local=./dashboard/data]

**Researcher:** Publish the dashboard.

**Assistant:** [nrp_plan goal=web project=./dashboard] Before you approve, one catch in the decisions: it copies 2 files, `app.py` and `requirements.txt`. Code delivery skips folders named `data` and any file over 200 KB, and nrp-mcp web Deployments cannot mount your volume, so the app would start with no data. Two options: move `daily_summary.csv` (180 KB) next to `app.py`, or build an image with nrp_build. **Researcher:** Daily is fine for partners. **Assistant:** [nrp_plan goal=web project=./dashboard] Now 3 files. Streamlit detected on port 8501; 1 CPU / 2 GiB; suggested hosts `ie-air-viewer.nrp-nautilus.io`, `ie-air.nrp-nautilus.io`, `aq-correct.nrp-nautilus.io`. Warnings: the NRP removes Deployments after 2 weeks; only P1 data may be served; at UCR, get Research Computing's OK before publishing. To go public, type the exact URL back.

**Researcher:** RC approved it for the partner workshop. https://ie-air-viewer.nrp-nautilus.io/

**Assistant:** [nrp_run public_ack=https://ie-air-viewer.nrp-nautilus.io/] Applied. [nrp_watch target=<run>] Ingress up, HTTPS not 200 yet: the pod is still installing requirements. A minute later: HTTPS 200, app ready. The PurpleAir attribution line is in the page footer, as the Data License requires. After the workshop, [nrp_cleanup] removes the jobs and the web app; the volume stays until you delete it.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PVC (rook-cephfs-central, RWX) | 20 GiB |
| AQS download | nrp_plan goal=pull | Job | 1 file, 69 MB, minutes |
| PurpleAir upload | nrp_data up | helper pod | 61 MB |
| Pair building | nrp_plan goal=job | Job + ConfigMap | 2 CPU / 4 GiB, about 2 min |
| Cross-validation | nrp_plan goal=sweep | Indexed Job + ConfigMap | 24 tasks, 2 CPU / 4 GiB, about 15 min each |
| Final correction | nrp_plan goal=job | Job + ConfigMap | 4 CPU / 8 GiB, about 11 min |
| Dashboard | nrp_plan goal=web | Deployment + Service + Ingress + ConfigMap | 1 CPU / 2 GiB, up to 2 weeks |

## Compute and cost estimate

Assumptions: 300 sensors and 24 collocated sites, one year hourly (about 2.6 million sensor-hours), boosting with a modest hyperparameter grid.

- Cross-validation: 24 tasks x 15 min x 2 cores, about 12 core-hours, under 30 min wall-clock. Laptop: 3-6 hours sequentially.
- Final correction: under 1 core-hour.
- Dashboard: 1 core held for the life of the demo.
- GPU-hours: zero.
- Nautilus is free to the user. The real cost is PurpleAir API points for the historical pull, paid on your PurpleAir account. On a commercial cloud the compute would be a few dollars; hosting a small always-on web app runs about $10-30/month.

## Limits and honest caveats

- **Two-week web limit:** Nautilus web apps are for demos, workshops and short reviews. A dashboard partners rely on for months needs an NRP exception (ask Nautilus Support) or a long-lived host; ask RC about options.
- **Publishing needs approval:** at UCR, Research Computing's OK comes before `public_ack`. Serve only outdoor P1 data.
- **Licensing:** follow PurpleAir's attribution and redistribution terms. Do not expose raw PurpleAir data as a downloadable dataset or API.
- **API keys:** nrp-mcp can inject only the NRP LLM token as a Secret, so scheduled in-cluster PurpleAir pulls would need a Secret created by hand with kubectl. The workflow above keeps the key on the laptop.
- **No volume in web apps:** the dashboard carries its own data (text files under 200 KB each, about 900 KB total, not in a `data/` folder) or uses an nrp_build image. If you bake data into an image, keep the registry project private and add a pull secret, since PurpleAir data may not be republished.
- **Not real-time:** refreshing data means rerunning the batch steps and redeploying.

## Starter kit

```text
requirements.txt     pandas  pyarrow  scikit-learn  lightgbm
dashboard/requirements.txt   streamlit  pandas  plotly
```

```python
# fit_cv.py - sketch: leave-one-site-out CV, one held-out site per task
import os, pandas as pd, lightgbm as lgb
from sklearn.linear_model import LinearRegression
from sklearn.metrics import mean_squared_error
k = int(os.environ.get("SITE_INDEX", os.environ["SLURM_ARRAY_TASK_ID"]))
df = pd.read_parquet("/data/pairs.parquet").dropna()   # sensor-monitor hourly pairs
site = sorted(df.site_id.unique())[k]
train, test = df[df.site_id != site], df[df.site_id == site]
X = ["pm25_cf1", "rh", "temp"]
out = {"site": site}
for name, m in [("linear_rh", LinearRegression()), ("lgbm", lgb.LGBMRegressor(n_estimators=400))]:
    m.fit(train[X], train.pm25_ref)
    out[name] = mean_squared_error(test.pm25_ref, m.predict(test[X])) ** 0.5
os.makedirs("/data/cv", exist_ok=True)
pd.DataFrame([out]).to_csv(f"/data/cv/site_{k:02d}.csv", index=False)
```

```python
# dashboard/app.py - sketch
import pandas as pd, plotly.express as px, streamlit as st
st.set_page_config(page_title="Inland Empire corrected PM2.5")
d = pd.read_csv("daily_summary.csv", parse_dates=["date"])
day = st.slider("Day", d.date.min().date(), d.date.max().date())
sel = d[d.date.dt.date == day]
st.plotly_chart(px.scatter_mapbox(sel, lat="lat", lon="lon", color="pm25_corrected",
                mapbox_style="open-street-map", zoom=8))
st.caption("Sensor data: PurpleAir. Reference data: US EPA AQS. Corrected values are estimates.")
```

## README blurb

Correct low-cost air sensor data against regulatory monitors and share the result as a Streamlit dashboard. Public monitor files are downloaded inside the cluster, a leave-one-site-out sweep compares correction models in minutes, and the dashboard is published at an nrp-nautilus.io address only after the user types the exact URL back. Web apps on Nautilus last up to two weeks, which suits workshops and reviews; long-lived dashboards need an exception or another host.
