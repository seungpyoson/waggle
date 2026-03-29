package cmd

import (
	"fmt"
	"strings"

	ia "github.com/seungpyoson/waggle/internal/adapter"
	"github.com/spf13/cobra"
)

var (
	adapterBootstrapTool      string
	adapterBootstrapAgent     string
	adapterBootstrapProjectID string
	adapterBootstrapSource    string
	adapterBootstrapFormat    string
)

func init() {
	adapterBootstrapCmd.Flags().StringVar(&adapterBootstrapTool, "tool", "", "Tool name (alternative to positional argument)")
	adapterBootstrapCmd.Flags().StringVar(&adapterBootstrapAgent, "agent", "", "Agent name (defaults to WAGGLE_AGENT_NAME or a tool-scoped fallback)")
	adapterBootstrapCmd.Flags().StringVar(&adapterBootstrapProjectID, "project-id", "", "Project ID (defaults to current project)")
	adapterBootstrapCmd.Flags().StringVar(&adapterBootstrapSource, "source", "", "Adapter registration source")
	adapterBootstrapCmd.Flags().StringVar(&adapterBootstrapFormat, "format", "json", "Output format: json or markdown")

	adapterCmd.AddCommand(adapterBootstrapCmd)
	rootCmd.AddCommand(adapterCmd)
}

var adapterCmd = &cobra.Command{
	Use:   "adapter",
	Short: "Run adapter-facing runtime coordination helpers",
}

var adapterBootstrapCmd = &cobra.Command{
	Use:   "bootstrap [tool]",
	Short: "Bootstrap a tool session into the machine runtime",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("accepts at most 1 arg(s), received %d", len(args))
		}
		if adapterBootstrapTool == "" && len(args) == 0 {
			return fmt.Errorf("tool required: pass a positional tool or --tool")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		tool := adapterBootstrapTool
		if tool == "" && len(args) == 1 {
			tool = args[0]
		}

		result, err := ia.Bootstrap(ia.BootstrapInput{
			Tool:      tool,
			AgentName: adapterBootstrapAgent,
			ProjectID: adapterBootstrapProjectID,
			Source:    adapterBootstrapSource,
		})
		if err != nil {
			return err
		}

		switch strings.ToLower(strings.TrimSpace(adapterBootstrapFormat)) {
		case "", "json":
			printJSON(map[string]any{
				"ok":              true,
				"tool":            result.Tool,
				"project_id":      result.ProjectID,
				"agent_name":      result.AgentName,
				"source":          result.Source,
				"runtime_running": result.RuntimeRunning,
				"runtime_error":   result.RuntimeError,
				"records":         result.Records,
			})
		case "markdown":
			fmt.Fprintln(rootCmd.OutOrStdout(), renderAdapterBootstrapMarkdown(result))
		default:
			return fmt.Errorf("invalid --format %q (supported: json, markdown)", adapterBootstrapFormat)
		}
		return nil
	},
}

func renderAdapterBootstrapMarkdown(result ia.BootstrapResult) string {
	var b strings.Builder
	b.WriteString("## Waggle Runtime\n\n")
	b.WriteString(fmt.Sprintf("- Tool: `%s`\n", result.Tool))
	b.WriteString(fmt.Sprintf("- Project: `%s`\n", result.ProjectID))
	b.WriteString(fmt.Sprintf("- Agent: `%s`\n", result.AgentName))
	b.WriteString(fmt.Sprintf("- Source: `%s`\n", result.Source))
	b.WriteString(fmt.Sprintf("- Runtime running: `%t`\n", result.RuntimeRunning))
	if result.RuntimeError != "" {
		b.WriteString(fmt.Sprintf("- Runtime note: `%s`\n", result.RuntimeError))
	}
	b.WriteString(fmt.Sprintf("- Unread: `%d`\n", len(result.Records)))

	if len(result.Records) == 0 {
		b.WriteString("\nNo unread Waggle records.\n")
		return b.String()
	}

	b.WriteString("\n### Unread Records\n")
	for _, rec := range result.Records {
		b.WriteString(fmt.Sprintf("- **%s:** %s\n", rec.FromName, rec.Body))
	}
	return b.String()
}
