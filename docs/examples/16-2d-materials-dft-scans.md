# 16. Parameter Scans for Twisted 2D Materials: DFT and Moire Models as Independent Tasks

**Field:** Condensed matter physics, computational materials science | **UCR connection:** Theory and computation in support of UCR experimental condensed matter work such as a 2026 Moore Foundation-funded project on nanoscale microwave spectroscopy of collective modes in 2D many-body electronic and excitonic systems; also UCR Physics and Astronomy condensed matter groups (https://physics.ucr.edu/) and Materials Science and Engineering computational materials (https://mse.ucr.edu/) | **Fit:** Good (for many small and medium independent calculations; Partial for large twisted supercells)

## The science

When two atomically thin layers are stacked with a small twist or lattice mismatch, they form a moire pattern whose long-wavelength potential can flatten electronic bands so that electron interactions dominate. That is the regime such experiments target: devices where strongly interacting electrons organize into collective states such as electron crystals and excitonic phases, probed experimentally with new microwave techniques. Such experiments benefit from theory mapping how flat bands and interlayer coupling depend on twist angle, strain, spacing, displacement field and doping.

The compute pattern is a grid. Continuum (moire) model parameters such as interlayer tunneling and moire potential strength usually come from density functional theory (DFT) on small cells at different stackings, strains and spacings, with Wannier functions extracting tight-binding parameters. The model is then solved across thousands of twist-angle, field and filling combinations. Every grid point is independent, which is exactly what a sweep does well.

## Who at UCR does this

The Department of Physics and Astronomy has condensed matter experiment and condensed matter theory groups, and the Department of Materials Science and Engineering lists computational materials as a research area. A 2026 Moore Foundation-funded project in Physics and Astronomy fabricates 2D materials devices and develops techniques to probe collective modes. That work is experimental; this example describes the theory support that experiments like it draw on.

None of these groups use nrp-mcp today; this is an illustration.

## The data

This workflow generates its data rather than downloading it. The inputs are public:

- **Quantum ESPRESSO** (https://www.quantum-espresso.org/, source https://gitlab.com/QEF/q-e): open-source plane-wave DFT. Packaged on conda-forge (https://anaconda.org/conda-forge/qe, version 7.5) and as an NVIDIA GPU container on NGC (https://catalog.ngc.nvidia.com/orgs/hpc/containers/quantum_espresso, for example `nvcr.io/hpc/quantum_espresso:qe-7.3.1`).
- **Wannier90** (https://wannier.org/, conda-forge https://anaconda.org/conda-forge/wannier90): maximally localized Wannier functions for tight-binding parameters.
- **Pseudopotentials:** the SSSP library from Materials Cloud (https://www.materialscloud.org/discover/sssp) or PseudoDojo (http://www.pseudo-dojo.org/).
- **Structures:** lattice constants and monolayer structures from the literature or open materials databases.

Outputs are a few MB per calculation; delete wavefunctions after use.

**P1 check:** Simulated materials data is P1; unpublished results are fine. Work under export control, an industry NDA, or CUI from a defense contract does not belong here; ask RC about the Secure Enclave.

## The conversation

**Researcher:** I need continuum-model parameters for a twisted WSe2 homobilayer. Plan: DFT on the untwisted bilayer at 3 stackings, 7 interlayer distances, 5 biaxial strains and 4 displacement fields. Then Wannier90 for each, then a continuum-model sweep over twist angle.

**Assistant:** 3 x 7 x 5 x 4 = 420 small DFT calculations (6-atom cells), each independent. That fits Nautilus well. A full twisted supercell at small angles (thousands of atoms) does not: that needs multi-node MPI, which belongs on the HPCC or Ursa Major. [nrp_status] Namespace `ucr-moire-theory`, nothing running.

**Researcher:** Should I use the GPU build of QE?

**Assistant:** For 6-atom cells, probably not: a small cell would leave a GPU mostly idle, and the NRP flags GPUs under 40% utilization. Use 8 CPU cores per task with OpenMPI inside one pod (single-node MPI is fine). I suggest `nrp_build` for an image with conda-forge QE and Wannier90.

**Researcher:** OK. [shares folder with `make_inputs.py`, `run_point.sh`, `grid.csv`, `pseudo/`]

**Assistant:** [nrp_build project=.] Wrote a `Dockerfile` (FROM mambaorg/micromamba; installs qe, wannier90 and openmpi from conda-forge) and a `.gitlab-ci.yml` for kaniko on NRP GitLab. Push to gitlab.nrp-nautilus.io; the image lands at `gitlab-registry.nrp-nautilus.io/<you>/qe-w90:7.5`. Note: `pseudo/` holds `.UPF` files, which ConfigMap code delivery skips (it copies text files with known extensions only), so I added `COPY pseudo/ /opt/pseudo/` to the Dockerfile.

**Researcher:** Built. Run a pilot of 4 grid points.

**Assistant:** [nrp_plan goal=volume name=moire size=100Gi data_is_p1=true] then [nrp_plan goal=sweep count=4 parallel=4 cpu=8 memory=16Gi image=gitlab-registry.nrp-nautilus.io/<you>/qe-w90:7.5 data_volume=moire command="bash run_point.sh"] Summary: 100 GiB shared volume; Indexed Job, 4 tasks, 8 CPU / 16 GiB each, no GPU; each task reads row $SLURM_ARRAY_TASK_ID of `grid.csv`, runs `pw.x` (SCF then NSCF), `wannier90.x` and `pw2wannier90.x`, and writes `hr.dat` and band files to /data/runs/<row>. Approve both? **Researcher:** Yes. [nrp_run] [nrp_run]

**Researcher:** Results?

**Assistant:** [nrp_watch target=<run>] 3 succeeded in about 50 minutes. Task 3 was OOMKilled at 16 GiB: it is the largest interlayer spacing, with the most vacuum and the densest k-mesh in the NSCF step. Fix: raise memory 50% to 24 GiB, or reduce the NSCF k-mesh for that block. I would raise memory.

**Researcher:** Raise it and run everything, 40 at a time.

**Assistant:** 40 at a time is within the default cap of 50 pods at once, and 420 tasks is well under the per-sweep task cap, so no config change is needed. [nrp_plan goal=sweep count=420 parallel=40 cpu=8 memory=24Gi hours=24 ...] Summary: 420 tasks, 40 at a time (320 cores at peak), 24 GiB each, limits equal to requests. Over 100 pods, the NRP requires limits equal to requests, which nrp already sets. Approve? **Researcher:** Yes. [nrp_run]

**Researcher:** Status?

**Assistant:** [nrp_watch target=<run> grep="convergence NOT achieved"] 411 succeeded; 9 exited non-zero with "convergence NOT achieved" in the SCF. All 9 are the largest displacement field at the smallest spacing, where the bilayer is close to metallic. Rerun those rows with smearing and a smaller `mixing_beta` (0.2): [nrp_plan goal=sweep count=9 ...] [nrp_run] All 9 converged. The continuum-model sweep is pure Python and fast: [nrp_plan goal=sweep count=200 parallel=50 cpu=2 memory=4Gi image=python:3.12-slim data_volume=moire command="python continuum.py"] 200 twist angles from 0.5 to 5 degrees, each solving the moire bands on a field and filling grid. [nrp_run] Done in 25 minutes. [nrp_data action=down volume=moire path=/summary local=./results] Flat-band widths, gaps and Wannier parameter tables (about 300 MB) are in `./results`.

**Researcher:** Clean up.

**Assistant:** [nrp_cleanup] lists five runs: the pilot, the main sweep, the rerun, the continuum sweep and the volume. I will clean up the four job runs one by one with `run=` and leave the volume. Confirm? **Researcher:** Yes. [nrp_cleanup run=... plan_id=... confirm_token=...] (x4) Done; the moire volume remains.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Image | nrp_build | Dockerfile + .gitlab-ci.yml (kaniko, NRP registry) | about 1.5 GB |
| Storage | nrp_plan goal=volume | PVC (rook-cephfs-central, RWX) | 100 GiB |
| Pilot | nrp_plan goal=sweep | Indexed Job + ConfigMap | 4 tasks, 8 CPU / 16 GiB |
| DFT + Wannier | nrp_plan goal=sweep | Indexed Job + ConfigMap | 420 tasks, 40 parallel, 8 CPU / 24 GiB, 24 h limit |
| Reruns | nrp_plan goal=sweep | Indexed Job + ConfigMap | 9 tasks |
| Continuum model | nrp_plan goal=sweep | Indexed Job + ConfigMap | 200 tasks, 50 parallel, 2 CPU / 4 GiB |
| Results | nrp_data down | helper pod | about 300 MB |

## Compute and cost estimate

Assumptions: 6-atom bilayer cells, SCF + NSCF + Wannier90, about 1 h per point on 8 cores.

- DFT sweep: 420 x 1 h x 8 cores, about 3,400 core-hours; at 40 parallel, about 11 waves, roughly 11-12 h wall-clock.
- Continuum sweep: about 200 x 20 min x 2 cores, about 130 core-hours, under 1 h wall-clock.
- GPU-hours: zero by choice. If the project moves to larger commensurate cells (hundreds of atoms), the NGC GPU build of QE on one standard GPU per task becomes worth testing.
- Nautilus: free to the user. Workstation (16 cores, 2 points at a time): about 9 days. HPCC: a strong fit for the same array, and the right place for multi-node runs. Commercial cloud at about $0.04/vCPU-hour: roughly $140-200 for the DFT sweep.

## Limits and honest caveats

- **Large twisted supercells do not fit.** Relaxing or computing bands for a small-angle twisted cell with thousands of atoms needs tightly coupled multi-node MPI and large memory; use the HPCC, Ursa Major (Slurm on GCP), or an ACCESS allocation. Nautilus is for the many independent small-to-medium cells around it.
- **Single-node only:** MPI within one pod works; nrp-mcp does not launch multi-node MPI jobs.
- **GPUs:** do not request them for small cells. Premium GPUs (A100/H100) have zero UCR quota by default; opportunistic use can be preempted.
- **Caps count pods at the same time:** `pods_per_run` (default 50) limits a sweep's `parallel`, and the total count is capped separately by `tasks_per_run` (default 10,000). The sweep time limit covers all waves.
- **Disk:** QE scratch can be several GB per task; keep `outdir` in the pod and write only small outputs to the volume.
- **Not an archive:** volumes idle 6 months are purged.

## Starter kit

```dockerfile
# Dockerfile - sketch (nrp_build writes a similar one)
FROM mambaorg/micromamba:latest
RUN micromamba install -y -n base -c conda-forge qe=7.5 wannier90 openmpi python=3.12 numpy pandas && \
    micromamba clean -a -y
COPY pseudo/ /opt/pseudo/
ENV PATH=/opt/conda/bin:$PATH OMP_NUM_THREADS=1
```

```bash
# run_point.sh - sketch: one grid row per task
set -euo pipefail
ROW=$(( ${SLURM_ARRAY_TASK_ID} + ${ROW_OFFSET:-0} ))
OUT=/data/runs/row_$ROW; mkdir -p "$OUT"
[ -f "$OUT/done" ] && exit 0
python make_inputs.py --row "$ROW" --pseudo /opt/pseudo --outdir /work/tmp --dest "$OUT"
cd "$OUT"
mpirun --allow-run-as-root -np 8 pw.x -nk 2 -in scf.in  > scf.out
mpirun --allow-run-as-root -np 8 pw.x -nk 2 -in nscf.in > nscf.out
wannier90.x -pp bilayer
mpirun --allow-run-as-root -np 8 pw2wannier90.x -in pw2wan.in > pw2wan.out
wannier90.x bilayer
rm -rf /work/tmp; touch done
```

```text
grid.csv   row,stacking,d_interlayer_A,strain_pct,efield_V_per_A
           0,AA,6.2,-1.0,0.00
```

## README blurb

Map how a layered material's electronic structure changes across a grid of stacking, spacing, strain and field values: each grid point runs Quantum ESPRESSO and Wannier90 in its own pod, using an image built on NRP GitLab, and writes small result files to a shared volume. A pilot sizes memory, nrp_watch pinpoints failed points for a targeted rerun, and a second, lightweight sweep solves the moire model across twist angles. Large multi-node DFT runs belong on a traditional HPC cluster; this workflow covers the many independent calculations around them.
