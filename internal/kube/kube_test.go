package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNamespacesFromGroups(t *testing.T) {
	all, admin := NamespacesFromGroups([]string{"oidcgroup:ucr-example:admin", "oidcgroup:nautilus-user", "oidcgroup:lab-b:user", "system:authenticated"})
	if strings.Join(all, ",") != "ucr-example,lab-b" || strings.Join(admin, ",") != "ucr-example" {
		t.Fatalf("got %v %v", all, admin)
	}
}

func TestGPUQuota(t *testing.T) {
	q := GPUQuota([]Quota{{Name: "a100-limit", Hard: map[string]string{"requests.nvidia.com/a100": "0", "limits.nvidia.com/a100": "0"}}, {Name: "x", Hard: map[string]string{"pods": "0"}}})
	if v, ok := q["nvidia.com/a100"]; !ok || v != 0 || len(q) != 1 {
		t.Fatalf("got %v", q)
	}
}

func TestRunArgsAndErrors(t *testing.T) {
	r := New(Config{Kubeconfig: "/k", Context: "nautilus"})
	var got []string
	r.Exec = func(ctx context.Context, name string, args []string, stdin []byte) ([]byte, []byte, error) {
		got = append([]string{name}, args...)
		return nil, []byte(`Error from server (Forbidden): pods is forbidden`), errors.New("exit 1")
	}
	_, err := r.Run(context.Background(), "ns", nil, "get", "pods")
	if strings.Join(got, " ") != "kubectl --kubeconfig /k --context nautilus -n ns get pods" {
		t.Fatalf("args %v", got)
	}
	if !IsForbidden(err) || IsNotFound(err) {
		t.Fatalf("err classification: %v", err)
	}
}

func TestRetryOnceOnAuthRefresh(t *testing.T) {
	n := 0
	r := New(Config{})
	r.Exec = func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
		n++
		if n == 1 {
			return nil, []byte("error: the server has asked for the client to provide credentials"), errors.New("exit 1")
		}
		return []byte("ok"), nil, nil
	}
	out, err := r.Run(context.Background(), "ns", nil, "get", "pods")
	if err != nil || string(out) != "ok" || n != 2 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, n)
	}
	n = 0
	r.Exec = func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
		n++
		return nil, []byte("Error from server (Forbidden)"), errors.New("exit 1")
	}
	if _, err := r.Run(context.Background(), "ns", nil, "get", "pods"); err == nil || n != 1 {
		t.Fatalf("forbidden must not retry: calls=%d", n)
	}
}
