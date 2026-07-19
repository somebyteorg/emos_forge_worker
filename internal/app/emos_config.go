package app

import (
	"fmt"
	"strings"

	"forge_worker/internal/config"

	"github.com/spf13/cobra"
)

type emosFlagOptions struct {
	URL      string
	Token    string
	WorkerID string
}

func addEMOSFlags(cmd *cobra.Command, opt *emosFlagOptions) {
	cmd.Flags().StringVar(&opt.URL, "emos-url", "", "EMOS backend URL; overrides EMOS_URL")
	cmd.Flags().StringVar(&opt.Token, "emos-token", "", "EMOS bearer token; overrides EMOS_TOKEN")
	cmd.Flags().StringVar(&opt.WorkerID, "emos-worker-id", "", "EMOS forge worker id; overrides EMOS_FORGE_WORKER_ID")
}

func applyEMOSFlagOptions(cfg *config.Config, opt emosFlagOptions) {
	if value := strings.TrimSpace(opt.URL); value != "" {
		cfg.EMOSURL = value
	}
	if value := strings.TrimSpace(opt.Token); value != "" {
		cfg.EMOSToken = value
	}
	if value := strings.TrimSpace(opt.WorkerID); value != "" {
		cfg.EMOSForgeWorkerID = value
	}
}

func validateEMOSConfig(cfg config.Config, mode string, requireWorkerID bool) error {
	var missing []string
	if strings.TrimSpace(cfg.EMOSURL) == "" {
		missing = append(missing, "EMOS_URL")
	}
	if strings.TrimSpace(cfg.EMOSToken) == "" {
		missing = append(missing, "EMOS_TOKEN")
	}
	if requireWorkerID && strings.TrimSpace(cfg.EMOSForgeWorkerID) == "" {
		missing = append(missing, "EMOS_FORGE_WORKER_ID")
	}
	if len(missing) == 0 {
		return nil
	}
	hint := "--emos-url and --emos-token"
	if requireWorkerID {
		hint += ", and --emos-worker-id"
	}
	return fmt.Errorf("%s requires EMOS config; set env values or pass %s (missing: %s)", mode, hint, strings.Join(missing, ", "))
}
