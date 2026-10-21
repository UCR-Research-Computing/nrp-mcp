// Command e2eweb drives a real nrp-mcp over stdio against Nautilus to test web publishing,
// the Pending diagnosis and avoid_node: plan + run a web app, watch it start (printing what
// nrp_watch says), re-plan with avoid_node=<the node it landed on>, run, check that the new
// pod is on another node and the URL answers 200, then clean up in two steps.
// Run: go run ./scripts/e2eweb -bin /path/to/nrp-mcp -ns NAMESPACE -project DIR -host NAME
// Creates a PUBLIC endpoint for a few minutes: only with the namespace admin's go.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	bin := flag.String("bin", "nrp-mcp", "nrp-mcp binary")
	ns := flag.String("ns", "", "namespace")
	project := flag.String("project", "", "web project folder")
	host := flag.String("host", "", "web host name")
	port := flag.Int("port", 8080, "app port")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2eweb", Version: "0"}, nil)
	cmd := exec.Command(*bin, "serve", "--namespace", *ns)
	cmd.Env = append(os.Environ(), "NRP_CONSENT=token")
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	must(err)
	defer cs.Close()

	call := func(name string, args map[string]any) map[string]any {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		must(err)
		if r.IsError {
			var b strings.Builder
			for _, c := range r.Content {
				if t, ok := c.(*mcp.TextContent); ok {
					b.WriteString(t.Text)
				}
			}
			fail("%s error: %s", name, b.String())
		}
		var m map[string]any
		bb, _ := json.Marshal(r.StructuredContent)
		_ = json.Unmarshal(bb, &m)
		return m
	}
	planArgs := func(avoid string) map[string]any {
		a := map[string]any{"project": *project, "goal": "web", "command": "python app.py",
			"cpu": "1", "memory": "512Mi", "gpu": 0, "port": *port, "host": *host}
		if avoid != "" {
			a["avoid_node"] = avoid
			a["show_manifests"] = true
		}
		return a
	}
	deploy := func(avoid string) string {
		p := call("nrp_plan", planArgs(avoid))
		fmt.Println("PLAN:", p["summary"])
		if d, ok := p["decisions"].([]any); ok {
			for _, x := range d {
				if s, _ := x.(string); strings.Contains(s, "off") || strings.Contains(s, "avoid") {
					fmt.Println("  decision:", s)
				}
			}
		}
		if avoid != "" {
			b, _ := json.Marshal(p["manifests"])
			if !strings.Contains(string(b), "NotIn") || !strings.Contains(string(b), avoid) {
				fail("plan manifests lack the avoid_node affinity")
			}
		}
		r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"], "public_ack": p["public_url"]})
		fmt.Println("RUN:", r["summary"], "run", r["run_id"])
		return fmt.Sprint(r["run_id"])
	}
	// watch until the URL answers 200 with a Running pod; return its node.
	watchUp := func(run, notNode string) string {
		start := time.Now()
		last := ""
		for time.Since(start) < 15*time.Minute {
			w := call("nrp_watch", map[string]any{"target": run, "tail": 5})
			pods, _ := w["pods"].([]any)
			node, state := "", ""
			for _, x := range pods {
				pm, _ := x.(map[string]any)
				s, _ := pm["state"].(string)
				if s == "Terminating" {
					continue
				}
				n, _ := pm["node"].(string)
				if n != "" && n == notNode {
					continue // the old pod still going away
				}
				node, state = n, s
			}
			line := fmt.Sprint(w["summary"])
			if line != last {
				fmt.Printf("WATCH +%3.0fs: %s\n", time.Since(start).Seconds(), line)
				last = line
			}
			if state == "Running" && strings.Contains(fmt.Sprint(w["url_check"]), "HTTP 200") {
				return node
			}
			time.Sleep(20 * time.Second)
		}
		fail("not up after 15 minutes")
		return ""
	}

	fmt.Println("STATUS:", call("nrp_status", nil)["summary"])
	run := deploy("")
	node1 := watchUp(run, "")
	fmt.Println("UP #1 on", node1)
	if node1 == "" {
		fail("nrp_watch did not report a node")
	}
	run2 := deploy(node1)
	if run2 != run {
		fmt.Println("note: re-plan used a new run id", run2, "(expected in-place reuse of", run+")")
	}
	node2 := watchUp(run2, node1)
	fmt.Println("UP #2 on", node2)
	if node2 == node1 {
		fail("avoid_node did not move the pod: still on %s", node1)
	}
	fmt.Println("AVOID_NODE OK:", node1, "->", node2)

	c1 := call("nrp_cleanup", map[string]any{"run": run2})
	fmt.Println("CLEANUP 1:", c1["summary"])
	c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
	fmt.Println("CLEANUP 2:", c2["summary"])
	if run2 != run {
		c1 = call("nrp_cleanup", map[string]any{"run": run})
		if c1["confirm_token"] != nil {
			c2 = call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
			fmt.Println("CLEANUP old run:", c2["summary"])
		}
	}
	fmt.Println("STATUS:", call("nrp_status", nil)["summary"])
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+f+"\n", a...)
	os.Exit(1)
}
