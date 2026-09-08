package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "enroll claude-code", Short: "Enroll from the native SessionStart hook", Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// Hook failure is diagnostic and bounded; it cannot block host startup.
			ctx, cancel := context.WithTimeout(cmd.Context(), config.Defaults.ConnectTimeout)
			defer cancel()
			if err := enrollHook(ctx, args[0], os.Stdin); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "waggle enrollment unavailable:", err)
			}
		},
	})
}

func enrollHook(ctx context.Context, provider string, input *os.File) error {
	if provider != "claude-code" {
		return fmt.Errorf("native credential propagation for %s is not implemented", provider)
	}
	endpoint := os.Getenv(config.ClaudeMessagingSocketEnv)
	envPath := os.Getenv(config.ClaudeEnvironmentFileEnv)
	if !filepath.IsAbs(endpoint) || !filepath.IsAbs(envPath) {
		return fmt.Errorf("SessionStart must supply the native socket and environment-file paths")
	}
	// Closing this command's pipe interrupts a stalled hook-input read.
	stop := context.AfterFunc(ctx, func() { input.Close() })
	defer stop()
	var hook struct {
		SessionID string `json:"session_id"`
		Event     string `json:"hook_event_name"`
	}
	data, err := io.ReadAll(io.LimitReader(input, config.Defaults.MaxMessageSize+1))
	if err != nil {
		return fmt.Errorf("read SessionStart input: %w", err)
	}
	if int64(len(data)) > config.Defaults.MaxMessageSize {
		return fmt.Errorf("SessionStart input exceeds configured size limit")
	}
	if err = json.Unmarshal(data, &hook); err != nil {
		return fmt.Errorf("decode SessionStart input: %w", err)
	}
	if hook.Event != "SessionStart" || hook.SessionID == "" {
		return fmt.Errorf("native SessionStart identity is required")
	}
	project, err := config.ResolveProjectID(ctx)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	boundPaths := config.NewPaths(project)
	deadline, ok := ctx.Deadline()
	if !ok {
		return fmt.Errorf("enrollment requires a bounded context")
	}
	c, err := client.Connect(boundPaths.Socket, time.Until(deadline))
	if err != nil {
		return fmt.Errorf("start the broker and restart this provider session: %w", err)
	}
	defer c.Close()
	if err = c.SetDeadline(time.Until(deadline)); err != nil {
		return err
	}
	response, err := c.Send(protocol.Request{Cmd: protocol.CmdEnroll, Enrollment: &protocol.EnrollmentInput{Provider: provider, Conversation: hook.SessionID, Endpoint: endpoint, Label: hook.SessionID}})
	if err != nil {
		return err
	}
	if !response.OK {
		return fmt.Errorf("%s: %s", response.Code, response.Error)
	}
	var enrolled protocol.EnrollmentResponse
	if err = json.Unmarshal(response.Data, &enrolled); err != nil {
		return fmt.Errorf("invalid private enrollment response")
	}
	if enrolled.State != "pending" || enrolled.Credential == "" || enrolled.Incarnation == "" {
		return fmt.Errorf("inconsistent private enrollment response")
	}
	// The provider owns this path. O_NOFOLLOW prevents a leaf symlink from
	// redirecting the credential export; contents and credentials are never read.
	file, err := os.OpenFile(envPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open native environment file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("native environment file must be private, regular and owned by this user")
	}
	credential := strings.ReplaceAll(enrolled.Credential, "'", "'\\''")
	if _, err = fmt.Fprintf(file, "export %s='%s'\n", config.IncarnationCredentialEnv, credential); err != nil {
		return fmt.Errorf("write private enrollment credential: %w", err)
	}
	return file.Sync()
}
