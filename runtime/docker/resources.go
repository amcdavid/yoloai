// ABOUTME: Embeds Docker build resources (Dockerfile, entrypoints, Python
// ABOUTME: monitor scripts) and exposes them for the Docker backend image builder.
// Package docker embeds Docker build resources (Dockerfile, entrypoints, scripts).
// The shared tmux.conf lives in internal/resources/tmux (neutral location).
package docker

import (
	_ "embed"

	tmuxres "github.com/kstenerud/yoloai/internal/resources/tmux"
	"github.com/kstenerud/yoloai/runtime/monitor"
)

// embeddedBatteries is the stack half of yoloai-base — toolchains, agent CLIs,
// dev tooling. It is NOT a complete image on its own; ComposeDockerfile appends
// embeddedRuntimeLayer to it.
//
//go:embed resources/Dockerfile
var embeddedBatteries []byte

// embeddedRuntimeLayer is the fragment that makes any Debian/Ubuntu-derived
// image drivable by yoloAI: the runtime user, the /yoloai tree, the entrypoint
// scripts, gosu, the managed label, and the ENTRYPOINT. Appended to the
// batteries half to compose yoloai-base, and reusable against a user-supplied
// base. It establishes exactly internal/imagecontract.Static().
//
//go:embed resources/runtime-layer.Dockerfile
var embeddedRuntimeLayer []byte

// embeddedMinimalStack is the lean stack half for `base: yoloai-minimal` — a
// Debian base plus only the packages the runtime layer and agent installs need
// (curl, gnupg, ca-certificates). Like embeddedBatteries it is NOT a complete
// image; the assembler appends agent installs and the runtime layer.
//
//go:embed resources/minimal.Dockerfile
var embeddedMinimalStack []byte

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte

//go:embed resources/entrypoint.py
var embeddedEntrypointPy []byte

//go:embed resources/firewall.py
var embeddedFirewallPy []byte

//go:embed resources/install-firewall.py
var embeddedInstallFirewallPy []byte

// embeddedTmuxConf is the shared default tmux.conf, sourced from the neutral
// internal/resources/tmux package rather than re-embedded here.
var embeddedTmuxConf = tmuxres.Embedded()

// embeddedSandboxSetup provides the consolidated Python sandbox setup script
// from the runtime/monitor package for inclusion in Docker image builds.
var embeddedSandboxSetup = monitor.SetupScript()

// embeddedSetupHelpers provides the typed pure-function helpers module
// imported by sandbox-setup.py at runtime. Must ship alongside it.
var embeddedSetupHelpers = monitor.SetupHelpers()

// embeddedTmuxIO provides the injectable tmux/subprocess wrappers module
// imported by sandbox-setup.py at runtime. Must ship alongside it.
var embeddedTmuxIO = monitor.TmuxIO()

// embeddedStatusMonitor provides the shared Python status monitor script
// from the runtime/monitor package for inclusion in Docker image builds.
var embeddedStatusMonitor = monitor.Script()

// embeddedDiagnoseIdle provides the idle detection diagnostic script.
var embeddedDiagnoseIdle = monitor.DiagnoseScript()

// embeddedAgentRun provides the fall-to-shell agent launch wrapper (D96),
// installed executable in /yoloai/bin and invoked by the launch command for
// hook-authoritative agents.
var embeddedAgentRun = monitor.AgentRunScript()

// embeddedYoloaiResume provides the in-sandbox resume command (D96 DD4),
// installed executable in /yoloai/bin as `yoloai-resume`.
var embeddedYoloaiResume = monitor.YoloaiResumeScript()
