package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/inspect"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
)

func TestCleanStripsEscapesAndCaps(t *testing.T) {
	in := "ok\x1b[31mRED\x1b[0m\x07 bell\u202eevil\nnext	line"
	got := Clean(in, 0)
	if got != "okRED bellevil\nnext	line" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("a", 100) + "END"
	if got := Clean(long, 10); !strings.HasSuffix(got, "aaaaaaaEND") || !strings.HasPrefix(got, "[... earlier") {
		t.Fatalf("%q", got)
	}
}

func TestAttachCodeMountsReadOnlyAndUnpacksInMain(t *testing.T) {
	pk := &inspect.Pack{Names: []string{"main.py", "pkg/sub.py"}, Files: map[string]string{"main.py": "print(1)\n", "pkg/sub.py": "x=2\n"}}
	cm := CodeConfigMap(pk, "app-code", "ns", nil)
	if cm == nil || cm.Data["pkg__SLASH__sub.py"] != "x=2\n" {
		t.Fatalf("cm %+v", cm)
	}
	obj := k8s.Object{Kind: "Job", JobSpec: &k8s.JobSpec{Template: k8s.PodTemplate{Spec: k8s.PodSpec{
		Containers: []k8s.Container{{Name: "main", Image: "python:3.12-slim-bookworm", Command: []string{"bash", "-c", "set -e; cd /work; python main.py"},
			VolumeMounts: []k8s.Mount{{Name: "code", MountPath: "/work"}}}},
		Volumes: []k8s.Volume{{Name: "code", EmptyDir: &k8s.EmptyDir{}}},
	}}}}
	objs := []k8s.Object{obj}
	AttachCode(objs, "app-code")
	ps := objs[0].PodSpecOf()
	if len(ps.InitContainers) != 0 {
		t.Fatalf("AttachCode must not add a busybox initContainer: %+v", ps.InitContainers)
	}
	hasMount := false
	for _, m := range ps.Containers[0].VolumeMounts {
		if m.Name == "code-src" && m.MountPath == "/code" && m.ReadOnly {
			hasMount = true
		}
	}
	if !hasMount {
		t.Fatalf("missing read-only /code mount: %+v", ps.Containers[0].VolumeMounts)
	}
	// Run the generated unpack snippet in sh and confirm nested files are copied to /work.
	if _, err := exec.LookPath("sh"); err == nil {
		codeDir := t.TempDir()
		workDir := t.TempDir()
		for k, v := range cm.Data {
			_ = os.WriteFile(filepath.Join(codeDir, k), []byte(v), 0o644)
		}
		script := strings.ReplaceAll(strings.ReplaceAll(unpackScript, "/code", codeDir), "/work", workDir)
		if out, err := exec.Command("sh", "-c", script+"true").CombinedOutput(); err != nil {
			t.Fatalf("unpack script failed: %v %s", err, out)
		}
		if b, err := os.ReadFile(filepath.Join(workDir, "pkg", "sub.py")); err != nil || string(b) != "x=2\n" {
			t.Fatalf("unpacked file %q (%v)", b, err)
		}
	}
}
