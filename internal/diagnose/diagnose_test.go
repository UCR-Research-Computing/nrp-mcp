package diagnose

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOOM(t *testing.T) {
	f := Explain(Pod{Name: "p", Phase: "Failed", Containers: []Container{{Name: "main", Terminated: "OOMKilled", ExitCode: 137}}})
	if f.State != "OOMKilled" || !strings.Contains(f.Fix, "memory") {
		t.Fatalf("%+v", f)
	}
}

func TestQuota(t *testing.T) {
	f := Explain(Pod{Name: "p", Phase: "Pending", Events: []Event{{Reason: "FailedCreate", Message: `pods "x" is forbidden: exceeded quota: a100-limit, requested: requests.nvidia.com/a100=1`}}})
	if !strings.Contains(f.Explain, "quota") || !strings.Contains(f.Fix, "opportunistic") {
		t.Fatalf("%+v", f)
	}
}

func TestNoGPU(t *testing.T) {
	f := Explain(Pod{Name: "p", Phase: "Pending", Events: []Event{{Reason: "FailedScheduling", Message: "0/500 nodes are available: 300 Insufficient nvidia.com/a40."}}})
	if !strings.Contains(f.Fix, "standard GPU") {
		t.Fatalf("%+v", f)
	}
}

func TestImagePull(t *testing.T) {
	f := Explain(Pod{Name: "p", Phase: "Pending", Containers: []Container{{Name: "m", Waiting: "ImagePullBackOff", WaitingMsg: "manifest unknown"}}})
	if f.State != "ImagePullBackOff" || !strings.Contains(f.Explain, "manifest unknown") {
		t.Fatalf("%+v", f)
	}
}

func TestExitCodes(t *testing.T) {
	f := Explain(Pod{Name: "p", Phase: "Failed", Containers: []Container{{Name: "m", Terminated: "Error", ExitCode: 132}}})
	if !strings.Contains(f.Explain, "illegal instruction") {
		t.Fatalf("%+v", f)
	}
}

func TestFailedMount(t *testing.T) {
	f := Explain(Pod{Name: "p", Phase: "Pending", Node: "n1", Events: []Event{{Reason: "FailedMount", Message: "MountVolume.MountDevice failed: driver name rook-system.cephfs.csi.ceph.com not found"}}})
	if !strings.Contains(f.Fix, "another node") {
		t.Fatalf("%+v", f)
	}
}

var pendNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func ago(m int) time.Time { return pendNow.Add(-time.Duration(m) * time.Minute) }

func TestPendingCases(t *testing.T) {
	cases := []struct {
		name      string
		pod       Pod
		state     string
		explain   []string
		fix       string // substring; "" means Fix must be empty
		ageMin    int
		node      string
		noExplain string
	}{
		{
			name:    "a no node, young",
			pod:     Pod{Name: "p", Phase: "Pending", Created: ago(3), Now: pendNow},
			state:   "Waiting for a node",
			explain: []string{"Not on a node yet (3 min)."},
			ageMin:  3,
		},
		{
			name:    "a no node, old",
			pod:     Pod{Name: "p", Phase: "Pending", Created: ago(12), Now: pendNow},
			state:   "Waiting for a node",
			explain: []string{"Not on a node yet (12 min)."},
			fix:     "Wait a few minutes, or loosen the request.",
			ageMin:  12,
		},
		{
			name: "b slow node beats pulling",
			pod: Pod{Name: "p", Phase: "Pending", Node: "k8s-haosu-20", Created: ago(30), Scheduled: ago(14), Now: pendNow,
				Containers: []Container{{Name: "main", Image: "python:3.12-slim", Waiting: "ContainerCreating"}},
				Events:     []Event{{Type: "Normal", Reason: "Pulling", Message: `Pulling image "python:3.12-slim"`}}},
			state:   "Slow node",
			explain: []string{"On k8s-haosu-20 for 14 min with no progress; this node may be slow or unhealthy."},
			fix:     "Re-plan with avoid_node=k8s-haosu-20 in nrp_plan, then nrp_run (the old pod is replaced).",
			ageMin:  14,
			node:    "k8s-haosu-20",
		},
		{
			name: "b slow node beats init, falls back to Created",
			pod: Pod{Name: "p", Phase: "Pending", Node: "n1", Created: ago(11), Now: pendNow,
				Containers: []Container{{Name: "unpack-code", Init: true, Running: true}}},
			state:   "Slow node",
			explain: []string{"On n1 for 11 min"},
			fix:     "avoid_node=n1",
			ageMin:  11,
			node:    "n1",
		},
		{
			name: "c init running",
			pod: Pod{Name: "p", Phase: "Pending", Node: "n1", Created: ago(5), Scheduled: ago(2), Now: pendNow,
				Containers: []Container{{Name: "unpack-code", Init: true, Running: true}, {Name: "main", Image: "python:3.12-slim", Waiting: "PodInitializing"}}},
			state:   "Starting",
			explain: []string{"On n1, preparing (unpacking your code) for 2 min."},
			ageMin:  2,
			node:    "n1",
		},
		{
			name: "c init waiting",
			pod: Pod{Name: "p", Phase: "Pending", Node: "n1", Scheduled: ago(1), Now: pendNow,
				Containers: []Container{{Name: "unpack-code", Init: true, Image: "busybox:1.36", Waiting: "PodInitializing"}, {Name: "main", Image: "python:3.12-slim", Waiting: "PodInitializing"}}},
			// Live 2026-10-15: a waiting init container is still downloading its own image.
			state:   "Pulling image",
			explain: []string{"On n1, downloading busybox:1.36 for 1 min."},
			fix:     "Don't redeploy",
			ageMin:  1,
			node:    "n1",
		},
		{
			name: "d pulling event names image",
			pod: Pod{Name: "p", Phase: "Pending", Node: "k8s-haosu-20", Scheduled: ago(4), Now: pendNow,
				Containers: []Container{{Name: "unpack-code", Init: true, Terminated: "Completed"}, {Name: "main", Image: "python:3.12", Waiting: "PodInitializing"}},
				Events:     []Event{{Type: "Normal", Reason: "Pulling", Message: `Pulling image "python:3.12-slim"`}}},
			state:   "Pulling image",
			explain: []string{"On k8s-haosu-20, downloading python:3.12-slim for 4 min. A first pull on a node can take 5-10 min."},
			fix:     "Don't redeploy; that restarts the download.",
			ageMin:  4,
			node:    "k8s-haosu-20",
		},
		{
			name: "d container creating uses main image",
			pod: Pod{Name: "p", Phase: "Pending", Node: "n2", Scheduled: ago(0), Now: pendNow,
				Containers: []Container{{Name: "main", Image: "ghcr.io/x/y:1", Waiting: "ContainerCreating"}}},
			state:   "Pulling image",
			explain: []string{"On n2, downloading ghcr.io/x/y:1 for 0 min."},
			fix:     "Don't redeploy",
			node:    "n2",
		},
		{
			name:      "d no info, no image text",
			pod:       Pod{Name: "p", Phase: "Pending", Node: "n3", Scheduled: pendNow.Add(time.Minute), Now: pendNow},
			state:     "Pulling image",
			explain:   []string{"On n3, downloading for 0 min."},
			fix:       "Don't redeploy",
			node:      "n3",
			noExplain: "for -",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Explain(c.pod)
			if f.State != c.state {
				t.Fatalf("state %q want %q: %+v", f.State, c.state, f)
			}
			for _, w := range c.explain {
				if !strings.Contains(f.Explain, w) {
					t.Fatalf("explain %q missing %q", f.Explain, w)
				}
			}
			if c.noExplain != "" && strings.Contains(f.Explain, c.noExplain) {
				t.Fatalf("explain %q must not contain %q", f.Explain, c.noExplain)
			}
			if c.fix == "" && f.Fix != "" {
				t.Fatalf("fix should be empty, got %q", f.Fix)
			}
			if !strings.Contains(f.Fix, c.fix) {
				t.Fatalf("fix %q missing %q", f.Fix, c.fix)
			}
			if f.AgeMinutes != c.ageMin || f.Node != c.node {
				t.Fatalf("age %d node %q, want %d %q", f.AgeMinutes, f.Node, c.ageMin, c.node)
			}
		})
	}
}

func TestPendingFailuresStillWin(t *testing.T) {
	base := func(ev Event) Pod {
		return Pod{Name: "p", Phase: "Pending", Node: "n1", Scheduled: ago(30), Now: pendNow,
			Containers: []Container{{Name: "main", Image: "img", Waiting: "ContainerCreating"}}, Events: []Event{ev}}
	}
	mount := Explain(base(Event{Reason: "FailedMount", Message: "MountVolume failed"}))
	if mount.State != "ContainerCreating" || !strings.Contains(mount.Fix, "another node") {
		t.Fatalf("FailedMount lost: %+v", mount)
	}
	q := Explain(Pod{Name: "p", Phase: "Pending", Created: ago(30), Now: pendNow, Events: []Event{{Reason: "FailedCreate", Message: "exceeded quota: x"}}})
	if !strings.Contains(q.Explain, "quota") {
		t.Fatalf("quota lost: %+v", q)
	}
	fs := Explain(Pod{Name: "p", Phase: "Pending", Created: ago(30), Now: pendNow, Events: []Event{{Reason: "FailedScheduling", Message: "0/5 nodes: Insufficient memory"}}})
	if !strings.Contains(fs.Fix, "Lower cpu/memory") {
		t.Fatalf("FailedScheduling lost: %+v", fs)
	}
	pull := Explain(Pod{Name: "p", Phase: "Pending", Node: "n1", Scheduled: ago(30), Now: pendNow, Containers: []Container{{Name: "m", Waiting: "ErrImagePull", WaitingMsg: "not found"}}})
	if pull.State != "ErrImagePull" {
		t.Fatalf("image pull error lost: %+v", pull)
	}
}

func TestFindingDetails(t *testing.T) {
	var evs []Event
	for i := 0; i < 7; i++ {
		evs = append(evs, Event{Type: "Normal", Reason: fmt.Sprintf("R%d", i), Message: "line1\nline2"})
	}
	f := Explain(Pod{Name: "p", Phase: "Pending", Node: "n1", Scheduled: ago(2), Now: pendNow, Events: evs,
		Containers: []Container{
			{Name: "unpack-code", Init: true, Image: "busybox", Terminated: "Completed"},
			{Name: "main", Image: "python:3.12-slim", Waiting: "ContainerCreating"},
		}})
	if len(f.Events) != 5 || f.Events[0].Reason != "R2" || f.Events[4].Reason != "R6" || f.Events[0].Message != "line1" {
		t.Fatalf("events %+v", f.Events)
	}
	if len(f.Containers) != 2 || f.Containers[0].State != "terminated:Completed" || f.Containers[1].State != "waiting:ContainerCreating" || f.Containers[1].Image != "python:3.12-slim" {
		t.Fatalf("containers %+v", f.Containers)
	}
	b, _ := json.Marshal(f)
	for _, k := range []string{`"node":"n1"`, `"age_minutes":2`, `"containers":`, `"events":`, `"reason":"R6"`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("json missing %s: %s", k, b)
		}
	}
	r := Explain(Pod{Name: "p", Phase: "Running", Node: "n1", Containers: []Container{{Name: "main", Image: "img", Running: true, Ready: true}}})
	if r.Node != "n1" || len(r.Containers) != 1 || r.Containers[0].State != "running" {
		t.Fatalf("running details %+v", r)
	}
}
