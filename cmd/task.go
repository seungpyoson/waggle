package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(taskCmd)
}

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Task management commands",
}

