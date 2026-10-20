package results

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
)

// Keys are the person's NRP S3 keys (from https://nrp.ai/s3token/), kept on this computer.
type Keys struct{ Access, Secret string }

// LoadKeys reads KEY=VALUE lines (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY) from path.
// It refuses a file other people can read.
func LoadKeys(path string) (*Keys, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("no S3 keys at %s: run `nrp-mcp s3 setup` in a terminal (keys come from https://nrp.ai/s3token/)", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users; run: chmod 600 %s", path, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	k := &Keys{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		n, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(n) {
		case "AWS_ACCESS_KEY_ID":
			k.Access = v
		case "AWS_SECRET_ACCESS_KEY":
			k.Secret = v
		}
	}
	if k.Access == "" || k.Secret == "" {
		return nil, fmt.Errorf("%s needs AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY lines (run `nrp-mcp s3 setup`)", path)
	}
	return k, nil
}

// SaveKeys writes the keys file with mode 0600.
func SaveKeys(path string, k Keys) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := "# NRP S3 keys for nrp-mcp results (from https://nrp.ai/s3token/). Never share this file.\nAWS_ACCESS_KEY_ID=" + k.Access + "\nAWS_SECRET_ACCESS_KEY=" + k.Secret + "\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// BucketName is the default per-person bucket: nrp-results-<first 12 hex of owner id>.
func BucketName(ownerID string) string {
	if len(ownerID) > 12 {
		ownerID = ownerID[:12]
	}
	return "nrp-results-" + ownerID
}

// Client talks to NRP S3 from this computer with the person's keys.
type Client struct {
	Endpoint string // https://s3-west.nrp-nautilus.io
	Bucket   string
	Keys     Keys
	HTTP     *http.Client
	Now      func() time.Time
}

func (c *Client) do(ctx context.Context, method, rawURL string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	SignRequest(req, body, c.Keys.Access, c.Keys.Secret, now())
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	return hc.Do(req)
}

func (c *Client) bucketURL() string { return strings.TrimRight(c.Endpoint, "/") + "/" + c.Bucket }

// EnsureBucket creates the bucket if it is missing (private by default).
func (c *Client) EnsureBucket(ctx context.Context) error {
	resp, err := c.do(ctx, "HEAD", c.bucketURL(), nil)
	if err != nil {
		return fmt.Errorf("cannot reach NRP S3 at %s: %w", c.Endpoint, err)
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case 200:
		return nil
	case 403:
		return fmt.Errorf("NRP S3 refused your keys for bucket %s (403): check the keys from https://nrp.ai/s3token/, or pick another s3_bucket in the nrp-mcp config", c.Bucket)
	case 404:
	default:
		return fmt.Errorf("NRP S3 answered %d for bucket %s", resp.StatusCode, c.Bucket)
	}
	resp, err = c.do(ctx, "PUT", c.bucketURL(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("could not create bucket %s (%d): %s", c.Bucket, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Object is one stored file.
type Object struct {
	Key  string `xml:"Key"`
	Size int64  `xml:"Size"`
}

// List returns every object under prefix.
func (c *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := c.do(ctx, "GET", c.bucketURL()+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("listing %s/%s failed (%d): %s", c.Bucket, prefix, resp.StatusCode, strings.TrimSpace(string(b)))
		}
		var r struct {
			Contents              []Object `xml:"Contents"`
			IsTruncated           bool     `xml:"IsTruncated"`
			NextContinuationToken string   `xml:"NextContinuationToken"`
		}
		if err := xml.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Contents...)
		if !r.IsTruncated || r.NextContinuationToken == "" {
			return out, nil
		}
		token = r.NextContinuationToken
	}
}

// Download copies every object under prefix into dir, keeping the paths below prefix.
// It refuses keys that would land outside dir and never overwrites a file.
func (c *Client) Download(ctx context.Context, prefix, dir string) (int, int64, error) {
	objs, err := c.List(ctx, prefix)
	if err != nil {
		return 0, 0, err
	}
	var n int
	var total int64
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return 0, 0, err
	}
	for _, o := range objs {
		rel := strings.TrimPrefix(o.Key, prefix)
		dst := filepath.Join(absDir, filepath.FromSlash(rel))
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue
		}
		if !strings.HasPrefix(dst, absDir+string(os.PathSeparator)) {
			return n, total, fmt.Errorf("refusing %q: it would be written outside %s", o.Key, absDir)
		}
		if fi, err := os.Stat(dst); err == nil {
			if fi.Size() == o.Size {
				continue // already downloaded (an earlier, interrupted download)
			}
			return n, total, fmt.Errorf("%s already exists with different content; not overwriting (move it away and download again)", dst)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return n, total, err
		}
		resp, err := c.do(ctx, "GET", c.bucketURL()+"/"+uriEncode(o.Key, true), nil)
		if err != nil {
			return n, total, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return n, total, fmt.Errorf("download %s failed (%d)", o.Key, resp.StatusCode)
		}
		// Write to a temporary name and rename, so an interrupted download never leaves a
		// partial file that looks finished.
		tmp := dst + ".part"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			resp.Body.Close()
			return n, total, err
		}
		w, err := io.Copy(f, resp.Body)
		resp.Body.Close()
		f.Close()
		if err != nil {
			os.Remove(tmp)
			return n, total, err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return n, total, err
		}
		n++
		total += w
	}
	return n, total, nil
}

// UploaderImage is the small image the results sidecar uses (pinned; curl + sh).
const UploaderImage = "quay.io/curl/curl:8.23.0"

// uploaderScript waits for the main container to finish (SIGTERM), then posts every
// file under /work/results to the signed policy. Sweeps add task-<index>/.
const uploaderScript = `set -u
up() {
  cd /work/results 2>/dev/null || { echo "nrp-results: no /work/results folder, nothing to save"; exit 0; }
  sub=""
  if [ -n "${JOB_COMPLETION_INDEX:-}" ]; then sub="task-${JOB_COMPLETION_INDEX}/"; fi
  fails=/work/.nrp-results-failed
  rm -f "$fails"
  find . -type f | while IFS= read -r f; do
    k="${S3_PREFIX}${sub}${f#./}"
    # --form-string sends values as is (no ';' or ',' parsing); the file comes from stdin so
    # any file name works.
    code=$(curl -sS --retry 3 -o /dev/null -w "%{http_code}" -X POST "$S3_URL" \
      --form-string "key=${k}" --form-string "policy=${S3_POLICY}" --form-string "x-amz-algorithm=${S3_ALG}" \
      --form-string "x-amz-credential=${S3_CRED}" --form-string "x-amz-date=${S3_DATE}" \
      --form-string "x-amz-signature=${S3_SIG}" -F "file=@-" < "$f")
    case "$code" in
      20*) echo "nrp-results: saved ${k}" ;;
      *) echo "nrp-results: FAILED ${k} (HTTP ${code:-none})"; echo "$k" >> "$fails" ;;
    esac
  done
  if [ -s "$fails" ]; then echo "nrp-results: $(wc -l < "$fails" | tr -d ' ') file(s) FAILED to save"; exit 1; fi
  exit 0
}
trap up TERM INT
echo "nrp-results: waiting for the job to finish"
while true; do sleep 5 & wait $!; done`

// SecretName is the per-run Secret that carries the signed upload form.
func SecretName(jobName string) string { return jobName + "-results" }

// AttachUploader adds /work/results and the uploader sidecar (a native sidecar: an init
// container with restartPolicy Always, which Kubernetes stops after the main container
// ends, sending it SIGTERM). The signed form comes from Secret secretName at run time.
func AttachUploader(ps *k8s.PodSpec, secretName string) {
	env := []k8s.EnvVar{}
	for _, kv := range [][2]string{{"S3_URL", "url"}, {"S3_PREFIX", "prefix"}, {"S3_POLICY", "policy"}, {"S3_ALG", "x-amz-algorithm"},
		{"S3_CRED", "x-amz-credential"}, {"S3_DATE", "x-amz-date"}, {"S3_SIG", "x-amz-signature"}} {
		env = append(env, k8s.EnvVar{Name: kv[0], ValueFrom: &k8s.EnvVarSource{SecretKeyRef: &k8s.KeyRef{Name: secretName, Key: kv[1]}}})
	}
	env = append(env, k8s.EnvVar{Name: "JOB_COMPLETION_INDEX", ValueFrom: &k8s.EnvVarSource{FieldRef: &k8s.FieldRef{FieldPath: "metadata.annotations['batch.kubernetes.io/job-completion-index']"}}})
	always := "Always"
	ps.InitContainers = append(ps.InitContainers, k8s.Container{Name: "save-results", Image: UploaderImage, RestartPolicy: always,
		Command: []string{"sh", "-c", uploaderScript},
		Env:     env,
		Resources: k8s.Resources{Requests: map[string]string{"cpu": fmt.Sprintf("%dm", uploaderMilliCPU), "memory": fmt.Sprintf("%dMi", uploaderBytes>>20)},
			Limits: map[string]string{"cpu": fmt.Sprintf("%dm", uploaderMilliCPU), "memory": fmt.Sprintf("%dMi", uploaderBytes>>20)}},
		VolumeMounts: []k8s.Mount{{Name: "code", MountPath: "/work"}}})
	if ps.TerminationGracePeriodSeconds == nil {
		g := 300
		ps.TerminationGracePeriodSeconds = &g
	}
	for i := range ps.Containers {
		carve(&ps.Containers[i].Resources)
		ps.Containers[i].Env = append(ps.Containers[i].Env, k8s.EnvVar{Name: "RESULTS_DIR", Value: "/work/results"})
		if len(ps.Containers[i].Command) == 3 && ps.Containers[i].Command[0] == "bash" {
			ps.Containers[i].Command[2] = "mkdir -p /work/results; " + ps.Containers[i].Command[2]
		}
	}
}

// Uploader's share of the pod (requests = limits).
const (
	uploaderMilliCPU = 100
	uploaderBytes    = 64 << 20
)

// carve takes the uploader's share out of the main container, so the pod as a whole asks
// for what the plan said (for example 1 CPU / 2 GiB stays inside the NRP usage-check
// exemption). Containers too small to share keep their size.
func carve(r *k8s.Resources) {
	for _, m := range []map[string]string{r.Requests, r.Limits} {
		if m == nil {
			continue
		}
		if c, err := k8s.ParseCPU(m["cpu"]); err == nil && c > 2*uploaderMilliCPU {
			m["cpu"] = fmt.Sprintf("%dm", c-uploaderMilliCPU)
		}
		if b, err := k8s.ParseBytes(m["memory"]); err == nil && b > 4*uploaderBytes && b%(1<<20) == 0 {
			m["memory"] = fmt.Sprintf("%dMi", (b-uploaderBytes)>>20)
		}
	}
}

// FormSecret is the run-time Secret with the signed upload form (no S3 keys in it).
func FormSecret(name, ns string, labels map[string]string, f *PostForm) k8s.Object {
	sd := map[string]string{"url": f.URL, "prefix": f.Prefix}
	for k, v := range f.Fields {
		sd[k] = v
	}
	return k8s.Object{APIVersion: "v1", Kind: "Secret", Metadata: k8s.Meta{Name: name, Namespace: ns, Labels: labels}, StringData: sd}
}
