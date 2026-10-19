package client

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
)

func TestQueryReportsTransportAndProtocolFailures(t *testing.T) {
	for _, response := range []string{"", "invalid JSON\n", "{\"ok\":true}\n", "{\"ok\":true,\"data\":null}\n", "{\"ok\":false,\"code\":\"REFUSED\"}\n", "{\"ok\":true,\"data\":{\"generation\":7}}\n"} {
		t.Run(response, func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer peer.Close()
				line, err := bufio.NewReader(peer).ReadBytes('\n')
				var req protocol.Request
				if err != nil || json.Unmarshal(line, &req) != nil || req.Cmd != protocol.CmdStatus || req.Version != config.ProtocolVersion {
					t.Error("invalid status request", err)
					return
				}
				peer.Write([]byte(response))
			}()
			got, err := query(newClient(local), protocol.Request{Cmd: protocol.CmdStatus})
			<-finished
			if response == "{\"ok\":true,\"data\":{\"generation\":7}}\n" {
				if err != nil || string(got) != `{"generation":7}` {
					t.Fatal(got, err)
				}
			} else if err == nil || got != nil {
				t.Fatal("failed observation reported success", got, err)
			}
		})
	}
}
