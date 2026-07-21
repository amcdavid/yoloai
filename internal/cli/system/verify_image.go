package system

// ABOUTME: `yoloai system verify-image` — checks that an image satisfies yoloAI's
// ABOUTME: runtime contract, so a custom base fails with a diagnostic, not at boot.

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/cli/extension"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

func newSystemVerifyImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify-image <image-ref>",
		Short: "Check that an image satisfies yoloAI's runtime contract",
		Long: `Run yoloAI's runtime contract against an image.

verify-image starts a throwaway container from <image-ref> and probes it for
everything yoloAI needs to drive a sandbox: tmux, gosu, git, python3, the yoloai
user, the /yoloai state tree, and more. It reports what is missing instead of
letting a non-conformant image fail at container start with a bare exec error.

Use it when building a custom base image, or before pointing a profile at a
third-party image. With --agent, it also checks that the agent's launch binary
is present — an image can be structurally sound yet lack the agent a sandbox
would try to run.

Exits 0 when every hard requirement is met (soft warnings do not fail the
check), 1 otherwise. Only OCI backends (docker, podman) can verify images.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerifyImage(cmd, args[0], cliutil.ResolveBackend(cmd))
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().String("agent", "", "Also require this agent's launch binary")

	return cmd
}

func runVerifyImage(cmd *cobra.Command, imageRef string, backend yoloai.BackendType) error {
	out := cmd.OutOrStdout()
	agentName, _ := cmd.Flags().GetString("agent")

	sys, err := cliutil.SystemWithEnv(cliutil.BackendEnv(cmd))
	if err != nil {
		return err
	}
	result, err := sys.VerifyImage(cmd.Context(), backend, imageRef, yoloai.AgentType(agentName))
	if err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		violations := make([]map[string]any, 0, len(result.Violations))
		for _, v := range result.Violations {
			violations = append(violations, map[string]any{
				"name": v.Name, "why": v.Why, "soft": v.Soft,
			})
		}
		if err := cliutil.WriteJSON(out, map[string]any{
			"image":      result.Image,
			"ok":         result.OK(),
			"violations": violations,
		}); err != nil {
			return err
		}
		if !result.OK() {
			// The JSON payload is the output; signal the failure through the exit
			// code only, so scripts can `|| handle` without parsing a duplicate
			// error line printed after the JSON.
			return &extension.ExitError{Code: 1}
		}
		return nil
	}

	if len(result.Violations) == 0 {
		fmt.Fprintf(out, "%s satisfies the yoloAI runtime contract\n", result.Image) //nolint:errcheck
		return nil
	}

	// Hard misses first, then soft — the ordering a reader acts on.
	var hard, soft int
	fmt.Fprintf(out, "%s is missing:\n", result.Image) //nolint:errcheck
	for _, v := range result.Violations {
		if v.Soft {
			continue
		}
		hard++
		fmt.Fprintf(out, "  ✗ %-28s %s\n", v.Name, v.Why) //nolint:errcheck
	}
	for _, v := range result.Violations {
		if !v.Soft {
			continue
		}
		soft++
		fmt.Fprintf(out, "  ! %-28s %s (degraded, not fatal)\n", v.Name, v.Why) //nolint:errcheck
	}

	if !result.OK() {
		return fmt.Errorf("%s is missing %d required item(s)", result.Image, hard)
	}
	fmt.Fprintf(out, "\nAll hard requirements met; %d soft warning(s) above.\n", soft) //nolint:errcheck
	return nil
}
