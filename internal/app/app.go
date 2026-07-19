package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type buildInfo struct {
	Version   string
	BuildTime string
}

func Run(ctx context.Context, args []string, version, buildTime string, stdout, stderr io.Writer) error {
	info := resolveBuildInfo(version, buildTime)
	root := newRootCommand(ctx, info, stdout, stderr)
	root.SetArgs(args)
	return root.Execute()
}

func newRootCommand(ctx context.Context, info buildInfo, stdout, stderr io.Writer) *cobra.Command {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:           "forge-worker",
		Short:         "Media worker for local and EMOS forge processing",
		Version:       formatVersion(info),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newDoctorCommand(ctx, stdout, stderr))
	root.AddCommand(newLocalCommand(ctx, stdout, stderr))
	root.AddCommand(newUploadCommand(ctx, stdout, stderr))
	root.AddCommand(newWorkerCommand(ctx, info.Version, stdout, stderr))
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(stdout, formatVersion(info))
		},
	})
	return root
}

func formatVersion(info buildInfo) string {
	return fmt.Sprintf("version: %s\nbuild_time: %s", info.Version, info.BuildTime)
}

func resolveBuildInfo(version, buildTime string) buildInfo {
	info := buildInfo{
		Version:   strings.TrimSpace(version),
		BuildTime: strings.TrimSpace(buildTime),
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.BuildTime == "" {
		info.BuildTime = executableBuildTime()
	}
	if info.BuildTime == "" {
		info.BuildTime = "unknown"
	}
	return info
}

func executableBuildTime() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	stat, err := os.Stat(executable)
	if err != nil {
		return ""
	}
	return stat.ModTime().UTC().Format(time.RFC3339)
}
