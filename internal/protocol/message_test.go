package protocol

import (
	"encoding/json"
	"testing"
)

// TestRequest_RoundTrip verifies P1: Request round-trips through JSON without data loss
func TestRequest_RoundTrip(t *testing.T) {
	original := Request{
		Cmd:            CmdPublish,
		Name:           "test-task",
		Topic:          "test.topic",
		Message:        "test message",
		Payload:        json.RawMessage(`{"key":"value"}`),
		Type:           "work",
		Tags:           "tag1,tag2",
		DependsOn:      "dep1,dep2",
		Priority:       5,
		Lease:          60,
		MaxRetries:     3,
		IdempotencyKey: "unique-key",
		Resource:       "resource1",
		TaskID:         "task-123",
		ClaimToken:     "token-456",
		Result:         json.RawMessage(`{"status":"done"}`),
		Reason:         "completed",
		Last:           "last-id",
		State:          "active",
		Owner:          "worker-1",
		PushListener:   true,
		PushToken:      "push-token-789",
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal back
	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Compare all fields
	if decoded.Cmd != original.Cmd {
		t.Errorf("Cmd mismatch: got %q, want %q", decoded.Cmd, original.Cmd)
	}
	if decoded.Name != original.Name {
		t.Errorf("Name mismatch: got %q, want %q", decoded.Name, original.Name)
	}
	if decoded.Topic != original.Topic {
		t.Errorf("Topic mismatch: got %q, want %q", decoded.Topic, original.Topic)
	}
	if decoded.Message != original.Message {
		t.Errorf("Message mismatch: got %q, want %q", decoded.Message, original.Message)
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Errorf("Payload mismatch: got %q, want %q", decoded.Payload, original.Payload)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type mismatch: got %q, want %q", decoded.Type, original.Type)
	}
	if decoded.Tags != original.Tags {
		t.Errorf("Tags mismatch: got %q, want %q", decoded.Tags, original.Tags)
	}
	if decoded.DependsOn != original.DependsOn {
		t.Errorf("DependsOn mismatch: got %q, want %q", decoded.DependsOn, original.DependsOn)
	}
	if decoded.Priority != original.Priority {
		t.Errorf("Priority mismatch: got %d, want %d", decoded.Priority, original.Priority)
	}
	if decoded.Lease != original.Lease {
		t.Errorf("Lease mismatch: got %d, want %d", decoded.Lease, original.Lease)
	}
	if decoded.MaxRetries != original.MaxRetries {
		t.Errorf("MaxRetries mismatch: got %d, want %d", decoded.MaxRetries, original.MaxRetries)
	}
	if decoded.IdempotencyKey != original.IdempotencyKey {
		t.Errorf("IdempotencyKey mismatch: got %q, want %q", decoded.IdempotencyKey, original.IdempotencyKey)
	}
	if decoded.Resource != original.Resource {
		t.Errorf("Resource mismatch: got %q, want %q", decoded.Resource, original.Resource)
	}
	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch: got %q, want %q", decoded.TaskID, original.TaskID)
	}
	if decoded.ClaimToken != original.ClaimToken {
		t.Errorf("ClaimToken mismatch: got %q, want %q", decoded.ClaimToken, original.ClaimToken)
	}
	if string(decoded.Result) != string(original.Result) {
		t.Errorf("Result mismatch: got %q, want %q", decoded.Result, original.Result)
	}
	if decoded.Reason != original.Reason {
		t.Errorf("Reason mismatch: got %q, want %q", decoded.Reason, original.Reason)
	}
	if decoded.Last != original.Last {
		t.Errorf("Last mismatch: got %q, want %q", decoded.Last, original.Last)
	}
	if decoded.State != original.State {
		t.Errorf("State mismatch: got %q, want %q", decoded.State, original.State)
	}
	if decoded.Owner != original.Owner {
		t.Errorf("Owner mismatch: got %q, want %q", decoded.Owner, original.Owner)
	}
	if decoded.PushListener != original.PushListener {
		t.Errorf("PushListener mismatch: got %t, want %t", decoded.PushListener, original.PushListener)
	}
	if decoded.PushToken != original.PushToken {
		t.Errorf("PushToken mismatch: got %q, want %q", decoded.PushToken, original.PushToken)
	}
}

// TestRequest_OmitsEmptyFields verifies empty fields are not present in JSON output
func TestRequest_OmitsEmptyFields(t *testing.T) {
	req := Request{
		Cmd: CmdConnect,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	// Should only have "cmd" field
	if len(m) != 1 {
		t.Errorf("Expected 1 field, got %d: %v", len(m), m)
	}
	if _, ok := m["cmd"]; !ok {
		t.Error("Missing required 'cmd' field")
	}
}

// TestResponse_OK verifies P2: OKResponse always has OK=true
func TestResponse_OK(t *testing.T) {
	testData := json.RawMessage(`{"result":"success"}`)
	resp := OKResponse(testData)

	if !resp.OK {
		t.Error("OKResponse must have OK=true")
	}
	if string(resp.Data) != string(testData) {
		t.Errorf("Data mismatch: got %q, want %q", resp.Data, testData)
	}
	if resp.Error != "" {
		t.Errorf("OKResponse should have empty Error, got %q", resp.Error)
	}
	if resp.Code != "" {
		t.Errorf("OKResponse should have empty Code, got %q", resp.Code)
	}
}

// TestResponse_Error verifies P3: ErrResponse always has OK=false and non-empty Code
func TestResponse_Error(t *testing.T) {
	code := ErrNotConnected
	message := "client not connected"
	resp := ErrResponse(code, message)

	if resp.OK {
		t.Error("ErrResponse must have OK=false")
	}
	if resp.Code == "" {
		t.Error("ErrResponse must have non-empty Code")
	}
	if resp.Code != code {
		t.Errorf("Code mismatch: got %q, want %q", resp.Code, code)
	}
	if resp.Error != message {
		t.Errorf("Error mismatch: got %q, want %q", resp.Error, message)
	}
	if resp.Data != nil {
		t.Errorf("ErrResponse should have nil Data, got %q", resp.Data)
	}
}

// TestCommandConstants_Unique verifies P4: All command constants are unique
func TestCommandConstants_Unique(t *testing.T) {
	commands := []string{
		CmdConnect, CmdDisconnect, CmdPublish, CmdSubscribe,
		CmdTaskCreate, CmdTaskList, CmdTaskClaim, CmdTaskComplete, CmdTaskFail,
		CmdTaskHeartbeat, CmdTaskCancel, CmdTaskGet, CmdTaskUpdate,
		CmdLock, CmdUnlock, CmdLocks, CmdStatus, CmdStop,
		CmdReplay,
		CmdPushReserve, CmdPushRelease,
	}

	seen := make(map[string]bool)
	for _, cmd := range commands {
		if seen[cmd] {
			t.Errorf("Duplicate command constant: %q", cmd)
		}
		seen[cmd] = true
	}

	if len(seen) != len(commands) {
		t.Errorf("Expected %d unique commands, got %d", len(commands), len(seen))
	}
}

// TestErrorCodes_Unique verifies P5: All error code constants are unique
func TestErrorCodes_Unique(t *testing.T) {
	errorCodes := []string{
		ErrBrokerNotRunning, ErrAlreadyConnected, ErrNotConnected,
		ErrResourceLocked, ErrTaskNotFound, ErrInvalidToken,
		ErrNoEligibleTask, ErrInvalidRequest, ErrDuplicateIdempotencyKey,
		ErrInternalError,
	}

	seen := make(map[string]bool)
	for _, code := range errorCodes {
		if seen[code] {
			t.Errorf("Duplicate error code constant: %q", code)
		}
		seen[code] = true
	}

	if len(seen) != len(errorCodes) {
		t.Errorf("Expected %d unique error codes, got %d", len(errorCodes), len(seen))
	}
}
