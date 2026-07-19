package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"forge_worker/internal/config"
	"forge_worker/internal/emos"

	"github.com/spf13/cobra"
)

func newUploadCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	var envFile string
	var root string
	var jobUUID string
	var deleteArtifacts bool
	var emosOpt emosFlagOptions
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload an existing completed forge output directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUploadWithOptions(ctx, uploadOptions{
				EnvFile: envFile, Root: root, JobUUID: jobUUID, DeleteArtifacts: deleteArtifacts,
				DeleteArtifactsSet: cmd.Flags().Changed("delete-artifacts"), EMOS: emosOpt,
			}, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&envFile, "env-file", "", "load configuration from this env file")
	cmd.Flags().StringVar(&root, "root", "", "task output directory containing manifest.json")
	cmd.Flags().StringVar(&jobUUID, "job-uuid", "", "EMOS forge job UUID")
	cmd.Flags().BoolVar(&deleteArtifacts, "delete-artifacts", false, "delete uploaded task files after a successful manual upload")
	addEMOSFlags(cmd, &emosOpt)
	return cmd
}

type uploadOptions struct {
	EnvFile            string
	Root               string
	JobUUID            string
	DeleteArtifacts    bool
	DeleteArtifactsSet bool
	EMOS               emosFlagOptions
}

func runUploadWithOptions(ctx context.Context, opt uploadOptions, stdout, stderr io.Writer) error {
	cfg, err := config.Load(opt.EnvFile)
	if err != nil {
		return err
	}
	applyEMOSFlagOptions(&cfg, opt.EMOS)
	if err := validateEMOSConfig(cfg, "upload", false); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.EnsureRuntimeDirs(); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	interactive := strings.TrimSpace(opt.JobUUID) == "" || strings.TrimSpace(opt.Root) == ""
	jobUUID := strings.TrimSpace(opt.JobUUID)
	if jobUUID == "" {
		jobUUID, err = promptUploadJobUUID(reader, stdout)
		if err != nil {
			return err
		}
	}
	root := strings.TrimSpace(opt.Root)
	if root == "" {
		root, err = promptUploadRoot(reader, cfg.OutputDir, stdout)
		if err != nil {
			return err
		}
	}
	cfg.UploadDeleteArtifacts = resolveUploadDeleteArtifacts(opt, interactive, reader, stdout)
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve upload root: %w", err)
	}
	if err := validateManualUploadManifest(root); err != nil {
		return err
	}
	logOut := newTimestampWriter(stdout)
	client, err := emos.New(cfg.EMOSURL, cfg.EMOSToken, cfg.EMOSForgeWorkerID, cfg.HTTPTimeout)
	if err != nil {
		return err
	}
	if err := uploadClaimedEMOSJob(ctx, cfg, client, jobUUID, root, logOut); err != nil {
		return err
	}
	fmt.Fprintf(logOut, "upload completed for job %s\n", jobUUID)
	return nil
}

func uploadClaimedEMOSJob(ctx context.Context, cfg config.Config, client *emos.Client, jobUUID, root string, output io.Writer) error {
	claim, err := client.Claim(ctx, jobUUID)
	if err != nil {
		return err
	}
	if claim.IsSuccess {
		fmt.Fprintf(output, "job %s claim accepted first_claim=true\n", jobUUID)
		stepTimes := loadCompletedStepTimes(root)
		return uploadCompletedEMOSJob(ctx, cfg, client, jobUUID, root, stepTimes, output)
	}
	return fmt.Errorf("job %s claim rejected: first_claim=false", jobUUID)
}

func resolveUploadDeleteArtifacts(opt uploadOptions, interactive bool, input io.Reader, stdout io.Writer) bool {
	if interactive && !opt.DeleteArtifactsSet {
		return promptUploadDeleteArtifacts(input, stdout)
	}
	return opt.DeleteArtifacts
}

func promptUploadJobUUID(input io.Reader, stdout io.Writer) (string, error) {
	value, err := promptLine(input, stdout, "forge job uuid: ")
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("forge job uuid is required")
	}
	return value, nil
}

func promptUploadRoot(input io.Reader, outputDir string, stdout io.Writer) (string, error) {
	candidates, err := uploadRootCandidates(outputDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(candidates) == 0 {
		return promptRequiredLine(input, stdout, "output directory: ", "output directory is required")
	}
	for i, candidate := range candidates {
		fmt.Fprintf(stdout, "%d. %s\n", i+1, candidate)
	}
	line, err := promptLine(input, stdout, "select output directory or enter path: ")
	if err != nil {
		return "", err
	}
	if line == "" {
		return "", fmt.Errorf("output directory is required")
	}
	index, err := strconv.Atoi(line)
	if err == nil {
		if index < 1 || index > len(candidates) {
			return "", fmt.Errorf("invalid output directory selection")
		}
		return candidates[index-1], nil
	}
	return line, nil
}

func promptUploadDeleteArtifacts(input io.Reader, stdout io.Writer) bool {
	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	return promptBool(reader, stdout, "delete uploaded files", false)
}

func promptRequiredLine(input io.Reader, stdout io.Writer, label, emptyError string) (string, error) {
	value, err := promptLine(input, stdout, label)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s", emptyError)
	}
	return value, nil
}

func promptLine(input io.Reader, stdout io.Writer, label string) (string, error) {
	fmt.Fprint(stdout, label)
	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func uploadRootCandidates(outputDir string) ([]string, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(outputDir, entry.Name())
		if _, err := os.Stat(filepath.Join(path, "manifest.json")); err == nil {
			result = append(result, path)
		}
	}
	return result, nil
}

func validateManualUploadManifest(root string) error {
	manifest, err := readManifest(root)
	if err != nil {
		return err
	}
	if !manifestHasVideoTracks(manifest) {
		return nil
	}
	if manifestVideoEncrypted(manifest) {
		return nil
	}
	return fmt.Errorf("manual upload is not allowed for unencrypted video")
}

func manifestHasVideoTracks(data map[string]any) bool {
	tracks, ok := data["video_tracks"].([]any)
	return ok && len(tracks) > 0
}

func manifestVideoEncrypted(data map[string]any) bool {
	playback, ok := data["playback"].(map[string]any)
	if !ok {
		return false
	}
	encryption, ok := playback["encryption"].(map[string]any)
	if !ok {
		return false
	}
	scheme, _ := encryption["scheme"].(string)
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	return scheme != "" && scheme != "none"
}
