package fastlane

// The python half of the fast lane (backlog 41c): a long-lived kernel
// process from the project venv, speaking the same JSON-lines-over-stdio
// protocol the catalog helper proved. The kernel holds the notebook's python
// namespace; the Session decides WHAT runs and WHEN (the declared DAG and
// the hash chain live on the Go side), the kernel only executes.

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed kernel.py
var kernelScript []byte

// pythonRunTimeout bounds one cell execution. Python cells do real work
// (model fits, plots), so this is looser than a SQL statement's budget.
const pythonRunTimeout = 5 * time.Minute

// exportRowCap bounds how many rows of an upstream SQL result are handed to
// python. A cap with a truthful marker beats an unbounded export that stalls
// the notebook; the verified lane remains the way to process full tables
// in-warehouse.
const exportRowCap = 100_000

type kernel struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Scanner
	cancel context.CancelFunc
}

type kernelReply struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Stdout  string `json:"stdout"`
	Tabular bool   `json:"tabular"`
	Pandas  bool   `json:"pandas"`
	Preview *struct {
		Columns   []string `json:"columns"`
		Rows      [][]any  `json:"rows"`
		Truncated bool     `json:"truncated"`
	} `json:"preview"`
}

// startKernel spawns the venv python on the embedded script, materialized
// under dir (the session's scratch space).
func startKernel(ctx context.Context, venvPython, dir string) (*kernel, error) {
	scriptPath := filepath.Join(dir, "kernel.py")
	if err := os.WriteFile(scriptPath, kernelScript, 0o600); err != nil {
		return nil, err
	}
	kctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(kctx, venvPython, scriptPath) // #nosec G204 -- both paths derive from the data dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = nil // cell errors arrive in the reply; kernel noise is not a channel
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // wide dataframes happen
	return &kernel{cmd: cmd, stdin: stdin, out: sc, cancel: cancel}, nil
}

func (k *kernel) stop() {
	k.cancel()
	_ = k.cmd.Wait()
}

// roundTrip sends one request and reads one reply, killing the kernel if the
// deadline passes — a hung cell must not own the notebook.
func (k *kernel) roundTrip(req any, timeout time.Duration) (*kernelReply, error) {
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	timer := time.AfterFunc(timeout, func() { k.cancel() })
	defer timer.Stop()

	if _, err := k.stdin.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("python kernel is gone: %v", err)
	}
	if !k.out.Scan() {
		return nil, fmt.Errorf("python kernel closed its stream (%v) — it will restart on the next run", k.out.Err())
	}
	var reply kernelReply
	if err := json.Unmarshal(k.out.Bytes(), &reply); err != nil {
		return nil, fmt.Errorf("python kernel spoke out of protocol: %v", err)
	}
	return &reply, nil
}

// exportPath is where one scope's cell exports for the kernel: a directory
// per scope tag under the session scratch, so two documents' orders_base.csv
// never overwrite each other.
func (s *Session) exportPath(scope, cellID string) string {
	return filepath.Join(s.scratch, scopeTag(scope), cellID+".csv")
}

// exportTable writes one volatile table's rows as CSV for the kernel,
// capped at exportRowCap. Returns the file path.
func (s *Session) exportTable(ctx context.Context, scope, cellID string) (string, error) {
	path := s.exportPath(scope, cellID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	q := fmt.Sprintf("SELECT TOP %d * FROM %s", exportRowCap, volatileName(scope, cellID))
	rows, err := s.db.QueryContext(qctx, q)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	f, err := os.Create(path) // #nosec G304 -- session scratch dir + validated cell id
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if err := w.Write(cols); err != nil {
		return "", err
	}
	record := make([]string, len(cols))
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		for i, v := range vals {
			switch t := v.(type) {
			case nil:
				record[i] = ""
			case []byte:
				record[i] = string(t)
			default:
				record[i] = fmt.Sprintf("%v", t)
			}
		}
		if err := w.Write(record); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	w.Flush()
	return path, w.Error()
}
