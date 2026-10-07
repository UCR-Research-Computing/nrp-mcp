# 08. Training video action-recognition models on public benchmarks

**Field:** Computer vision, machine learning | **UCR connection:** the Vision and Learning Group (formerly the Video Computing Group) in Electrical and Computer Engineering, which works on visual analysis and understanding, efficient learning, multi-modal data and adversarial robustness; and UCR machine-learning papers at ICML on vision-language safety and related topics (RAISE@UCR, https://raise.ucr.edu/presenting/2026/06/12/uc-riverside-researchers-unveil-groundbreaking-ai-advances-premier-global) | **Fit:** Excellent

## The science

Action recognition asks a model to say what is happening in a video clip: someone is "dribbling a basketball" or "chopping vegetables". Modern approaches pretrain a large video transformer, then fine-tune it on a benchmark and report accuracy on standard splits. Research questions tend to be about how: does a new adaptation method, a cheaper architecture, or a defense against adversarial frames hold up across datasets, learning rates and clip lengths?

Video is expensive. One 16-frame clip at 224 x 224 pixels is about 2.4 million input values, and a single fine-tuning run of a ViT-Base video model on a mid-sized benchmark takes several GPU-hours. A credible paper needs a hyperparameter sweep, several random seeds and ablations, which means dozens of runs. Each run fits on one standard GPU, and the runs are independent. That is Nautilus's sweet spot, as long as the GPUs stay busy and long runs can survive interruption.

## Who at UCR does this

Computer vision and machine learning groups in Electrical and Computer Engineering and in Computer Science and Engineering. The Vision and Learning Group's site lists recent papers at CVPR, ICLR, ICRA and ECCV 2026, and the RAISE story lists the group's principal investigator as a co-author on two of three UCR papers at ICML. Robotics, autonomous-driving perception and multimedia groups run similar training sweeps. None of these groups use nrp-mcp today; this is an illustration.

## The data

- UCF101: 13,320 YouTube clips in 101 action classes (https://crcv.ucf.edu/research/data-sets/ucf101/). The archive is 6.9 GB; frames decoded to JPEG take roughly 30 to 60 GB. Check the dataset page's terms and cite the authors.
- Kinetics-400 / 700: up to 650,000 roughly 10-second clips, with copies hosted by the CVDF (https://github.com/cvdfoundation/kinetics-dataset). Kinetics-400 alone is about 240 training archives of around 1.6 GB each, roughly 400 GB. Google publishes the annotations under CC BY 4.0. The clips come from YouTube, so treat them as research-only and do not redistribute them.
- Pretrained weights: VideoMAE checkpoints on Hugging Face (for example https://huggingface.co/MCG-NJU/videomae-base). The VideoMAE code is CC BY-NC 4.0 (non-commercial, fine for academic work); MMAction2 (https://github.com/open-mmlab/mmaction2) is Apache-2.0.
- P1 check: public benchmark data is fine on Nautilus. Video recorded by the lab, such as study participants, classrooms, clinics or campus cameras, is human-subjects data under IRB oversight and often identifiable (faces, voices). It does not go to Nautilus; ask UCR Research Computing about the Secure Enclave or a Tier 2 Ursa Major project. Some person re-identification datasets have been withdrawn by their creators; do not use withdrawn data.

## The conversation

**Researcher:** I want to fine-tune VideoMAE-Base on UCF101 split 1 with 12 configurations: three learning rates, two layer-decay values, and 16 or 32 frames. One GPU each. Then one long final run of the best config.

**Assistant:** [nrp_status] Namespace `vlg-video`, signed in. Standard GPUs are available; A100/H100 quota is 0. Your cap is 4 GPUs at the same time. [nrp_plan goal=volume size=250Gi data_is_p1=true] [nrp_plan goal=pull data_volume=video-bench subdir=ucf101 urls=[UCF101.rar, train/test split zip] data_is_p1=true] Approve both?

**Researcher:** Yes.

**Assistant:** [nrp_run ...] [nrp_run ...] [nrp_watch target=run-0a3f] The download Job failed, and the last 40 log lines show `curl: (60) SSL certificate problem: unable to get local issuer certificate`. The UCF server does not send its intermediate certificate. Browsers fill it in, but curl does not. Fix: a one-off job whose command first fetches the issuer's published intermediate certificate (InCommon RSA OV SSL CA 3, from crt.sectigo.com) and passes it to curl with `--cacert`, then unpacks the rar with 7-Zip and decodes frames to JPEG with ffmpeg. Decoding once up front also helps the GPU later. [nrp_plan goal=job command="bash prep.sh" cpu=8 memory=16Gi image=python:3.12 data_volume=video-bench] (`prep.sh` installs p7zip and ffmpeg with apt first.) Approve? [nrp_run ...] Done in 1 h 50 min: 13,320 clips, 41 GB of frames.

**Researcher:** Now the sweep.

**Assistant:** `train.py` maps `$JOB_COMPLETION_INDEX` to one row of `configs.yaml`, one config per task. [nrp_plan goal=sweep count=12 parallel=4 gpu=1 cpu=8 memory=32Gi hours=30 data_volume=video-bench] Plan summary: "A sweep of 12 tasks, 1 GPU each (any standard GPU), 8 CPU and 32 GiB, limits equal to requests. Indexed Job: 12 tasks, 4 at a time. Adds an 8 GiB /dev/shm so PyTorch data loaders do not crash. Image pytorch/pytorch; requirements installed at start." Your cap counts GPUs in use at the same time, so 4 at a time fits the default; all 12 configs still run, in 3 waves. The 30-hour limit covers the whole Job, all 3 waves, not each task. Approve?

**Researcher:** Approve.

**Assistant:** [nrp_run ...] After 40 minutes: [nrp_watch target=run-6b21 grep="data_time"] Your log line reads `iter 1.10s, data_time 0.82s`. The GPU waits on data about three-quarters of the time, so utilization is around 25%. That is under the NRP's 40% guideline, and the account could be flagged. The cause: the 32-frame configs read 32 JPEGs per clip through 4 data-loader workers. Fix: raise `num_workers` to 7 (one under the 8 CPUs) and pack each clip's frames into a single file, so one read replaces 32. I will stop this run, [nrp_cleanup run=run-6b21] two-step, add the packing step to `prep.sh` as a short CPU job, then rerun. [nrp_plan ...] [nrp_run ...] Now `data_time 0.06s` against `iter 0.41s`, so the GPU is mostly busy; the NRP's GPU dashboards confirm it. All 12 tasks finished in 16 h. Best: lr 5e-4, layer decay 0.75, 16 frames, 91.8% top-1 on split 1.

**Researcher:** For the final run, can we use an A100?

**Assistant:** Your A100 quota is 0. `opportunistic` priority can use one, but the pod can be preempted at any time. `train.py` saves a checkpoint to `/data/ckpt` every epoch and resumes from the latest, so that is acceptable. [nrp_plan goal=job gpu=1 gpu_type=a100 opportunistic=true cpu=8 memory=48Gi hours=24 data_volume=video-bench command="python train.py --config best --epochs 100 --seeds 3"] Decision noted in the plan: "priorityClassName opportunistic: bypasses the special-GPU quota, but the pod can be preempted at any time. Checkpoint your work." Approve? [nrp_run ...] Nine hours in: [nrp_watch target=run-c77d] "exit 137 without OOM: killed (deadline or preemption)." It was preempted. The Job's retry started a new pod, which loaded the epoch-61 checkpoint and carried on. Finished in 14 h total. [nrp_data down volume=video-bench path=/results/] brings back metrics and the final checkpoint (330 MB). [nrp_cleanup] then lists the runs; keep the volume while you write the paper.

## What nrp builds

| Step | nrp goal/tool | Kubernetes object | Size |
|---|---|---|---|
| Storage | nrp_plan goal=volume | PVC (CephFS, RWX) | 250 GiB |
| Download try | nrp_plan goal=pull | Job | failed on TLS chain |
| Download + decode | nrp_plan goal=job | Job + ConfigMap (prep.sh) | 8 CPU / 16 GiB, ~2 h |
| Hyperparameter sweep | nrp_plan goal=sweep | Indexed Job + ConfigMap | 12 tasks (4 at a time), 1 GPU / 8 CPU / 32 GiB each, ~16 h |
| Final run | nrp_plan goal=job, opportunistic | Job (priorityClassName opportunistic) | 1 A100 / 8 CPU / 48 GiB, ~14 h with one preemption |
| Results | nrp_data down | helper pod + kubectl cp | ~0.5 GB |

## Compute and cost estimate

Assumptions: about 9,500 training clips, 50 epochs per sweep run, and 25 to 40 clips per second on an A40/A6000-class GPU in mixed precision once data loading is fixed. That gives about 4 to 6 GPU-hours per run, about 60 GPU-hours for the sweep, and 15 to 25 more for the final runs (3 seeds), so roughly 80 GPU-hours in all. Free on Nautilus.

Comparison: a laptop cannot do this. One lab workstation GPU would take three to four days of continuous running. The UCR HPCC's GPU partitions work well if the lab has an account, with queue times varying. On a commercial cloud, 80 GPU-hours costs roughly $80 to $130 on L4/A10G-class instances, or $250 to $350 on A100s, plus storage. For full Kinetics pretraining (thousands of GPU-hours, multi-GPU), consider a NAIRR Pilot or ACCESS allocation.

## Limits and honest caveats

- GPU utilization is policed (above 40%). Fix data loading before scaling up, and never request GPUs for preprocessing.
- The default cap is 4 GPUs at the same time, so 12 configs run as 12 tasks, 4 at a time, in 3 waves. One pod can hold up to 8 GPUs for single-node multi-GPU training if you raise `gpus_per_run` on purpose (be courteous on a shared cluster). Multi-node distributed training is a poor fit for Nautilus.
- Premium GPUs are quota 0 at UCR. Opportunistic use gets preempted, so checkpoint every epoch at least and make resume automatic.
- An Indexed Job's time limit covers the whole sweep, so size `hours` for all the waves (cap 48 h). With the default retry budget, one task that keeps failing can stop the whole sweep; scripts should skip finished work so a rerun is cheap.
- Full Kinetics (about 400 GB) fits on a volume, but expect slow first reads from CephFS (about 86 MiB/s per writer in a UCR test). Pack frames into large files.
- Datasets drawn from YouTube are research-only; do not publish the clips. Lab-recorded human video is IRB data and stays off Nautilus.
- Volumes are purged after 6 months of inactivity. Keep final checkpoints elsewhere.

## Starter kit

Sketch, not tested code.

`train.py` (key parts)
```python
import os, glob, torch, yaml

def run(cfg):                                            # lr, layer_decay, frames, seed
    ckdir = f"/data/ckpt/{cfg['name']}"; os.makedirs(ckdir, exist_ok=True)
    if os.path.exists(f"/data/results/{cfg['name']}.json"):
        return                                           # finished earlier; skip
    model, opt, sched = build(cfg)                       # VideoMAE-Base from HF weights
    start = 0
    if (ck := sorted(glob.glob(f"{ckdir}/ep*.pt"))):
        s = torch.load(ck[-1], map_location="cuda")      # resume after preemption
        model.load_state_dict(s["model"]); opt.load_state_dict(s["opt"]); start = s["epoch"] + 1
    loader = make_loader("/data/ucf101/packed", cfg, num_workers=7, pin_memory=True)
    for epoch in range(start, cfg["epochs"]):
        train_one_epoch(model, loader, opt, sched, amp=True)  # logs iter and data_time
        torch.save({"model": model.state_dict(), "opt": opt.state_dict(), "epoch": epoch},
                   f"{ckdir}/ep{epoch:03d}.pt")
    evaluate_and_write(model, f"/data/results/{cfg['name']}.json")

idx = int(os.environ.get("JOB_COMPLETION_INDEX", "0"))  # 0..11
run(yaml.safe_load(open("configs.yaml"))[idx])           # one config per GPU task
```

`requirements.txt`
```
transformers>=4.44
decord
pyyaml
```

## README blurb

Run a hyperparameter sweep for a video model on public benchmarks on standard GPUs, running a few configurations at a time so the sweep stays within your GPU cap. Data is downloaded and decoded inside the cluster, low GPU utilization shows up in the training logs before the account is flagged, and a long final run can borrow a premium GPU at opportunistic priority because it resumes from its last checkpoint after preemption. Only public benchmark video belongs here; lab-recorded video of people needs a secure environment.
