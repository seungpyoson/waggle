# Task 8: Client Library — Socket Communication

**Files:**
- Create: `internal/client/client.go`
- Create: `internal/client/client_test.go`
- Depends on: Task 2 (protocol types)

Shared client used by all CLI commands: connect to socket, send request, read response.

- [ ] **Step 1: Write failing tests**

```go
// internal/client/client_test.go
package client

import (
    "encoding/json"
    "net"
    "path/filepath"
    "testing"

    "github.com/seungpyoson/waggle/internal/protocol"
)

func TestClient_SendAndReceive(t *testing.T) {
    sockPath := filepath.Join(t.TempDir(), "test.sock")

    ln, err := net.Listen("unix", sockPath)
    if err != nil {
        t.Fatal(err)
    }
    defer ln.Close()

    go func() {
        conn, _ := ln.Accept()
        defer conn.Close()
        buf := make([]byte, 4096)
        conn.Read(buf)
        resp := protocol.OKResponse(json.RawMessage(`{"id":1}`))
        data, _ := json.Marshal(resp)
        data = append(data, '\n')
        conn.Write(data)
    }()

    c, err := Connect(sockPath)
    if err != nil {
        t.Fatal(err)
    }
    defer c.Close()

    resp, err := c.Send(protocol.Request{Cmd: protocol.CmdStatus})
    if err != nil {
        t.Fatal(err)
    }
    if !resp.OK {
        t.Error("expected OK response")
    }
}

func TestClient_ConnectFail(t *testing.T) {
    _, err := Connect("/tmp/nonexistent-waggle-test.sock")
    if err == nil {
        t.Fatal("expected connection error")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/client/ -v`
Expected: FAIL — `Connect` not defined

- [ ] **Step 3: Implement client.go**

```go
// internal/client/client.go
package client

import (
    "bufio"
    "encoding/json"
    "fmt"
    "net"

    "github.com/seungpyoson/waggle/internal/protocol"
)

// Client is a connection to the waggle broker.
type Client struct {
    conn    net.Conn
    scanner *bufio.Scanner
}

// Connect establishes a connection to the broker socket.
func Connect(socketPath string) (*Client, error) {
    conn, err := net.Dial("unix", socketPath)
    if err != nil {
        return nil, fmt.Errorf("connect to broker: %w", err)
    }
    return &Client{
        conn:    conn,
        scanner: bufio.NewScanner(conn),
    }, nil
}

// Send sends a request and reads one response.
func (c *Client) Send(req protocol.Request) (*protocol.Response, error) {
    data, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }
    data = append(data, '\n')

    if _, err := c.conn.Write(data); err != nil {
        return nil, fmt.Errorf("write request: %w", err)
    }

    if !c.scanner.Scan() {
        if err := c.scanner.Err(); err != nil {
            return nil, fmt.Errorf("read response: %w", err)
        }
        return nil, fmt.Errorf("broker closed connection")
    }

    var resp protocol.Response
    if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
        return nil, fmt.Errorf("parse response: %w", err)
    }
    return &resp, nil
}

// ReadStream reads the next streamed event (for subscribe connections).
func (c *Client) ReadStream() ([]byte, error) {
    if !c.scanner.Scan() {
        if err := c.scanner.Err(); err != nil {
            return nil, err
        }
        return nil, fmt.Errorf("broker closed connection")
    }
    return c.scanner.Bytes(), nil
}

// Close closes the connection.
func (c *Client) Close() error {
    return c.conn.Close()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/client/ -v`
Expected: PASS (2/2)

- [ ] **Step 5: Commit**

```bash
git add internal/client/
python3 ~/.claude/lib/safe_git.py commit -m "feat: client library — socket communication"
```
