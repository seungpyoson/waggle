package native

import (
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"testing"
)

func TestEnrollmentCannotChooseUnregisteredProviderOrEndpoint(t *testing.T) {
	cfg := config.ProviderConfig{ClientVersion: "fixture", CodexEndpoint: "/registered/server.sock", Transport: config.NewNativeConfig()}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []messages.Enrollment{
		{Provider: "claude-code", Conversation: "session", Endpoint: cfg.CodexEndpoint},
		{Provider: "codex", Conversation: "thread", Endpoint: "/another/server.sock"},
		{Provider: "codex", Endpoint: cfg.CodexEndpoint},
	} {
		if err := c.Check(e); err == nil {
			t.Fatal("unsupported binding accepted")
		}
	}
	e := messages.Enrollment{Provider: "codex", Conversation: "thread", Endpoint: cfg.CodexEndpoint}
	if err := c.Check(e); err != nil {
		t.Fatal(err)
	}
	cfg.CodexEndpoint = ""
	c, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(e); err == nil {
		t.Fatal("enrollment supplied the missing broker prerequisite")
	}
}
