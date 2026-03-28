package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// CommandNotifier dispatches desktop notifications through a platform command.
type CommandNotifier struct {
	goos string
	run  func(context.Context, commandSpec) error
}

func NewCommandNotifier() *CommandNotifier {
	return &CommandNotifier{
		goos: runtime.GOOS,
		run:  runCommandSpec,
	}
}

func (n *CommandNotifier) Notify(ctx context.Context, title, body string) error {
	spec, err := commandSpecForGOOS(n.goos, title, body)
	if err != nil {
		return err
	}
	if spec.noop {
		return nil
	}
	if err := n.run(ctx, spec); err != nil {
		return fmt.Errorf("run notifier command: %w", err)
	}
	return nil
}

type commandSpec struct {
	name string
	args []string
	noop bool
}

func commandSpecForGOOS(goos, title, body string) (commandSpec, error) {
	switch goos {
	case "darwin":
		return commandSpec{
			name: "osascript",
			args: []string{
				"-e",
				fmt.Sprintf(`display notification %q with title %q`, body, title),
			},
		}, nil
	case "linux":
		return commandSpec{
			name: "notify-send",
			args: []string{title, body},
		}, nil
	default:
		return commandSpec{}, fmt.Errorf("unsupported notifier platform: %s", goos)
	}
}

func runCommandSpec(ctx context.Context, spec commandSpec) error {
	if spec.noop {
		return nil
	}
	cmd := exec.CommandContext(ctx, spec.name, spec.args...)
	return cmd.Run()
}
