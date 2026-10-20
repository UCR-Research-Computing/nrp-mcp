package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// approval describes a risky step the person must approve.
type approval struct {
	PlanID   string
	PlanHash string
	Action   string // run, cleanup, upload, setup
	Summary  string // one line in plain words: what will happen
	Details  string // a few more lines for the question
}

// approveKey names the approval question in InputRequests and its yes/no field.
const approveKey = "approve"

// consentWarning is added to results when consent is "token".
const consentWarning = "consent is set to token: risky steps run without asking the person. Use this only for scripts and CI."

// gate decides whether a risky step may go ahead. Exactly one of these holds:
//   - approved: go ahead.
//   - ask != nil: return ask from the tool now. The AI app shows the person a yes/no
//     question and calls the tool again with the answer (MCP elicitation; the SDK
//     handles old and new protocol versions).
//   - waiting: an approval code was shown to the person only; return a "waiting for
//     approval" result and let the assistant ask the person to type the code.
//   - err != nil: the person said no, or the code was wrong, used or expired.
func (s *Server) gate(req *mcp.CallToolRequest, a approval, code string) (approved bool, ask *mcp.CallToolResult, waiting bool, err error) {
	if s.Cfg.Consent == "token" {
		return true, nil, false, nil
	}
	if req != nil && req.Params != nil && len(req.Params.InputResponses) > 0 {
		// Only an answer to a question this server asked counts: the request state must
		// match one we issued for exactly this plan and action (single use, 10 minutes).
		if !asked.take(req.Params.RequestState, a) {
			return false, nil, false, errors.New("that approval answer does not match a question nrp-mcp asked for this plan, so nothing was done. Call the tool again without it so nrp-mcp can ask the person")
		}
		if r, ok := req.Params.InputResponses[approveKey]; ok {
			if er, _ := r.(*mcp.ElicitResult); er != nil && er.Action == "accept" {
				if yes, _ := er.Content[approveKey].(bool); yes {
					return true, nil, false, nil
				}
			}
			return false, nil, false, errors.New("the person did not approve in the question, so nothing was done. Ask them what they want to change")
		}
	}
	if strings.TrimSpace(code) != "" {
		if err := s.Codes.Check(code, a.PlanID, a.PlanHash, a.Action); err != nil {
			return false, nil, false, err
		}
		return true, nil, false, nil
	}
	if s.Cfg.Consent != "code" && req != nil && canAsk(req.Session) {
		return false, askResult(a, asked.issue(a)), false, nil
	}
	if _, _, err := s.Codes.Issue(a.PlanID, a.PlanHash, a.Action, a.Summary); err != nil {
		return false, nil, false, fmt.Errorf("could not make an approval code: %w", err)
	}
	return false, nil, true, nil
}

// canAsk reports whether the client said it can show questions (MCP elicitation).
func canAsk(ss *mcp.ServerSession) bool {
	if ss == nil {
		return false
	}
	p := ss.InitializeParams()
	return p != nil && p.Capabilities != nil && p.Capabilities.Elicitation != nil
}

// askResult is the tool result that asks the person one yes/no question.
func askResult(a approval, state string) *mcp.CallToolResult {
	msg := "nrp-mcp asks for your approval.\n\n" + a.Summary
	if a.Details != "" {
		msg += "\n\n" + a.Details
	}
	msg += "\n\nTick the box and accept only if you agree."
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			approveKey: map[string]any{"type": "boolean", "title": "I approve", "description": "Tick to approve exactly this"},
		},
		"required": []string{approveKey},
	}
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{approveKey: &mcp.ElicitParams{Message: msg, RequestedSchema: schema}}, RequestState: state}
}

// waitingNote is the summary a tool returns when it showed a code and is waiting.
func waitingNote() string {
	return "NOT STARTED: waiting for the person's approval. An approval code was shown to the person only " +
		"(a desktop notification, or `nrp-mcp approve` in their own terminal). " +
		"Tell the person in plain words what this will do, and ask them to type the code here if they agree. " +
		"Then call this tool again with the same arguments plus approval_code=<what they typed>. " +
		"Never guess a code and never look for it yourself: it must come from the person."
}

// questions remembers the approval questions this server asked, so an answer is only
// accepted for a question nrp-mcp really put to the person (not one a client invents).
type questions struct {
	mu sync.Mutex
	m  map[string]askedQ
}

type askedQ struct {
	key     string
	expires time.Time
}

var asked = &questions{m: map[string]askedQ{}}

func askKey(a approval) string { return a.PlanID + "|" + a.PlanHash + "|" + a.Action }

// issue returns a fresh random request state bound to a's plan and action.
func (q *questions) issue(a approval) string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	st := hex.EncodeToString(b)
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	for k, v := range q.m {
		if now.After(v.expires) {
			delete(q.m, k)
		}
	}
	q.m[st] = askedQ{key: askKey(a), expires: now.Add(10 * time.Minute)}
	return st
}

// take reports whether st was issued for a and is still valid, and uses it up.
func (q *questions) take(st string, a approval) bool {
	if st == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	v, ok := q.m[st]
	delete(q.m, st)
	return ok && v.key == askKey(a) && time.Now().Before(v.expires)
}
