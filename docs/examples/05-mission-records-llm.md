# 05. Reading California mission records with an open LLM

**Field:** History, digital humanities | **UCR connection:** the Early California Population Project (ECPP) in the Department of History, which received a $350,000 NEH grant in 2026 to build "ECPP 2.0" (database at https://ecpp.ucr.edu/) | **Fit:** Good

## The science

The baptism, marriage and burial registers kept at California's 21 missions between 1769 and 1850 are among the most detailed population records of early America. They name Native people, soldiers and settlers, their villages of origin, godparents, spouses, parents and children. ECPP has turned them into a database of more than 110,000 people, and the new grant adds roughly 20,000 immigrant records, moves the data to the GEDCOM genealogy format, and maps ancestral Native villages. Questions about kin networks across missions need every record reduced to the same fields: person, role, date, mission, village, and links to relatives.

Much of that structure sits in free-text notes, Spanish phrasing and inconsistent spellings; hand-coding it takes years of graduate-student time. An open LLM can draft each structured entry in seconds, with humans checking samples. The bottleneck is running a large model hundreds of thousands of times without a cloud contract, an API budget, or a GPU in the department.

## Who at UCR does this

The ECPP lives in the Department of History in the College of Humanities, Arts, and Social Sciences (CHASS), with CHASS IT maintaining the database and The Huntington Library as a long-standing partner. Similar extraction problems arise with historical newspapers, finding aids and census-like registers. None of these groups use nrp-mcp today; this is an illustration.

## The data

- Source: transcribed ECPP records (https://ecpp.ucr.edu/). The site describes about 104,000 baptisms, 28,000 marriages and 72,000 burials, roughly 204,000 records spread over some 225 fields. As JSONL text, under 100 MB.
- Terms: the ECPP page states the data may be used "for personal and noncommercial purposes only" and must be cited. The NRP-hosted LLMs are also for non-commercial use with P1 data only, so the terms line up. Only records the project has the right to publish should go to the cluster or into a public app; anything still under a repository's or archive's restrictions stays out.
- P1 check: these are records of people who died more than 170 years ago. Historical records of long-dead people are generally P1, but confirm with the project's own terms and partners. No living-person data goes in: no descendant contact details, user accounts, or family trees submitted by living genealogists. If the project ever collects those, they belong on UCR-managed systems (for example a Tier 2 secure project in Ursa Major), not Nautilus.
- Respect: these registers document colonization of Native communities. Decisions about what a public search app shows should involve the project's tribal and community partners, not just the license.

## The conversation

**Researcher:** I have a folder with `extract.py`, a JSON schema, and the record export split into 400 batch files. For each record I want person, role, date, mission, village and kin links as JSON, using one of the NRP models. Then a small search app for my students.

**Assistant:** [nrp_status] You are in namespace `ecpp-history`, signed in, nothing running. The hosted LLMs need a personal token (https://nrp.ai/llmtoken, for groups with LLM access). It belongs in a Kubernetes Secret named `nrp-llm-token`, created once by you (`kubectl create secret generic nrp-llm-token --from-file=token=<file>`), never in the project; nrp-mcp skips any file named like a key or token. Then I need a volume for the batches and the outputs. [nrp_plan goal=volume size=20Gi data_is_p1=true] Plan: "A shared CephFS volume (rook-cephfs-central, ReadWriteMany), 20 GiB, mountable at /data." Approve?

**Researcher:** Yes. And the records are P1.

**Assistant:** [nrp_run plan_id=... confirm_token=...] Volume `ecpp-batches` created. The batches folder is 85 MB of JSONL, under the 100 MB limit for `nrp_data up`. [nrp_data up local=batches/ volume=ecpp-batches path=/batches data_is_p1=true] Done. Now the extraction. [nrp_plan goal=llm-batch data_volume=ecpp-batches command="python extract.py --model qwen3 --concurrency 12 --limit 500" cpu=2 memory=4Gi hours=36] Plan summary: "A Job, no GPU, 2 CPU and 4 GiB, mounts ecpp-batches at /data, stops after 36 h. LLM token comes from the Secret nrp-llm-token (key token), never the spec; base URL https://ellm.nrp-nautilus.io/v1. Retry with backoff in your script (fair use: 200K output tokens/min per token and model)." Note: an llm-batch is one Job, not an indexed sweep. That suits this work: the real limit is the endpoint's per-user concurrency (16 requests for qwen3), not pods. Your script runs 12 requests at once and skips batches that already have output, so a restart resumes. This plan is the 500-record pilot.

**Researcher:** Yes, pilot first.

**Assistant:** [nrp_run ...] Pilot finished in 4 minutes. [nrp_data down volume=ecpp-batches path=/out/pilot-batch000.jsonl] 497 of 500 parsed against your schema; 3 came back with a date as free text. Your validator flagged them. Full run?

**Researcher:** Go.

**Assistant:** [nrp_plan ... (no --limit)] [nrp_run ...] Two hours in: [nrp_watch target=run-7f2c grep=429] The logs show bursts of HTTP 429 "rate limited". Long reasoning before each answer counts against the output budget. Your script backs off, so nothing is lost, but it is slow. Fix: set reasoning effort low in `extract.py` (the JSON needs no long reasoning). I will stop this run; the restart picks up from batch 61. [nrp_cleanup run=run-7f2c] then a new plan and run with the change. After 11 hours: [nrp_watch target=run-91aa] "Succeeded. 400 of 400 batches." 203,860 records parsed, 1,140 flagged for human review.

**Researcher:** Now the search app. It's a small Streamlit app over a SQLite file.

**Assistant:** The SQLite file is 140 MB, too big for the ConfigMap that copies code (200 KB per file), and web Deployments do not mount volumes in this version. So the app needs an image. [nrp_build project=ecpp-explorer] It suggests a Dockerfile (python:3.12-slim, your app, the SQLite file) and a `.gitlab-ci.yml` that builds with kaniko on NRP GitLab into gitlab-registry.nrp-nautilus.io. nrp_build writes nothing itself; with your yes I save both files into the app folder. Push to NRP GitLab and the pipeline builds the image. Then: [nrp_plan goal=web image=gitlab-registry.nrp-nautilus.io/ecpp/ecpp-explorer:latest] Suggested names from your app: `mission-records-explorer`, `ecpp-explorer`, `mission-register-search`. Plan: "A Deployment (1 replica) with 1 CPU and 2Gi, Service and Ingress at https://mission-records-explorer.nrp-nautilus.io (TLS). Deployments are removed after 2 weeks." At UCR, Research Computing's OK comes before anything goes public. To publish, type the exact URL back.

**Researcher:** RC approved it for the spring seminar. https://mission-records-explorer.nrp-nautilus.io

**Assistant:** [nrp_run plan_id=... confirm_token=... public_ack=https://mission-records-explorer.nrp-nautilus.io] Live. [nrp_watch target=mission-records-explorer] HTTPS returns 200. It will be removed after 2 weeks; for the full term, ask NRP for an exception or plan permanent hosting on CHASS servers. When the seminar ends: [nrp_cleanup] lists your runs; deleting needs a second yes.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PersistentVolumeClaim (CephFS, RWX) | 20 GiB |
| Upload batches | nrp_data up | helper pod + kubectl cp | 85 MB |
| Pilot | nrp_plan goal=llm-batch | Job + ConfigMap (code) | 2 CPU, 4 GiB, 0 GPU, minutes |
| Full extraction | nrp_plan goal=llm-batch | Job + ConfigMap, token from Secret | 2 CPU, 4 GiB, 0 GPU, ~11 h (36 h limit) |
| App image | nrp_build | none (Dockerfile + GitLab CI files) | built on NRP GitLab |
| Search app | nrp_plan goal=web | Deployment + Service + Ingress | 1 CPU, 2 GiB, 2-week life |

## Compute and cost estimate

Assumptions: about 204,000 records; roughly 600 input and 200 output tokens per record with reasoning kept short, so about 120 million input and 40 million output tokens. The fair-use ceiling of 200,000 output tokens per minute puts a hard floor near 3.5 hours; with 12 concurrent requests at 2 to 3 seconds each, expect 10 to 15 hours of wall-clock in one Job. No GPU is requested, since the model runs on NRP's servers. Nautilus and the hosted LLMs are free to the user.

Comparison: a 27B-class model on a strong laptop (about 10 tokens per second) would need weeks. A budget commercial API would likely cost tens to low hundreds of dollars here, plus procurement and a data-terms review. The HPCC could serve a model on its GPUs, but you would install and run it yourself. The savings are modest; the gain is no procurement, no model hosting, one sign-in.

## Limits and honest caveats

- LLM output is a draft. Plan for human review of flagged records and a random sample, and report the error rate in any publication.
- Fair use and concurrency limits set the pace; adding pods does not make it faster. The script must retry with backoff.
- nrp-mcp runs llm-batch as one Job. Resumability comes from your script's done-markers on the volume, not from Kubernetes.
- Web apps on Nautilus are for demos and short courses: removed after 2 weeks, they need the typed-back URL and RC's OK, and they cannot mount volumes in this version, so data goes into an image. Long-term public hosting belongs on CHASS or campus infrastructure.
- Uploads through `nrp_data` are capped at 100 MB per file. NRP S3 suits bigger transfers, but UCR RC has not yet tested S3.
- Nautilus is not an archive: volumes are purged after 6 months of inactivity. Keep the master data at UCR.

## Starter kit

Sketch, not tested code.

`extract.py`
```python
import argparse, asyncio, json, pathlib, random
from openai import AsyncOpenAI   # reads OPENAI_API_KEY and OPENAI_BASE_URL from env

client = AsyncOpenAI()
SCHEMA = open("schema.json").read()
IN, OUT = pathlib.Path("/data/batches"), pathlib.Path("/data/out")

async def one(rec, model, sem):
    async with sem:
        for attempt in range(8):
            try:
                r = await client.chat.completions.create(model=model,
                    messages=[{"role": "system", "content": "Return JSON matching: " + SCHEMA},
                              {"role": "user", "content": rec["text"]}],
                    response_format={"type": "json_object"}, reasoning_effort="low")
                return {"id": rec["id"], "out": r.choices[0].message.content}
            except Exception:
                await asyncio.sleep(min(60, 2 ** attempt) + random.random())
        return {"id": rec["id"], "error": "gave up"}

async def main(a):
    sem = asyncio.Semaphore(a.concurrency)
    for f in sorted(IN.glob("*.jsonl")):
        done = OUT / (("pilot-" if a.limit else "") + f.name)
        if done.exists():
            continue                      # resume: skip finished batches
        recs = [json.loads(l) for l in f.open()][: a.limit or None]
        rows = await asyncio.gather(*(one(r, a.model, sem) for r in recs))
        tmp = done.with_suffix(".tmp")
        tmp.write_text("\n".join(json.dumps(x) for x in rows))
        tmp.rename(done)                  # atomic marker
        if a.limit:
            break                         # pilot: first batch only

ap = argparse.ArgumentParser()
ap.add_argument("--model", default="qwen3")
ap.add_argument("--concurrency", type=int, default=12)
ap.add_argument("--limit", type=int, default=0)
asyncio.run(main(ap.parse_args()))
```

`requirements.txt`
```
openai>=1.40
```

`app.py` (Streamlit, sketch)
```python
import sqlite3, streamlit as st
db = sqlite3.connect("records.sqlite", check_same_thread=False)
q = st.text_input("Name, village or mission")
if q:
    rows = db.execute("SELECT name, role, date, mission, village FROM people "
                      "WHERE people MATCH ? LIMIT 200", (q,)).fetchall()
    st.dataframe(rows)
st.caption("Source: The Early California Population Project, Edition 1.1.")
```

## README blurb

Turn a large collection of transcribed historical records into structured, searchable data with the NRP's hosted open LLMs. One llm-batch Job sends every record to an OpenAI-compatible model with the token kept in a Kubernetes Secret, retries politely under fair-use limits, and resumes from where it stopped. A small search app then goes up at a public nrp-nautilus.io address once you type the URL back. Use public, non-sensitive records only, with the source's terms respected.
