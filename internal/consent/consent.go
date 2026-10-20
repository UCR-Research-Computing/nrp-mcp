// Package consent makes approval codes that only the person at the computer sees.
//
// The AI assistant never receives a code. nrp-mcp writes the code to a file in the
// user's state folder (approve.txt), prints it on the server's stderr log, and tries a
// desktop notification. The person reads it there and types it into the chat; the
// assistant passes it back as approval_code. A code is single use, expires after 10
// minutes, and is bound to one plan hash, so it approves exactly what was shown.
package consent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/filelock"
)

// TTL is how long an approval code is valid.
const TTL = 10 * time.Minute

// alphabet has no look-alike characters (no 0/O, 1/I/L).
const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// Codes issues and checks approval codes. Records live in <Dir>/approvals.json and hold
// only sha256 hashes of the codes.
type Codes struct {
	Dir    string
	Now    func() time.Time
	Notify func(title, body string) // nil: DesktopNotify
	Log    func(line string)        // nil: write to os.Stderr
	mu     sync.Mutex
}

type rec struct {
	Hash     string    `json:"code_sha256"`
	PlanID   string    `json:"plan_id"`
	PlanHash string    `json:"plan_hash"`
	Action   string    `json:"action"`
	Expires  time.Time `json:"expires"`
	Used     bool      `json:"used"`
}

// Errors from Check.
var (
	ErrWrong   = errors.New("that approval code is not right for this plan. Ask the person to read the newest code (desktop notification, or run `nrp-mcp approve` in a terminal) and type it exactly")
	ErrUsed    = errors.New("that approval code was already used; each code works once. Call the same tool again without approval_code to get a new one")
	ErrExpired = errors.New("that approval code expired (codes last 10 minutes). Call the same tool again without approval_code to get a new one")
)

// New makes a Codes rooted at dir (created 0700).
func New(dir string) (*Codes, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Codes{Dir: dir, Now: time.Now}, nil
}

// File is the path of the file that shows the newest code to the person.
func (c *Codes) File() string { return filepath.Join(c.Dir, "approve.txt") }

func (c *Codes) path() string { return filepath.Join(c.Dir, "approvals.json") }

// Normalize upper-cases a typed code and drops spaces and dashes ("abc-23x" -> "ABC23X").
func Normalize(code string) string {
	r := strings.NewReplacer(" ", "", "-", "", "\t", "")
	return strings.ToUpper(r.Replace(strings.TrimSpace(code)))
}

func hashCode(code string) string {
	h := sha256.Sum256([]byte(Normalize(code)))
	return hex.EncodeToString(h[:])
}

func newCode() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 6)
	for i, x := range b {
		out[i] = alphabet[int(x)%len(alphabet)]
	}
	return string(out), nil
}

func (c *Codes) load() []rec {
	var r []rec
	if b, err := os.ReadFile(c.path()); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	return r
}

// save writes atomically (temp file + rename) and drops records a day past expiry.
func (c *Codes) save(r []rec) error {
	now := c.Now()
	var keep []rec
	for _, x := range r {
		if now.Before(x.Expires.Add(24 * time.Hour)) {
			keep = append(keep, x)
		}
	}
	b, err := json.MarshalIndent(keep, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path())
}

// Issue makes a code for (plan, action), shows it to the person, and returns it.
// Callers must NOT put the returned code in any tool result; only tests read it.
// summary is one short line saying what the code approves.
func (c *Codes) Issue(planID, planHash, action, summary string) (string, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := filelock.Acquire(filepath.Join(c.Dir, "approvals.lock"))
	if err != nil {
		return "", time.Time{}, err
	}
	defer l.Unlock()
	code, err := newCode()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := c.Now().Add(TTL)
	r := append(c.load(), rec{Hash: hashCode(code), PlanID: planID, PlanHash: planHash, Action: action, Expires: exp})
	if err := c.save(r); err != nil {
		return "", time.Time{}, err
	}
	shown := code[:3] + "-" + code[3:]
	note := fmt.Sprintf("nrp-mcp approval code: %s\n\nIt approves: %s\nPlan %s, valid until %s (10 minutes, one use).\nIf you agree, type this code into the chat with your AI assistant. If you do not agree, ignore it.\n",
		shown, summary, planID, exp.Local().Format("15:04"))
	if err := os.WriteFile(c.File(), []byte(note), 0o600); err != nil {
		return "", time.Time{}, err
	}
	logf := c.Log
	if logf == nil {
		logf = func(s string) { fmt.Fprintln(os.Stderr, s) }
	}
	logf("nrp-mcp: approval code " + shown + " for " + planID + " (" + action + "): " + summary)
	notify := c.Notify
	if notify == nil {
		notify = DesktopNotify
	}
	notify("nrp-mcp approval code "+shown, summary)
	return code, exp, nil
}

// Check redeems a code typed by the person for (plan, action). It fails if the code is
// wrong, used, expired, or was issued for another plan, plan hash or action.
func (c *Codes) Check(code, planID, planHash, action string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := filelock.Acquire(filepath.Join(c.Dir, "approvals.lock"))
	if err != nil {
		return err
	}
	defer l.Unlock()
	if Normalize(code) == "" {
		return ErrWrong
	}
	h := hashCode(code)
	r := c.load()
	for i := range r {
		x := &r[i]
		if x.Hash != h {
			continue
		}
		if x.PlanID != planID || x.Action != action || x.PlanHash != planHash {
			return ErrWrong
		}
		if x.Used {
			return ErrUsed
		}
		if c.Now().After(x.Expires) {
			return ErrExpired
		}
		x.Used = true
		return c.save(r)
	}
	return ErrWrong
}

// Latest returns the text of approve.txt (for `nrp-mcp approve`), or "" if none.
func (c *Codes) Latest() string {
	b, err := os.ReadFile(c.File())
	if err != nil {
		return ""
	}
	return string(b)
}

// DesktopNotify tries a desktop notification and ignores every error: approve.txt and
// the stderr line are the reliable paths.
func DesktopNotify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", body, title))
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			return
		}
		cmd = exec.Command("notify-send", title, body)
	case "windows":
		ps := "[void][System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');" +
			"$n=New-Object System.Windows.Forms.NotifyIcon;$n.Icon=[System.Drawing.SystemIcons]::Information;" +
			"$n.Visible=$true;$n.ShowBalloonTip(15000,'" + psQuote(title) + "','" + psQuote(body) + "','Info');Start-Sleep -Seconds 16"
		cmd = exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", ps)
	default:
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }
