// Command e2earray checks Slurm array translation live on Nautilus: it plans a folder that
// holds a Slurm script with --array, runs it as an Indexed Job (CPU only), and checks that
// every task printed a different SLURM_ARRAY_TASK_ID and a different result. Then cleans up.
// Run: go run ./scripts/e2earray -bin /path/to/nrp-mcp -ns NAMESPACE -dir examples/slurm-array-bootstrap
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	bin := flag.String("bin", "nrp-mcp", "nrp-mcp binary")
	ns := flag.String("ns", "", "namespace")
	dir := flag.String("dir", "examples/slurm-array-bootstrap", "project with a Slurm --array script")
	want := flag.Int("tasks", 10, "expected number of array tasks")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2earray", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin, "serve", "--namespace", *ns)}, nil)
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

	p := call("nrp_plan", map[string]any{"project": *dir, "name": "array-check"})
	fmt.Println("PLAN:", p["summary"])
	for _, m := range p["slurm_mapping"].([]any) {
		mm := m.(map[string]any)
		fmt.Printf("  %-30s -> %v\n", mm["slurm"], mm["becomes"])
	}
	if p["runnable"] != true {
		fail("not runnable: %v", p["refusals"])
	}
	r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"]})
	run := r["run_id"].(string)
	fmt.Println("RUN:", run)

	done := fmt.Sprintf("%d Succeeded", *want)
	var w map[string]any
	for i := 0; i < 80; i++ {
		time.Sleep(10 * time.Second)
		w = call("nrp_watch", map[string]any{"target": run, "tail": 5})
		pr := fmt.Sprint(w["progress"])
		fmt.Printf("WATCH %d: %v\n", i, pr)
		if strings.Contains(pr, done) || strings.Contains(pr, "Failed") {
			break
		}
	}
	ok := strings.Contains(fmt.Sprint(w["progress"]), done)

	// Read every pod's log with kubectl (nrp_watch tails one pod).
	out, err := exec.Command("kubectl", "--context", "nautilus", "-n", *ns, "logs", "-l", "nrp-mcp/run="+run, "--prefix", "--tail", "5", "--max-log-requests", "20").CombinedOutput()
	if err != nil {
		fmt.Println("logs:", err)
	}
	re := regexp.MustCompile(`task (\d+) mean (\S+) sd (\S+)`)
	ids, means := map[string]bool{}, map[string]bool{}
	var lines []string
	for _, m := range re.FindAllStringSubmatch(string(out), -1) {
		ids[m[1]], means[m[2]] = true, true
		lines = append(lines, m[0])
	}
	sort.Strings(lines)
	fmt.Println("RESULTS:\n  " + strings.Join(lines, "\n  "))

	c1 := call("nrp_cleanup", map[string]any{"run": run})
	c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
	fmt.Println("CLEANUP:", c2["summary"])

	if !ok {
		fail("did not finish %d tasks: %v", *want, w["progress"])
	}
	if len(ids) != *want || len(means) != *want {
		fail("want %d distinct task ids and results, got %d ids and %d results", *want, len(ids), len(means))
	}
	fmt.Printf("ARRAY OK: %d tasks, %d distinct ids, %d distinct results\n", *want, len(ids), len(means))
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "E2E FAIL: "+f+"\n", a...)
	os.Exit(1)
}
