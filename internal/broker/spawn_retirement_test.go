package broker

import (
	"encoding/json"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func TestRemovedSpawnCommandsRejectBeforeAccessingBroker(t *testing.T) {
	for _, command := range []string{"spawn.register", "spawn.update-pid"} {
		t.Run(command, func(t *testing.T) {
			data, err := json.Marshal(protocol.Request{
				Version: config.ProtocolVersion, Cmd: command, Name: "worker",
				Payload: json.RawMessage(`{"pid":0,"type":"claude"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			request, err := protocol.DecodeRequest(data)
			if err != nil {
				t.Fatal(err)
			}
			// A removed command must reject before touching broker state or an
			// operation capability. Neither is provided to this dispatch test.
			response := route(&call{Session: &Session{name: "cli"}}, request)
			if response.OK || response.Code != protocol.ErrInvalidRequest {
				t.Fatalf("removed command was accepted: %+v", response)
			}
		})
	}
}
