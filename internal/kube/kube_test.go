package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestKind(t *testing.T) {
	for _, tc := range []struct{ msg, want string }{
		{"error: You must be logged in to the server (Unauthorized)", KindSignedOut},
		{"error: the server has asked for the client to provide credentials", KindSignedOut},
		{"error: get-token: oidc: token has expired", KindSignedOut},
		{`Error from server (Forbidden): jobs.batch is forbidden: User cannot list`, KindForbidden},
		{"Unable to connect to the server: dial tcp 1.2.3.4:443: i/o timeout", KindTimeout},
		{`Error from server (NotFound): jobs.batch "x" not found`, KindNotFound},
		{"error: something odd", KindOther},
	} {
		if got := Kind(&Error{Args: []string{"get"}, Msg: tc.msg}); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.msg, got, tc.want)
		}
	}
	if Kind(context.DeadlineExceeded) != KindTimeout || Kind(nil) != "" {
		t.Fatal("deadline or nil")
	}
}

func TestLimitForSignInAndWait(t *testing.T) {
	r := &Runner{Timeout: 60 * time.Second}
	for _, tc := range []struct {
		args []string
		want time.Duration
	}{
		{[]string{"auth", "whoami"}, 5 * time.Minute},
		{[]string{"wait", "--for=condition=Ready", "pod/x", "--timeout=120s"}, 150 * time.Second},
		{[]string{"apply", "-f", "-"}, 2 * time.Minute},
		{[]string{"get", "pods"}, 60 * time.Second},
	} {
		if got := r.LimitFor(tc.args); got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.args, got, tc.want)
		}
	}
}

func TestSignInIsNotCutAtTheDefaultLimit(t *testing.T) {
	// Scaled down: default 50 ms, so sign-in gets 250 ms; the fake takes 100 ms.
	r := &Runner{Timeout: 50 * time.Millisecond, Exec: func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
		select {
		case <-time.After(100 * time.Millisecond):
			return []byte(`{"status":{"userInfo":{"username":"u"}}}`), nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}}
	if _, err := r.WhoAmI(context.Background()); err != nil {
		t.Fatalf("sign-in was cut off at the default limit: %v", err)
	}
	if _, err := r.Run(context.Background(), "", nil, "get", "pods"); Kind(err) != KindTimeout {
		t.Fatalf("an ordinary call past the default limit must time out, got %v", err)
	}
}
