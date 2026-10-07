# Research examples

Twenty worked examples of researchers using nrp-mcp on the National Research Platform's
Nautilus cluster, one for each of 20 fields. Each one is modeled on the kind of research
done at UC Riverside: public UCR news, department pages and published papers. Each shows
a realistic conversation with an AI assistant, what nrp-mcp builds, a compute estimate,
honest limits, and a starter kit you can adapt.

These are illustrations, not case studies. None of these groups use nrp-mcp today, and
none of these runs happened. Times, throughputs, failure counts and costs are stated
assumptions, not measurements. The conversations are written against nrp-mcp 0.6.4.

| # | Example | Field | Fit | What it shows |
|---|---|---|---|---|
| 01 | [Bee repellent screening](01-bee-repellent-screening.md) | Chemical ecology, ML | Good | Scoring about 51M molecules as a GPU sweep, kept within the GPU cap |
| 02 | [Atmospheric rivers in CMIP6](02-atmospheric-rivers-ensemble.md) | Climate dynamics | Good | 30 climate models streamed from public cloud data; figures in a private notebook |
| 03 | [Sea-ice floe sweep](03-sea-ice-floe-sweep.md) | Granular physics | Excellent | 1,000 runs from an unchanged Slurm array script |
| 04 | [Plant genome scans](04-plant-genome-scans.md) | Plant genomics | Good | Custom image with nrp_build, then alignment and association sweeps on public SRA data |
| 05 | [Mission records with an LLM](05-mission-records-llm.md) | History, digital humanities | Good | NRP-hosted open LLMs read about 200k historical records, then a search app |
| 06 | [Exoplanet and Enceladus spectra](06-exoplanet-enceladus-spectra.md) | Astrobiology | Good | A 1,200-model photochemistry grid, one case per task |
| 07 | [Avian flu proteomics](07-avian-flu-proteomics-ai.md) | Virology, proteomics | Good | Deep-learning peptide ID on public PRIDE data, four GPUs at a time |
| 08 | [Video model training](08-video-computing-training.md) | Computer vision | Excellent | Fixing low GPU use (the 40% rule) and surviving preemption |
| 09 | [LLM confidence probing](09-llm-confidence-probing.md) | Trustworthy AI | Good | Open-weight model internals plus NRP-hosted black-box baselines |
| 10 | [Cloud droplet parcel sweeps](10-cloud-droplet-parcel-sweeps.md) | Atmospheric chemistry | Excellent | 16,800 parcel-model runs packed into 50 tasks |
| 11 | [Joint-model simulation study](11-joint-model-simulation-study.md) | Statistics | Excellent | 3,000 seeded R replicates from a Slurm array |
| 12 | [Wolbachia genomics](12-mosquito-wolbachia-genomics.md) | Entomology | Good | 180 bacterial genomes assembled, then a phylogeny |
| 13 | [Open neuroimaging](13-open-neuroimaging-pipelines.md) | Neuroscience | Good | fMRIPrep per subject on OpenNeuro data |
| 14 | [Air-quality dashboard](14-air-quality-dashboard.md) | Air quality | Good | Sensor correction plus a short-lived public demo dashboard |
| 15 | [Seismic waveforms](15-seismic-waveform-processing.md) | Seismology | Excellent | 5 years x 40 stations of public waveform data read in place |
| 16 | [2D materials DFT scans](16-2d-materials-dft-scans.md) | Condensed matter | Good | 420 single-node DFT cells; large MPI work stays on campus clusters |
| 17 | [Metabolomics matching](17-metabolomics-spectral-matching.md) | Metabolomics | Excellent | 300 public studies matched against open spectral libraries |
| 18 | [Course notebooks](18-course-notebooks-for-a-class.md) | Teaching | Good | Hosted JupyterHub versus nrp-mcp for a 40-student class |
| 19 | [Econometrics Monte Carlo](19-econometrics-monte-carlo.md) | Econometrics | Excellent | Reproducible random-number streams per task |
| 20 | [Open-text LLM coding](20-open-text-llm-coding.md) | Social science | Good | 61k public comments coded by LLMs and checked against human coders |

**Fit:** Excellent means independent tasks, public data, standard hardware and nothing to
work around. Good means it fits with one caveat, such as a license, a data boundary or a
short web-app lifetime.

## By kind of work

- **Bring a Slurm array as is:** 03, 11, 19
- **GPU sweeps:** 01, 07, 08, 09
- **Large CPU sweeps:** 02, 06, 10, 12, 13, 15, 16, 17
- **NRP-hosted open LLMs:** 05, 09, 20
- **Web apps:** 05, 14
- **Notebooks:** 02, 06, 09, 17, 18
- **Custom images with nrp_build:** 04, 06, 11, 12, 16, 19

## Where sensitive data goes instead

Nautilus is for public or low-risk (P1) data. These examples say where the sensitive
version of the work belongs: 04 (human genomes), 07 (farm records, controlled sequence
databases), 08 (lab video of people), 11 (patient data), 13 (IRB-governed MRI), 18
(student records), 19 (licensed financial data), 20 (survey responses).
