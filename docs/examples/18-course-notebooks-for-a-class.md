# 18. Notebooks and GPU exercises for a 40-student course

**Field:** Teaching (any discipline with notebook labs) | **UCR connection:** UCR Research Computing's KB030, "Teaching a class or workshop on Nautilus" (https://ucr-research-computing.github.io/kb/kb030-nautilus-teaching-and-workshops/, in the KB index at https://ucr-research-computing.github.io/kb/), written from NRP documentation and tests in a UCR namespace; modeled on a hands-on Nautilus workshop for a UCR statistics group, scaled to a term | **Fit:** Good (excellent for the hosted JupyterHub; nrp-mcp's part is the instructor's side)

## The science

The question here is teaching: how does an instructor give 40 students the same working Python or R environment for a 10-week term, with a GPU for the weeks that need one, without installs or a cloud bill? A typical course has weekly notebook labs, two or three GPU exercises (train a CNN, fine-tune a small model) and a final project.

Compute is the bottleneck twice over. Student laptops differ wildly, so the first weeks go to installation problems, and most have no usable GPU. And 40 students each training for an hour is 40 GPU-hours per exercise: a real cost on a commercial platform.

### Three ways to run it, compared honestly

| | A. NRP hosted JupyterHub | B. A private nrp session per student | C. The instructor runs batch jobs |
|---|---|---|---|
| Student needs | A browser, the UCR sign-in, namespace membership | nrp-mcp, kubectl, kubelogin, an AI client, membership | Nothing |
| Where it runs | https://jupyterhub-west.nrp-nautilus.io, in the NRP's own namespace | A Deployment per student in the class namespace, reached by port-forward | Jobs and sweeps in the class namespace |
| Shared class volume | No (use Git and public downloads) | Yes, /data | Yes, /data |
| GPU per student | Chosen at spawn | Yes, explicit GPU and confirm | Not per student |
| Lifetime | Stops 1 h after the browser disconnects; 5 GB home persists | Removed after 2 weeks; recreate per lab | Runs to completion |
| Best for | Almost every course | Courses on clusters and agents; advanced students | Preparing checkpoints, data, demos |

**For most courses, use A.** Nothing to install, and KB030 covers it step by step. B gives each student a private notebook on the class volume, but installing an AI client, kubectl and nrp-mcp for 40 students costs the first week. C is where nrp-mcp helps the instructor most. Do not run graded submissions on Nautilus: graded work is a student record.

**Membership.** The instructor is namespace admin and responsible for everything students run there. A training join link at nrp.ai adds everyone who opens it until a date you set, at most 90 days (a quarter plus finals fits if created near the start of term). For longer terms, add students directly and remove them at the end. Add TAs directly so a link never removes them.

## Who at UCR does this

Any department that teaches with notebooks: Statistics, Computer Science and Engineering, Economics, Psychology, the School of Public Policy, and graduate methods courses. The pattern is an instructor, one or two TAs and 20-60 students. UCR Research Computing has run a hands-on Nautilus workshop for a statistics group; this example stretches that flow over a term.

None of these groups use nrp-mcp today; this is an illustration.

## The data

- **Course materials:** notebooks in a Git repository (GitHub or the NRP GitLab, https://gitlab.nrp-nautilus.io).
- **Public teaching datasets**, for example CIFAR-10 (https://www.cs.toronto.edu/~kriz/cifar.html, about 163 MB) or public-domain texts.
- **Hosted models:** the NRP's LLM endpoint for an AI-in-research week (KB028).

**P1 check, and FERPA.** Course materials and public data are P1. **Student records are not:** the NRP states its systems are not suitable for FERPA-protected data. No grades, rosters with student IDs, graded submissions or feedback on Nautilus; grade in the campus learning management system. Course data above P1 goes to the HPCC, Ursa Major or the Secure Enclave.

## The conversation

> **Instructor:** I'm teaching a 40-student machine learning course this fall. Where do I stand on Nautilus, and what is the simplest setup?

> **Assistant:** [nrp_status] You are admin of the empty namespace ucr-example-ml-course. Regular GPUs are available; A100/H100/H200/GH200 quotas are 0. For 40 students the hosted JupyterHub is simplest (browser plus join link; KB030 is the guide). I can prepare the materials side and test each GPU exercise.

> **Instructor:** Week 6 fine-tunes a ResNet on CIFAR-10. Train a reference model for students who fall behind.

> **Assistant:** [nrp_plan goal=volume name=course-data size=50Gi] then [nrp_plan goal=pull data=https://www.cs.toronto.edu/~kriz/cifar-10-python.tar.gz]. The pull needs you to confirm the data is P1. CIFAR-10 is a public research dataset, so is that a yes?

> **Instructor:** Yes.

> **Assistant:** [nrp_run] volume Bound; [nrp_run] pull finished. [nrp_plan goal=job gpu=1 cpu=4 memory=16Gi command="python train_reference.py"] picks pytorch/pytorch, one regular GPU, 24 h limit, 2 retries. Confirm?

> **Instructor:** Confirm.

> **Assistant:** [nrp_run] Started. [nrp_watch] after a minute: failed, exit code 1, `ModuleNotFoundError: No module named 'timm'`. The stock image lacks it and the project has no requirements.txt. Fix: add `timm` to requirements.txt (installed at start), or use [nrp_build] for a reusable course image built on NRP GitLab.

> **Instructor:** Use requirements.txt for now and rerun.

> **Assistant:** Added. [nrp_plan] again, confirm, [nrp_run]. [nrp_watch]: running on an RTX A5000, GPU utilization 85%, epoch 12/30. Finished in 41 minutes, 93.1% test accuracy; checkpoint at /data/checkpoints/resnet18_cifar10.pt.

> **Instructor:** Students on the hosted hub can't mount that volume, right?

> **Assistant:** Correct. [nrp_data down path=/data/checkpoints] brings the 45 MB checkpoint to your laptop; publish it so the notebook can download it. To dry-run the student notebook: [nrp_plan goal=session gpu=1 cpu=4 memory=16Gi], a private Jupyter. A GPU session must be used actively, and Deployments are removed after 2 weeks. Confirm?

> **Instructor:** Confirm, and pin it to an A40 so the timings match my slides.

> **Assistant:** Replanned with gpu=a40, confirm, [nrp_run]. [nrp_watch] after 5 minutes: Pending, `Insufficient nvidia.com/gpu`: no free node with that GPU type right now. Fix: ask for any regular GPU instead of a specific model. Replanned with gpu=1; [nrp_session] shows it ready, with the port-forward command and a local URL with its sign-in token. On class day, keep a CPU path through each GPU exercise.

> **Instructor:** Notebook works. Clean up my test runs and keep the volume until the end of term.

> **Assistant:** [nrp_cleanup] lists the pull, both training jobs and the sessions; deletes them after your second yes. The volume stays. At the end of term, `nrp_cleanup everyone=true` (as admin) removes anything students made with nrp-mcp; objects made by hand with kubectl need KB030 section 11.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Class storage | `volume` | PVC, rook-cephfs-central, RWX | 50Gi |
| Public dataset | `pull` | Job | 1 CPU, 2Gi, minutes |
| Reference model | `job` | Job | 1 GPU, 4 CPU / 16Gi, under 1 h |
| Dry run of a lab | `session` | Deployment (1 replica) + port-forward | 1 GPU, 4 CPU / 16Gi, an hour |
| Course image (optional) | `nrp_build` | none (Dockerfile + GitLab CI; builds in NRP GitLab) | - |
| Students' notebooks | hosted JupyterHub (not nrp) | NRP's namespace | chosen at spawn |
| End of term | `nrp_cleanup everyone=true` | deletes labelled objects | - |

## Compute and cost estimate

Assumptions: 40 students, 10 weeks, CPU labs of about 2 hours a week on 1-2 cores, and three GPU exercises of about 2 GPU-hours each per student: about 800 CPU-core-hours and 240 GPU-hours, plus 5-10 GPU-hours of preparation.

- **Student laptops:** free but uneven; most have no CUDA GPU.
- **Commercial cloud notebooks:** 240 GPU-hours at roughly $0.50-$1.50 per hour is about $120-$360, plus CPU, storage and per-student accounts.
- **UCR HPCC:** possible, but accounts, Slurm and queues add friction for a course; ask Research Computing.
- **Nautilus:** free to the user, no recharge from UCR, but capacity is shared.

## Limits and honest caveats

- **No reserved capacity.** A class asking for GPUs at once can leave students Pending. Stagger GPU labs, pair students, keep a CPU path.
- **Join links last at most 90 days.** Semester courses add students directly.
- **The hosted hub cannot mount the class volume** (per KB030; to be confirmed with the NRP). Distribute data by Git or public URLs; public NRP S3 objects work too, but S3 is not yet tested by UCR RC.
- **Per-student nrp sessions** are Deployments: removed after 2 weeks, and a GPU session must stay above 40% utilization or the namespace is flagged.
- **5 GB home folders** on the hub fill fast with model weights.
- **Instructor responsibility.** Teach the day-one rules in KB030 section 10 (no `sleep` jobs, limits within 20% of requests, give GPUs back, no secrets in specs).
- **Your own course JupyterHub** is possible with Helm but needs a 2-week exception and upkeep; nrp-mcp does not deploy it.

## Starter kit

```
course-repo/
  labs/week06_resnet.ipynb
  train_reference.py
  requirements.txt
  STUDENT_SETUP.md      # join link, hub URL, which image and hardware to pick
```

`train_reference.py` (sketch):

```python
import torch, timm, torchvision as tv, torchvision.transforms as T
dev = "cuda" if torch.cuda.is_available() else "cpu"
tf = T.Compose([T.ToTensor(), T.Normalize((0.5,)*3, (0.5,)*3)])
train = tv.datasets.CIFAR10("/data/cifar", train=True, download=False, transform=tf)
dl = torch.utils.data.DataLoader(train, batch_size=256, shuffle=True, num_workers=4)
model = timm.create_model("resnet18", num_classes=10).to(dev)
opt = torch.optim.AdamW(model.parameters(), lr=1e-3)
for epoch in range(30):
    for x, y in dl:
        x, y = x.to(dev), y.to(dev)
        loss = torch.nn.functional.cross_entropy(model(x), y)
        opt.zero_grad(); loss.backward(); opt.step()
    torch.save(model.state_dict(), "/data/checkpoints/resnet18_cifar10.pt")
    print(f"epoch {epoch} loss {loss.item():.3f}", flush=True)
```

`requirements.txt`: `timm`

Student notebook cell (sketch, runs on the hosted hub):

```python
import torch, urllib.request
urllib.request.urlretrieve(CHECKPOINT_URL, "resnet18_cifar10.pt")  # public course file
dev = "cuda" if torch.cuda.is_available() else "cpu"   # CPU path still works
```

## README blurb

Teaching a class: students use the NRP's hosted JupyterHub with nothing to install, and the instructor uses nrp-mcp to prepare the course behind it, pulling public datasets into a class volume, training reference models on a standard GPU, dry-running each lab in a private notebook, and cleaning up everything at the end of term. Per-student private sessions are possible for advanced classes. Course materials and public data only; grades and student records stay in the campus learning management system.
