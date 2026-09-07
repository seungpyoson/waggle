package protocol

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

func TestNativeDecoderRejectsPreviousVersionBeforeCommand(t *testing.T) {
	for _, command := range []string{"status", "connect", "replay", "ack", "push.reserve"} {
		data, err := json.Marshal(map[string]any{"cmd": command, "name": "legacy", "push_token": "old"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRequest(data); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("%s did not reject missing version: %v", command, err)
		}
	}
}

func TestNativeDecoderRejectsRemovedFieldsAndMultipleFrames(t *testing.T) {
	data, err := json.Marshal(map[string]any{"version": config.ProtocolVersion, "cmd": "connect", "push_listener": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRequest(data); err == nil {
		t.Fatal("removed routing field silently accepted")
	}
	data, err = json.Marshal(Request{Version: config.ProtocolVersion, Cmd: CmdStatus})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRequest(append(append([]byte{}, data...), data...)); err == nil {
		t.Fatal("multiple values accepted in one frame")
	}
	if _, err := DecodeRequest(data); err != nil {
		t.Fatal(err)
	}
}
