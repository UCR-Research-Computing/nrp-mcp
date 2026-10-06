package diagnose

import (
	"strings"
	"testing"
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
