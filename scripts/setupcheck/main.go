// Command setupcheck calls nrp_setup (check only) over a real stdio MCP session.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()
	cl := mcp.NewClient(&mcp.Implementation{Name: "setupcheck", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(os.Args[1], "serve")}, nil)
	if err != nil {
		panic(err)
	}
	defer cs.Close()
	tl, _ := cs.ListTools(ctx, nil)
	fmt.Println("tools:", len(tl.Tools))
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "nrp_setup", Arguments: map[string]any{"sign_in": true}})
	if err != nil || r.IsError {
		fmt.Println("error", err, r)
		os.Exit(1)
	}
	var m map[string]any
	b, _ := json.Marshal(r.StructuredContent)
	_ = json.Unmarshal(b, &m)
	fmt.Println("summary:", m["summary"], "ready:", m["ready"])
	for _, c := range m["checks"].([]any) {
		cm := c.(map[string]any)
		fmt.Printf("  %-8s %-10s %s\n", cm["status"], cm["id"], cm["detail"])
	}
}
