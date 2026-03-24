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

func TestClient_ReadStream(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		// Send 3 events
		for i := 1; i <= 3; i++ {
			event := protocol.Event{
				Topic: "test.topic",
				Event: fmt.Sprintf("event-%d", i),
				Data:  json.RawMessage(fmt.Sprintf(`{"num":%d}`, i)),
				TS:    "2024-01-01T00:00:00Z",
			}
			data, _ := json.Marshal(event)
			data = append(data, '\n')
			conn.Write(data)
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
		conn, _ := ln.Accept()
		defer conn.Close()
		
		// Read request
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
		scanner.Scan()
		
		// Send large response
		resp := protocol.OKResponse(json.RawMessage(fmt.Sprintf(`{"data":"%s"}`, largeData)))
		data, _ := json.Marshal(resp)
		data = append(data, '\n')
		conn.Write(data)
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

