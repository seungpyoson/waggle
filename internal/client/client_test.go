package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
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
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("mock server accept: %v", err)
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		if _, err := conn.Read(buf); err != nil {
			t.Errorf("mock server read: %v", err)
			return
		}
		resp := protocol.OKResponse(json.RawMessage(`{"id":1}`))
		data, err := json.Marshal(resp)
		if err != nil {
			t.Errorf("mock server marshal: %v", err)
			return
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			t.Errorf("mock server write: %v", err)
		}
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

func TestClient_ReadStream(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("mock server accept: %v", err)
			return
		}
		defer conn.Close()
		// Send 3 events
		for i := 1; i <= 3; i++ {
			event := protocol.Event{
				Topic: "test.topic",
				Event: fmt.Sprintf("event-%d", i),
				Data:  json.RawMessage(fmt.Sprintf(`{"num":%d}`, i)),
				TS:    "2024-01-01T00:00:00Z",
			}
			data, err := json.Marshal(event)
			if err != nil {
				t.Errorf("mock server marshal event %d: %v", i, err)
				return
			}
			data = append(data, '\n')
			if _, err := conn.Write(data); err != nil {
				t.Errorf("mock server write event %d: %v", i, err)
				return
			}
		}
	}()

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	eventChan, err := c.ReadStream()
	if err != nil {
		t.Fatal(err)
	}

	// Read 3 events
	count := 0
	for event := range eventChan {
		count++
		if event.Topic != "test.topic" {
			t.Errorf("event %d: expected topic 'test.topic', got %q", count, event.Topic)
		}
		if count == 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("expected 3 events, got %d", count)
	}
}

func TestClient_LargePayload(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Create a payload larger than 64KB (default bufio.Scanner limit)
	largeData := strings.Repeat("x", 100*1024) // 100KB

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("mock server accept: %v", err)
			return
		}
		defer conn.Close()

		// Read request
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Errorf("mock server scan: %v", err)
			}
			return
		}

		// Send large response
		resp := protocol.OKResponse(json.RawMessage(fmt.Sprintf(`{"data":"%s"}`, largeData)))
		data, err := json.Marshal(resp)
		if err != nil {
			t.Errorf("mock server marshal: %v", err)
			return
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			t.Errorf("mock server write: %v", err)
		}
	}()

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Send request with large payload
	resp, err := c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(fmt.Sprintf(`{"desc":"%s"}`, largeData)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Error("expected OK response")
	}

	// Verify we got the large data back
	var result map[string]string
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result["data"]) != len(largeData) {
		t.Errorf("expected data length %d, got %d", len(largeData), len(result["data"]))
	}
}

