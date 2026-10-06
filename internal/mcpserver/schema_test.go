package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func listToolsForTest(t *testing.T) []*mcp.Tool {
	t.Helper()
	cs, _ := setup(t)
	r, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return r.Tools
}

// Every tool's input schema must use one type per field: Gemini rejects
// "type": ["null", "array"] (seen through OpenCode on gemini-3.8-flash).
func TestToolSchemasAreSingleTyped(t *testing.T) {
	tools := listToolsForTest(t)
	if len(tools) != 9 {
		t.Fatalf("want 9 tools, got %d", len(tools))
	}
	for _, tl := range tools {
		b, _ := json.Marshal(tl.InputSchema)
		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch x := v.(type) {
			case map[string]any:
				if ty, ok := x["type"].([]any); ok {
					t.Errorf("%s: %s has a type union %v", tl.Name, path, ty)
				}
				if x["type"] == "array" {
					if _, ok := x["items"]; !ok {
						t.Errorf("%s: %s is an array without items", tl.Name, path)
					}
				}
				for k, c := range x {
					walk(path+"."+k, c)
				}
			case []any:
				for _, c := range x {
					walk(path+"[]", c)
				}
			}
		}
		var m any
		_ = json.Unmarshal(b, &m)
		walk("$", m)
		if strings.Contains(string(b), `"null"`) {
			t.Errorf("%s: schema still mentions null: %s", tl.Name, b)
		}
	}
}
