# 20. Coding open-ended public comments with open LLMs, validated against human coders

**Field:** Public policy, sociology, political science, psychology, health equity | **UCR connection:** The UCR School of Public Policy houses the first undergraduate public policy major in the UC system (https://spp.ucr.edu/undergraduate); the Department of Society, Environment, and Health Equity (SEHE, https://sehe.ucr.edu/) works on social, environmental and health policy problems; U.S. News 2026 ranked UCR No. 52 in political science, No. 64 in sociology, No. 80 in public affairs and No. 81 in psychology (Inside UCR, April 2026, https://insideucr.ucr.edu/stories/2026/04/07/ucr-grad-programs-ranked-among-best) | **Fit:** Good (excellent for truly public text; not usable for most survey data)

## The science

Social scientists study what people write in their own words: public comments on a proposed federal rule, open-ended survey answers, testimony. The standard method is content analysis: a codebook (stance toward the rule, main argument, form letter or not), two trained coders on a sample, and agreement measured with Cohen's kappa or Krippendorff's alpha before the codes are trusted. A high-profile docket can draw tens of thousands of comments, so most studies code only a sample.

LLMs can apply a codebook to every comment, if they are validated like a new human coder: against a human-coded subset, with agreement statistics and per-category error rates. The bottleneck is the model calls: 60,000 comments x two models x two prompts is 240,000 requests. Commercial APIs bill per token, and hosting a 100B-class open model needs GPUs most social science departments lack. The NRP's hosted models remove both problems for public text.

## Who at UCR does this

- **School of Public Policy:** policy analysis, regulation, and public participation in rulemaking.
- **Sociology, Political Science and Psychology** (College of Humanities, Arts, and Social Sciences): text-as-data, survey methods and content analysis.
- **Society, Environment, and Health Equity:** environmental justice and health policy, where public comments on environmental and health rules are a natural corpus.

The typical group is a faculty member with research assistants who already hand-code text.

None of these groups use nrp-mcp today; this is an illustration.

## The data

**Use this: public regulatory comments.** Regulations.gov publishes public comments on federal rulemakings. The v4 API (https://open.gsa.gov/api/regulationsgov/) serves dockets and comments with a free api.data.gov key, by default 1,000 requests per hour (https://api.data.gov/docs/developer-manual/). A large docket is tens of thousands of short comments, well under 1 GB of text. Agencies' Federal Register notices usually say comments are posted publicly without change, so this is public, P1 text. Cautions: some commenters include names and addresses, so drop those fields and do not quote private individuals identifiably; keep the API key out of job specs.

**Check carefully: archive survey files.** ICPSR public-use files are not automatically fine. ICPSR's terms prohibit redistribution without written permission and any attempt to identify respondents, and ICPSR has separate guidance on using LLMs with its data. Read the study's terms first; when in doubt, do not.

**Do not put this on Nautilus: your own human-subjects survey text.** Answers collected from participants are governed by the IRB protocol and consent form, and free text often identifies people even after names are removed. It is usually not P1. At UCR, even a "de-identified" student survey needs a classification from the Information Security Office, and FERPA applies to student records. Use AI tools under UCR's enterprise terms on approved platforms, or an open model inside an Ursa Major Tier 2 secure project or the UCR Secure Enclave. The UC protection-level guide is at https://security.ucop.edu/files/documents/uc-protection-level-classification-guide.pdf; UCR's IRB is at https://research.ucr.edu/orc/irb.

## The conversation

> **Researcher:** I have 61,000 public comments from one EPA docket in `comments.jsonl` (fetched with the Regulations.gov API, names and addresses dropped) and a codebook. Two RAs double-coded 600 of them. Code all 61,000 with an NRP model, then tell me how well it agrees with the RAs.

> **Assistant:** Public regulatory text fits Nautilus. Storage first: [nrp_plan goal=volume name=comments size=10Gi]. The file is 180 MB, over the 100 MB limit for [nrp_data up], so I'll split it in two. Uploading needs you to confirm the data is P1. Is that a yes?

> **Researcher:** Yes, public comments, identifiers removed.

> **Assistant:** [nrp_run] volume Bound; [nrp_data up] 3 files to /data/in (the two parts plus `human600.csv`, the RA codes). Now the coding run. [nrp_plan goal=llm-batch command="python code_comments.py --models gpt-oss,qwen3 --prompts v1,v2" cpu=2 memory=4Gi]. Plan summary: one Job, python:3.12-slim, calling https://ellm.nrp-nautilus.io/v1. Your LLM token goes from the file you name into a Kubernetes Secret; the spec only references it. The script must retry with backoff (fair use is about 200K output tokens per minute per token and model). Set a private `cache_salt` so prompts are not cached across users. 24 h limit, 2 retries; progress on /data/out lets a retry resume. Confirm?

> **Researcher:** Confirm. The token is in ~/.config/nrp/llm-token.

> **Assistant:** [nrp_run] Secret created from that file (its value is not logged or shown), Job started; run card in .nrp/runs/. [nrp_watch] after 20 minutes: running, 9,800 of 244,000 requests done, but the log shows `404 model not found: qwen3` for every qwen3 request. Diagnosis: that model name is not in the current catalog; NRP models rotate. Fix: read /v1/models at start and use the exact id instead of a hard-coded name.

> **Researcher:** Patch it to check /v1/models and use whichever qwen3 model is listed.

> **Assistant:** Patched. [nrp_cleanup] removes the first run (its gpt-oss results on /data/out are kept and skipped on restart). [nrp_plan goal=llm-batch ...] again, confirm, [nrp_run]. [nrp_watch] four hours later: complete. 244,000 requests, 1,712 retried after HTTP 429 and all succeeded on backoff, 37 responses were not valid JSON on the first try and were re-asked once; 3 still failed and are listed in /data/out/failed.jsonl.

> **Researcher:** Now the validation.

> **Assistant:** [nrp_plan goal=job command="python validate.py" cpu=1 memory=2Gi] runs on the 600 double-coded comments. Confirm? ... [nrp_run], [nrp_watch]: done. Summary from /data/out/agreement.csv: RA vs RA Krippendorff's alpha 0.81 on stance; gpt-oss prompt v2 vs the RA consensus alpha 0.78, qwen3 v2 0.74; both models weakest on "mixed" stance (most errors are mixed coded as oppose). Form-letter detection agrees almost perfectly. [nrp_data down path=/data/out] brought back codes, agreement tables and confusion matrices (95 MB).

> **Researcher:** Good enough to report with the caveats. Clean up everything, including the volume.

> **Assistant:** [nrp_cleanup] lists the coding run, the validation job, the data helper pod, the Secret and the volume; deletes them after your second yes. Keep the prompt files, model ids and run dates with the results; the hosted models will change.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | `volume` | PVC, rook-cephfs-central, RWX | 10Gi |
| Upload comments | `nrp_data up` (P1 confirmed) | helper pod | 3 files under 100 MB |
| LLM coding | `llm-batch` | Job + Secret (LLM token) | 2 CPU / 4Gi, no GPU, about 4 h |
| Validation | `job` | Job | 1 CPU / 2Gi, minutes |
| Results | `nrp_data down` | helper pod | 95 MB |
| Cleanup | `nrp_cleanup` | deletes all of the above | - |

## Compute and cost estimate

Assumptions: 61,000 comments averaging about 300 tokens, an 800-token codebook prompt, about 150 output tokens of JSON per call, 2 models x 2 prompt versions = 244,000 calls, about 270 million input and 37 million output tokens. With 16 requests in flight per model and 2-4 seconds per call, the run takes about 4-6 hours, and output stays well under the 200K tokens per minute fair-use limit.

- **Hand coding:** at 1 minute per comment, about 1,000 RA hours per coder.
- **Laptop:** cannot host a 100B-class model.
- **Commercial API:** roughly $20 to several hundred dollars depending on the model tier.
- **HPCC or cloud GPU hosting an open model:** possible, but means managing vLLM and GPU memory.
- **Nautilus:** free to the user. The job itself uses no GPU; the NRP hosts the models.

## Limits and honest caveats

- **Public text only.** Most survey and interview text is IRB-governed and not P1. Use UCR enterprise AI terms on approved platforms, Ursa Major Tier 2, or the Secure Enclave for it.
- **Validation is not optional.** Report agreement against human coders per category, the error pattern (for example, mixed stances coded as oppose), and the prompt and model versions. Agreement on 600 comments does not guarantee it on subgroups; check by comment length and by form-letter status.
- **Models change.** The NRP rotates models; a rerun next year may not reproduce exactly. Save raw responses and model ids.
- **Fair use and concurrency.** Keep in-flight requests modest, retry with backoff, and do not run many copies of the job against one large model.
- **Regulations.gov rate limits** (1,000 requests per hour by default) make fetching a large docket a multi-hour step; fetch once.
- **Prompt caching** can share prompts across users unless `cache_salt` is set.
- **No public dashboard by default.** A `goal=web` explorer is possible but NRP removes Deployments after 2 weeks, and at UCR it needs RC's OK first.

## Starter kit

```
comments.jsonl       # id, docket, text (identifiers dropped)
codebook.md          # categories and definitions
prompts/v1.txt prompts/v2.txt
code_comments.py     # llm-batch job
validate.py          # agreement statistics
requirements.txt     # openai, pandas, krippendorff, scikit-learn
```

`code_comments.py` (sketch):

```python
import json, os, random, time, pathlib
from openai import OpenAI
cli = OpenAI(base_url="https://ellm.nrp-nautilus.io/v1",
             api_key=open("/secrets/llm/token").read().strip())
ids = {m.id for m in cli.models.list().data}
models = [next(i for i in ids if i.startswith(m)) for m in ("gpt-oss", "qwen3")]
done = pathlib.Path("/data/out/done.txt"); seen = set(done.read_text().split()) if done.exists() else set()
def ask(model, prompt, text):
    for attempt in range(8):
        try:
            r = cli.chat.completions.create(model=model, temperature=0,
                messages=[{"role": "system", "content": prompt}, {"role": "user", "content": text}],
                extra_body={"cache_salt": os.environ["CACHE_SALT"]})
            return json.loads(r.choices[0].message.content)
        except Exception:
            time.sleep(min(60, 2 ** attempt) + random.random())
    return None
```

`validate.py` (sketch):

```python
import pandas as pd, krippendorff
from sklearn.metrics import cohen_kappa_score
d = pd.read_csv("/data/in/human600.csv").merge(pd.read_csv("/data/out/codes.csv"), on="id")
print("RA1 vs RA2 kappa", cohen_kappa_score(d.ra1_stance, d.ra2_stance))
for col in [c for c in d if c.startswith("llm_")]:
    a = krippendorff.alpha(reliability_data=[d.consensus_stance.astype("category").cat.codes,
                                             d[col].astype("category").cat.codes],
                           level_of_measurement="nominal")
    print(col, "alpha vs consensus", round(a, 3))
```

## README blurb

Code tens of thousands of public comments with open LLMs and validate the result like a new human coder: nrp-mcp uploads public text to a volume, runs an llm-batch Job against the NRP-hosted models with the token kept in a Kubernetes Secret and polite retries, and runs a validation job that reports Cohen's kappa and Krippendorff's alpha against a human-coded subset. Only truly public text belongs here; IRB-governed survey or interview text goes to approved secure platforms.
