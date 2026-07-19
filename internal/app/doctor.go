package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"forge_worker/internal/config"
	"forge_worker/internal/doctor"

	"github.com/spf13/cobra"
)

func newDoctorCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	var debugOutput bool
	var envFile string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate configuration and media dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctorWithOptions(ctx, doctorOptions{JSON: jsonOutput, Debug: debugOutput, EnvFile: envFile}, stdout, stderr)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print machine-readable JSON")
	cmd.Flags().BoolVar(&debugOutput, "debug", false, "include executed dependency check commands in the report")
	cmd.Flags().StringVar(&envFile, "env-file", "", "load configuration from this env file")
	return cmd
}

type doctorOptions struct {
	JSON    bool
	Debug   bool
	EnvFile string
}

func runDoctorWithOptions(ctx context.Context, opt doctorOptions, stdout, stderr io.Writer) error {
	cfg, err := config.Load(opt.EnvFile)
	if err != nil {
		return err
	}
	report := doctor.Run(ctx, cfg, doctor.Options{Debug: opt.Debug})
	if opt.JSON {
		data, err := report.JSON()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprint(stdout, report.Human())
	}
	if !report.OK {
		return errors.New("doctor checks failed")
	}
	return nil
}
