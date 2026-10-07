// Package k8s holds the small, typed subset of Kubernetes objects nrp-mcp writes.
// Keeping our own structs (instead of client-go) keeps the binary small and lets the
// rules engine and tests work on plain data. Output is JSON, which kubectl accepts.
package k8s

// Meta is object metadata.
type Meta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Resources are requests and limits, e.g. {"cpu":"2","memory":"8Gi","nvidia.com/gpu":"1"}.
type Resources struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// EnvVar is a container env var, literal or from a Secret.
type EnvVar struct {
	Name      string        `json:"name"`
	Value     string        `json:"value,omitempty"`
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty"`
}

// EnvVarSource points to a Secret key or a field.
type EnvVarSource struct {
	SecretKeyRef *KeyRef   `json:"secretKeyRef,omitempty"`
	FieldRef     *FieldRef `json:"fieldRef,omitempty"`
}

// KeyRef selects a key of a Secret.
type KeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// FieldRef selects a pod field.
type FieldRef struct {
	FieldPath string `json:"fieldPath"`
}

// Port is a container port.
type Port struct {
	ContainerPort int    `json:"containerPort"`
	Name          string `json:"name,omitempty"`
}

// Mount mounts a volume.
type Mount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// Probe is an HTTP readiness probe.
type Probe struct {
	HTTPGet             *HTTPGet `json:"httpGet,omitempty"`
	InitialDelaySeconds int      `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int      `json:"periodSeconds,omitempty"`
}

// HTTPGet is a probe target.
type HTTPGet struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

// Container is a pod container.
type Container struct {
	Name           string    `json:"name"`
	Image          string    `json:"image"`
	Command        []string  `json:"command,omitempty"`
	Args           []string  `json:"args,omitempty"`
	WorkingDir     string    `json:"workingDir,omitempty"`
	Env            []EnvVar  `json:"env,omitempty"`
	Ports          []Port    `json:"ports,omitempty"`
	Resources      Resources `json:"resources"`
	VolumeMounts   []Mount   `json:"volumeMounts,omitempty"`
	ReadinessProbe *Probe    `json:"readinessProbe,omitempty"`
}

// Volume is a pod volume (PVC, emptyDir, ConfigMap or Secret).
type Volume struct {
	Name                  string           `json:"name"`
	PersistentVolumeClaim *PVCSource       `json:"persistentVolumeClaim,omitempty"`
	EmptyDir              *EmptyDir        `json:"emptyDir,omitempty"`
	ConfigMap             *ConfigMapSource `json:"configMap,omitempty"`
	Secret                *SecretVolumeSrc `json:"secret,omitempty"`
}

// PVCSource references a PVC.
type PVCSource struct {
	ClaimName string `json:"claimName"`
}

// EmptyDir is scratch space.
type EmptyDir struct {
	Medium    string `json:"medium,omitempty"`
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// ConfigMapSource mounts a ConfigMap.
type ConfigMapSource struct {
	Name string `json:"name"`
}

// SecretVolumeSrc mounts a Secret.
type SecretVolumeSrc struct {
	SecretName string `json:"secretName"`
}

// PodSpec is the pod template spec.
type PodSpec struct {
	RestartPolicy     string      `json:"restartPolicy,omitempty"`
	PriorityClassName string      `json:"priorityClassName,omitempty"`
	Containers        []Container `json:"containers"`
	InitContainers    []Container `json:"initContainers,omitempty"`
	Volumes           []Volume    `json:"volumes,omitempty"`
	ImagePullSecrets  []NameRef   `json:"imagePullSecrets,omitempty"`
}

// NameRef is a reference by name (imagePullSecrets).
type NameRef struct {
	Name string `json:"name"`
}

// PodTemplate wraps a pod spec.
type PodTemplate struct {
	Metadata Meta    `json:"metadata"`
	Spec     PodSpec `json:"spec"`
}

// JobSpec is a batch/v1 Job spec.
type JobSpec struct {
	Completions             *int        `json:"completions,omitempty"`
	Parallelism             *int        `json:"parallelism,omitempty"`
	CompletionMode          string      `json:"completionMode,omitempty"`
	BackoffLimit            *int        `json:"backoffLimit,omitempty"`
	ActiveDeadlineSeconds   *int        `json:"activeDeadlineSeconds,omitempty"`
	TTLSecondsAfterFinished *int        `json:"ttlSecondsAfterFinished,omitempty"`
	Template                PodTemplate `json:"template"`
}

// LabelSelector matches labels.
type LabelSelector struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

// DeploymentSpec is an apps/v1 Deployment spec.
type DeploymentSpec struct {
	Replicas int           `json:"replicas"`
	Selector LabelSelector `json:"selector"`
	Template PodTemplate   `json:"template"`
}

// ServicePort is a Service port.
type ServicePort struct {
	Port       int `json:"port"`
	TargetPort int `json:"targetPort"`
}

// ServiceSpec is a ClusterIP Service spec.
type ServiceSpec struct {
	Selector map[string]string `json:"selector"`
	Ports    []ServicePort     `json:"ports"`
	Type     string            `json:"type,omitempty"`
}

// IngressSpec is a networking.k8s.io/v1 Ingress spec.
type IngressSpec struct {
	IngressClassName string        `json:"ingressClassName"`
	Rules            []IngressRule `json:"rules"`
	TLS              []IngressTLS  `json:"tls,omitempty"`
}

// IngressRule routes a host.
type IngressRule struct {
	Host string           `json:"host"`
	HTTP IngressRuleValue `json:"http"`
}

// IngressRuleValue holds paths.
type IngressRuleValue struct {
	Paths []IngressPath `json:"paths"`
}

// IngressPath routes a path to a backend.
type IngressPath struct {
	Path     string         `json:"path"`
	PathType string         `json:"pathType"`
	Backend  IngressBackend `json:"backend"`
}

// IngressBackend is the target service.
type IngressBackend struct {
	Service IngressServiceBackend `json:"service"`
}

// IngressServiceBackend names a service and port.
type IngressServiceBackend struct {
	Name string             `json:"name"`
	Port IngressServicePort `json:"port"`
}

// IngressServicePort is a port number.
type IngressServicePort struct {
	Number int `json:"number"`
}

// IngressTLS lists TLS hosts.
type IngressTLS struct {
	Hosts      []string `json:"hosts"`
	SecretName string   `json:"secretName,omitempty"`
}

// PVCSpec is a PersistentVolumeClaim spec.
type PVCSpec struct {
	AccessModes      []string  `json:"accessModes"`
	StorageClassName string    `json:"storageClassName"`
	Resources        Resources `json:"resources"`
}

// Object is one manifest. Exactly one spec field is set, matching Kind.
type Object struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   Meta   `json:"metadata"`

	JobSpec        *JobSpec          `json:"-"`
	DeploymentSpec *DeploymentSpec   `json:"-"`
	ServiceSpec    *ServiceSpec      `json:"-"`
	IngressSpec    *IngressSpec      `json:"-"`
	PVCSpec        *PVCSpec          `json:"-"`
	PodSpec        *PodSpec          `json:"-"`
	StringData     map[string]string `json:"-"`
	Data           map[string]string `json:"-"`
}

// PodSpecOf returns the pod spec inside a Job, Deployment or Pod (nil otherwise).
func (o *Object) PodSpecOf() *PodSpec {
	switch {
	case o.JobSpec != nil:
		return &o.JobSpec.Template.Spec
	case o.DeploymentSpec != nil:
		return &o.DeploymentSpec.Template.Spec
	case o.PodSpec != nil:
		return o.PodSpec
	}
	return nil
}
