package k8s

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// MarshalJSON writes the object with its spec under the right key.
func (o Object) MarshalJSON() ([]byte, error) {
	m := map[string]any{"apiVersion": o.APIVersion, "kind": o.Kind, "metadata": o.Metadata}
	switch {
	case o.JobSpec != nil:
		m["spec"] = o.JobSpec
	case o.DeploymentSpec != nil:
		m["spec"] = o.DeploymentSpec
	case o.ServiceSpec != nil:
		m["spec"] = o.ServiceSpec
	case o.IngressSpec != nil:
		m["spec"] = o.IngressSpec
	case o.PVCSpec != nil:
		m["spec"] = o.PVCSpec
	case o.PodSpec != nil:
		m["spec"] = o.PodSpec
	}
	if o.StringData != nil {
		m["stringData"] = o.StringData
		m["type"] = "Opaque"
	}
	if o.Data != nil {
		m["data"] = o.Data
	}
	return json.Marshal(m)
}

// UnmarshalJSON reads an object back (used for stored plans and user YAML-as-JSON).
func (o *Object) UnmarshalJSON(b []byte) error {
	var raw struct {
		APIVersion string            `json:"apiVersion"`
		Kind       string            `json:"kind"`
		Metadata   Meta              `json:"metadata"`
		Spec       json.RawMessage   `json:"spec"`
		StringData map[string]string `json:"stringData"`
		Data       map[string]string `json:"data"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	o.APIVersion, o.Kind, o.Metadata, o.StringData, o.Data = raw.APIVersion, raw.Kind, raw.Metadata, raw.StringData, raw.Data
	if len(raw.Spec) == 0 {
		return nil
	}
	var target any
	switch o.Kind {
	case "Job":
		o.JobSpec = &JobSpec{}
		target = o.JobSpec
	case "Deployment":
		o.DeploymentSpec = &DeploymentSpec{}
		target = o.DeploymentSpec
	case "Service":
		o.ServiceSpec = &ServiceSpec{}
		target = o.ServiceSpec
	case "Ingress":
		o.IngressSpec = &IngressSpec{}
		target = o.IngressSpec
	case "PersistentVolumeClaim":
		o.PVCSpec = &PVCSpec{}
		target = o.PVCSpec
	case "Pod":
		o.PodSpec = &PodSpec{}
		target = o.PodSpec
	default:
		return nil
	}
	return json.Unmarshal(raw.Spec, target)
}

// List wraps objects as a kubectl-applyable v1 List.
func List(objs []Object) ([]byte, error) {
	return json.MarshalIndent(map[string]any{"apiVersion": "v1", "kind": "List", "items": objs}, "", "  ")
}

// ParseCPU converts "500m", "2", "1.5" to millicores.
func ParseCPU(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty cpu")
	}
	if strings.HasSuffix(s, "m") {
		return strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("bad cpu %q", s)
	}
	return int64(f * 1000), nil
}

var units = []struct {
	suf string
	mul float64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
	{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
}

// ParseBytes converts "8Gi", "512Mi", "2G", "1000" to bytes.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty quantity")
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			f, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suf), 64)
			if err != nil {
				return 0, fmt.Errorf("bad quantity %q", s)
			}
			return int64(f * u.mul), nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("bad quantity %q", s)
	}
	return int64(f), nil
}

// ParseCount converts a GPU count like "1" to an int (0 on error).
func ParseCount(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// IntPtr returns a pointer to n.
func IntPtr(n int) *int { return &n }
