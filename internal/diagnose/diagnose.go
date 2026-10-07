// Package diagnose explains pod states and events in plain language with a fix.
package diagnose

import (
	"fmt"
	"strings"
)

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
}

// Event is a Kubernetes event on the pod.
type Event struct {
	Type    string
	Reason  string
	Message string
}

// Finding is a diagnosis.
type Finding struct {
	Pod     string `json:"pod"`
	State   string `json:"state"`
	Explain string `json:"explanation"`
	Fix     string `json:"fix,omitempty"`
}

// Explain diagnoses one pod.
func Explain(p Pod) Finding {
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
		case c.Waiting == "CreateContainerConfigError":
			f.State = c.Waiting
			f.Explain = "The container cannot start: " + firstLine(c.WaitingMsg)
			f.Fix = "Usually a missing Secret or ConfigMap key; create it or fix the name."
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
		if e := evt("FailedMount"); e != nil {
			f.State = "ContainerCreating"
			f.Explain = "The volume cannot be mounted on node " + p.Node + ": " + firstLine(e.Message)
			f.Fix = "Delete this pod so the Job puts it on another node (seen on Nautilus with CephFS on some nodes); nrp_cleanup can do it."
			return f
		}
		f.Explain = "Waiting to be scheduled or for the image to download (large images can take a few minutes the first time on a node)."
		return f
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
