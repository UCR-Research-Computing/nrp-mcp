// Package diagnose explains pod states and events in plain language with a fix.
package diagnose

import (
	"fmt"
	"strings"
	"time"
)

// slowAfter is how long a pod may sit on a node without starting before we call
// the node slow.
const slowAfter = 10 * time.Minute

// Pod is the part of a Kubernetes pod status diagnosis needs.
type Pod struct {
	Name       string
	Phase      string
	Node       string
	Reason     string // pod-level reason
	Message    string
	Containers []Container
	Events     []Event
	LogTail    string
	Created    time.Time // metadata.creationTimestamp
	Scheduled  time.Time // PodScheduled condition lastTransitionTime
	Now        time.Time // injected clock (ops sets time.Now())
	// Owner is the kind of workload the pod belongs to: Job, ReplicaSet (a Deployment:
	// web or session) or "" (a bare pod). It changes the slow-node advice.
	Owner string
	// Started is when the pod started running (status.startTime), for readiness checks.
	Started time.Time
}

// Container status.
type Container struct {
	Name         string
	Ready        bool
	Restarts     int
	Waiting      string // reason
	WaitingMsg   string
	Terminated   string // reason
	ExitCode     int
	LastTermRsn  string
	LastExitCode int
	Image        string
	Init         bool // an init container
	Running      bool // state.running is set
}

// Event is a Kubernetes event on the pod.
type Event struct {
	Type    string
	Reason  string
	Message string
}

// Finding is a diagnosis.
type Finding struct {
	Pod        string          `json:"pod"`
	State      string          `json:"state"`
	Explain    string          `json:"explanation"`
	Fix        string          `json:"fix,omitempty"`
	Node       string          `json:"node,omitempty"`
	AgeMinutes int             `json:"age_minutes,omitempty"`
	Containers []ContainerInfo `json:"containers,omitempty"`
	Events     []EventInfo     `json:"events,omitempty"`
}

// ContainerInfo is one container in a Finding. State is running,
// waiting:<reason> or terminated:<reason>.
type ContainerInfo struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
	State string `json:"state,omitempty"`
}

// EventInfo is one recent pod event in a Finding.
type EventInfo struct {
	Type    string `json:"type,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// Explain diagnoses one pod and attaches node, containers and recent events.
func Explain(p Pod) Finding {
	f := explain(p)
	f.Node = p.Node
	for _, c := range p.Containers {
		ci := ContainerInfo{Name: c.Name, Image: c.Image}
		switch {
		case c.Terminated != "":
			ci.State = "terminated:" + c.Terminated
		case c.Waiting != "":
			ci.State = "waiting:" + c.Waiting
		case c.Running:
			ci.State = "running"
		}
		f.Containers = append(f.Containers, ci)
	}
	evs := p.Events
	if len(evs) > 5 {
		evs = evs[len(evs)-5:]
	}
	for _, e := range evs {
		f.Events = append(f.Events, EventInfo{Type: e.Type, Reason: e.Reason, Message: firstLine(e.Message)})
	}
	if p.Phase == "Pending" {
		f.AgeMinutes = pendingMinutes(p)
	}
	return f
}

// pendingAge is the time since the pod was scheduled (or created), never negative.
func pendingAge(p Pod) time.Duration {
	since := p.Scheduled
	if since.IsZero() || p.Node == "" {
		since = p.Created
	}
	if since.IsZero() || p.Now.IsZero() {
		return 0
	}
	if d := p.Now.Sub(since); d > 0 {
		return d
	}
	return 0
}

func pendingMinutes(p Pod) int { return int(pendingAge(p) / time.Minute) }

// pullImage is the image the pod is downloading: the newest Pulling event's image,
// else the first main container's image, else "".
func pullImage(p Pod) string {
	for i := len(p.Events) - 1; i >= 0; i-- {
		e := p.Events[i]
		if e.Reason != "Pulling" {
			continue
		}
		if a := strings.IndexByte(e.Message, '"'); a >= 0 {
			if b := strings.IndexByte(e.Message[a+1:], '"'); b > 0 {
				return e.Message[a+1 : a+1+b]
			}
		}
	}
	for _, c := range p.Containers {
		if !c.Init && c.Image != "" {
			return c.Image
		}
	}
	return ""
}

func hasPullingEvent(p Pod) bool {
	for _, e := range p.Events {
		if e.Reason == "Pulling" {
			return true
		}
	}
	return false
}

// explainPending covers a Pending pod with no known failure.
func explainPending(p Pod, f Finding) Finding {
	age := pendingAge(p)
	n := pendingMinutes(p)
	if p.Node == "" {
		f.State = "Waiting for a node"
		f.Explain = fmt.Sprintf("Not on a node yet (%d min).", n)
		if age >= slowAfter {
			f.Fix = "Wait a few minutes, or loosen the request."
		}
		return f
	}
	if age > slowAfter {
		f.State = "Slow node"
		f.Explain = fmt.Sprintf("On %s for %d min with no progress; this node may be slow or unhealthy.", p.Node, n)
		if p.Owner == "Job" {
			f.Fix = "Remove this run with nrp_cleanup run=<run id>, then plan again with avoid_node=" + p.Node + " and nrp_run (a Job is not moved in place)."
		} else {
			f.Fix = "Re-plan with avoid_node=" + p.Node + " in nrp_plan, then nrp_run: the web app or session moves in place and the old pod is replaced."
		}
		return f
	}
	// An init container that is Running is unpacking code (seconds). One that is still
	// Waiting has not started: its own image is downloading (live 2026-10-15: busybox
	// took about 2 min on a node, which "preparing" described wrongly).
	img := pullImage(p)
	for _, c := range p.Containers {
		if !c.Init || c.Terminated != "" {
			continue
		}
		if c.Running {
			f.State = "Starting"
			f.Explain = fmt.Sprintf("On %s, preparing (unpacking your code) for %d min.", p.Node, n)
			return f
		}
		if c.Waiting != "" && c.Image != "" && !hasPullingEvent(p) {
			img = c.Image
		}
		break
	}
	f.State = "Pulling image"
	if img != "" {
		img = " " + img
	}
	f.Explain = fmt.Sprintf("On %s, downloading%s for %d min. A first pull on a node can take 5-10 min.", p.Node, img, n)
	f.Fix = "Don't redeploy; that restarts the download."
	return f
}

func explain(p Pod) Finding {
	f := Finding{Pod: p.Name, State: p.Phase}
	evt := func(sub string) *Event {
		for i := len(p.Events) - 1; i >= 0; i-- {
			e := &p.Events[i]
			if strings.Contains(e.Message, sub) || strings.Contains(e.Reason, sub) {
				return e
			}
		}
		return nil
	}
	for _, c := range p.Containers {
		switch {
		case c.Terminated == "OOMKilled" || c.LastTermRsn == "OOMKilled":
			f.State = "OOMKilled"
			f.Explain = fmt.Sprintf("Container %s ran out of memory: it used its whole memory limit and was killed.", c.Name)
			f.Fix = "Raise memory by about 50% (memory=...) and run again, or process the data in smaller pieces."
			return f
		case (c.Waiting == "ImagePullBackOff" || c.Waiting == "ErrImagePull") &&
			(strings.Contains(c.WaitingMsg, "toomanyrequests") || strings.Contains(c.WaitingMsg, "429") || evt("toomanyrequests") != nil):
			f.State = "Image download limit"
			f.Explain = "Docker Hub refused the download: too many downloads from Nautilus right now (its rate limit). Nothing is wrong with your code."
			f.Fix = "Wait 15-30 minutes and Kubernetes retries by itself, or use an image from ghcr.io or quay.io instead of Docker Hub."
			return f
		case c.Waiting == "ImagePullBackOff" || c.Waiting == "ErrImagePull" || c.Waiting == "InvalidImageName":
			f.State = c.Waiting
			f.Explain = "Kubernetes cannot download the container image. " + firstLine(c.WaitingMsg)
			f.Fix = "Check the image name and tag. A private image (GitHub ghcr.io packages start private) needs pull_secret=<secret> in nrp_plan, or make the package public; nrp_build explains both."
			return f
		case c.Waiting == "CrashLoopBackOff" || (c.Terminated == "Error" || (c.Terminated != "" && c.ExitCode != 0 && c.Terminated != "Completed")):
			f.State = "Failed"
			code := c.ExitCode
			if code == 0 {
				code = c.LastExitCode
			}
			f.Explain = fmt.Sprintf("Your program in container %s exited with code %d.", c.Name, code)
			switch code {
			case 132:
				f.Explain += " Exit 132 is an illegal instruction: the binary uses CPU features this node lacks."
				f.Fix = "Rebuild without -march=native, or use a generic build."
			case 137:
				f.Explain += " Exit 137 means it was killed (time limit, preemption, or memory)."
				f.Fix = "Check activeDeadlineSeconds and memory; opportunistic pods can be preempted at any time."
			case 127:
				f.Explain += " Exit 127 means a command was not found in the image."
				f.Fix = "Install the tool in the image, or use an image that has it."
			default:
				f.Fix = "Read the last log lines below; fix the error and run again (nrp_plan with the same project)."
			}
			return f
		case c.Waiting == "CreateContainerError":
			f.State = c.Waiting
			f.Explain = "The image downloaded, but the container could not be created: " + firstLine(c.WaitingMsg)
			f.Fix = "Usually a wrong command or working folder for this image; check command= in nrp_plan, or try another image."
			return f
		case c.Waiting == "CreateContainerConfigError":
			f.State = c.Waiting
			f.Explain = "The container cannot start: " + firstLine(c.WaitingMsg)
			f.Fix = "Usually a missing Secret or ConfigMap key; create it or fix the name."
			return f
		}
	}
	if p.Reason == "Evicted" || evt("Evicted") != nil {
		msg := p.Message
		if e := evt("Evicted"); e != nil && msg == "" {
			msg = e.Message
		}
		f.State = "Evicted"
		if strings.Contains(msg, "ephemeral") || strings.Contains(msg, "ephemeral-storage") || strings.Contains(msg, "disk") {
			f.Explain = "Kubernetes removed the pod: it wrote more to its local disk than allowed. " + firstLine(msg)
			f.Fix = "Write big files to a volume (data_volume=) or S3 results, not the container's own disk, or clean up temporary files as you go."
		} else {
			f.Explain = "Kubernetes removed the pod from its node (the node ran short of memory or disk). " + firstLine(msg)
			f.Fix = "Run it again; a Job retries on another node by itself. If it keeps happening, raise memory a little."
		}
		return f
	}
	if p.Phase == "Running" && !p.Started.IsZero() && !p.Now.IsZero() && p.Now.Sub(p.Started) > 5*time.Minute && evt("Readiness probe failed") != nil {
		allReady := true
		for _, c := range p.Containers {
			if !c.Init && !c.Ready {
				allReady = false
			}
		}
		if !allReady {
			f.State = "Not ready"
			f.Explain = "The app has been running for over 5 minutes but does not answer on its port. " + firstLine(evt("Readiness probe failed").Message)
			f.Fix = "Make the app listen on 0.0.0.0 (all addresses), not 127.0.0.1 or localhost, and on the port you gave nrp_plan (port=)."
			return f
		}
	}
	if p.Phase == "Pending" {
		switch e := evt("exceeded quota"); {
		case e != nil && (strings.Contains(e.Message, "high-priority-ban") || strings.Contains(e.Message, "low-priority-ban")):
			f.Explain = "The pod uses a priorityClassName that is banned in user namespaces."
			f.Fix = "Remove priorityClassName, or use opportunistic for special GPUs."
			return f
		case e != nil:
			f.Explain = "Your namespace quota does not allow this request: " + firstLine(e.Message)
			f.Fix = "For A100/H100/H200/GH200 use nvidia.com/gpu (any standard GPU) or priorityClassName opportunistic; A100 access can be requested from the NRP."
			return f
		}
		if e := evt("denied the request"); e != nil {
			f.State = "Refused by Nautilus"
			f.Explain = "A Nautilus policy check refused the pod: " + firstLine(e.Message)
			f.Fix = "Fix what the message names (often resources or a banned setting), then plan again; nrp_plan applies the NRP rules it knows."
			return f
		}
		if p.Node == "" {
			if e := evt("FailedScheduling"); e != nil {
				msg := e.Message
				f.Explain = "No node can take the pod right now: " + firstLine(msg)
				switch {
				case strings.Contains(msg, "nvidia.com"):
					f.Fix = "No free GPU of that type fits. Ask for a standard GPU (nvidia.com/gpu), fewer GPUs, or wait; nrp_status shows what you asked for."
				case strings.Contains(msg, "Insufficient memory") || strings.Contains(msg, "Insufficient cpu"):
					f.Fix = "Lower cpu/memory, or wait for a larger node to free up."
				case strings.Contains(msg, "persistentvolumeclaim") || strings.Contains(msg, "volume node affinity"):
					f.Fix = "The volume is in another region than the free nodes; keep compute in the volume's region or use S3."
				default:
					f.Fix = "Wait a few minutes, or loosen the request."
				}
				return f
			}
		}
		if e := evt("FailedMount"); e != nil {
			f.State = "ContainerCreating"
			f.Explain = "The volume cannot be mounted on node " + p.Node + ": " + firstLine(e.Message)
			f.Fix = "Delete this pod so the Job puts it on another node (seen on Nautilus with CephFS on some nodes); nrp_cleanup can do it."
			return f
		}
		return explainPending(p, f)
	}
	switch p.Phase {
	case "Running":
		f.Explain = "Running on " + p.Node + "."
	case "Succeeded":
		f.Explain = "Finished successfully."
	case "Failed":
		f.Explain = "Failed: " + firstLine(p.Reason+" "+p.Message)
		if p.Reason == "DeadlineExceeded" {
			f.Fix = "It hit the time limit; raise hours or checkpoint and resume."
		}
	default:
		f.Explain = p.Phase
	}
	return f
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// Workload is the part of a Job or Deployment that diagnosis needs: conditions and the
// controller's own events (quota or admission refusals happen there, before any pod).
type Workload struct {
	Kind       string // Job or Deployment
	Name       string
	Conditions []Condition
	Events     []Event
}

// Condition is one status condition.
type Condition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

// ExplainWorkload diagnoses the Job or Deployment itself. ok is false when there is
// nothing to say beyond the pods.
func ExplainWorkload(w Workload) (Finding, bool) {
	f := Finding{Pod: w.Kind + "/" + w.Name}
	for _, c := range w.Conditions {
		if c.Status != "True" {
			continue
		}
		switch {
		case c.Type == "Failed" && c.Reason == "DeadlineExceeded":
			f.State, f.Explain = "Time limit", "The Job hit its time limit and was stopped: "+firstLine(c.Message)
			f.Fix = "Raise hours= in nrp_plan, or save progress (checkpoints) and resume in a new run."
			return f, true
		case c.Type == "Failed" && c.Reason == "BackoffLimitExceeded":
			f.State, f.Explain = "Failed", "The Job failed and used up its retries: "+firstLine(c.Message)
			f.Fix = "Read the logs below for the error, fix it, and plan again."
			return f, true
		case c.Type == "Failed":
			f.State, f.Explain = "Failed", "The Job failed: "+firstLine(c.Reason+" "+c.Message)
			return f, true
		case c.Type == "ReplicaFailure":
			f.State, f.Explain = "Cannot create pods", firstLine(c.Message)
			f.Fix = fixForCreate(c.Message)
			return f, true
		}
	}
	for i := len(w.Events) - 1; i >= 0; i-- {
		e := w.Events[i]
		if e.Reason != "FailedCreate" && !strings.Contains(e.Message, "denied") && !strings.Contains(e.Message, "forbidden") {
			continue
		}
		f.State = "Cannot create pods"
		f.Explain = "Nautilus would not create the pod: " + firstLine(e.Message)
		f.Fix = fixForCreate(e.Message)
		return f, true
	}
	return f, false
}

func fixForCreate(msg string) string {
	switch {
	case strings.Contains(msg, "exceeded quota"):
		return "Your namespace quota does not allow this. Ask for less (cpu, memory, GPUs), use nvidia.com/gpu or opportunistic for special GPUs, or wait until other runs finish."
	case strings.Contains(msg, "denied the request") || strings.Contains(msg, "admission webhook"):
		return "A Nautilus policy check refused it; fix what the message names and plan again."
	case strings.Contains(msg, "forbidden"):
		return "Your account is not allowed to create this in the namespace; ask the namespace admin."
	}
	return "Read the message, fix the cause and plan again."
}
