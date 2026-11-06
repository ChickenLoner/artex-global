package agent

import (
	"log"
	"path/filepath"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/noaadapter"
)

// noaWarn returns a diagnostics sink tagging non-fatal noa messages with the
// session, routed through the package logger (agents have no per-instance one).
func noaWarn(session string) func(string) {
	return func(msg string) { log.Printf("[noa] %s: %s", session, msg) }
}

// noa is the "model-driven context compression" mechanism introduced in norma v0.4.0, exposed as a platform experimental feature that users toggle in
// system settings. It is mutually exclusive with built-in compaction: noaadapter.Enable is the only entry point and, in one call, attaches the
// context takeover component (Compactor), the Compress tool and three resident prompt segments; not calling Enable means it is off
// (built-in compaction works as usual). The switch is resolved by the noaEnabledFn injected into each agent and is read once per run,
// so toggling only affects runs started afterwards and no agent rebuild is needed.

// enableNoa wires noa into opts when the resolver reports it enabled. archiveRoot is the persistent base directory for the original text of compressed content
// (the global workDir; every agent uses <workDir>/noa uniformly instead of scattering it across task/intent directories), and sessionID
// names the archive subdirectory beneath it (globally unique, so there are no conflicts within the same base directory).
//
// noa is an experimental feature: a failure to attach it must not interrupt real tasks. On error it is reported via onWarn and falls back to built-in compression.
// On successful enablement opts.Compaction is cleared, to avoid agentcore warning that "two context managers are set at the same time".
func enableNoa(opts *agentcore.Options, enabled func() bool, archiveRoot, sessionID string, onWarn func(string)) {
	if enabled == nil || !enabled() {
		return
	}
	if opts.OnWarn == nil {
		opts.OnWarn = onWarn
	}
	if err := noaadapter.Enable(opts, noaadapter.Options{
		ArchiveBaseDir: filepath.Join(archiveRoot, "noa"),
		SessionID:      sessionID,
		OnWarn:         onWarn,
	}); err != nil {
		if onWarn != nil {
			onWarn("noa 压缩启用失败,回退内置压缩:" + err.Error())
		}
		return
	}
	// Compactor overrides Compaction, but when both coexist agentcore warns every time; clear it explicitly.
	opts.Compaction = nil
}
