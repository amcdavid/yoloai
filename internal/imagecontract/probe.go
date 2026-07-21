// ABOUTME: Renders the contract into a POSIX sh probe script and parses its output,
// ABOUTME: so any backend that can exec a shell in an image can check conformance.

package imagecontract

import (
	"fmt"
	"strings"
)

// resultPrefix marks the probe script's machine-readable lines. The script runs
// inside an arbitrary user image whose shell profile may print banners, MOTDs,
// or activation notices, so results are prefixed and every non-matching line is
// discarded by [ParseProbe] rather than assumed to be noise-free.
const resultPrefix = "YOLOAI_CONTRACT\t"

// ProbeScript renders reqs into a POSIX sh script that reports, one line per
// requirement, whether the image satisfies it.
//
// The script is deliberately POSIX sh and uses only `command -v`, `test`, and
// `id` — a contract checker cannot presume bash, coreutils extensions, or any
// of the very things it is checking for. It always exits 0: the caller reads the
// results, and a non-zero exit would be indistinguishable from the image lacking
// a shell at all.
func ProbeScript(reqs []Requirement) string {
	var b strings.Builder
	// No `set -e`: a failing probe is data, not an error, and must not abort
	// the remaining checks.
	b.WriteString("#!/bin/sh\n")
	for _, r := range reqs {
		fmt.Fprintf(&b, "if %s; then s=OK; else s=MISS; fi\n", probeTest(r))
		fmt.Fprintf(&b, "printf '%s%%s\\t%%s\\n' \"$s\" %s\n", resultPrefix, shellQuote(r.Name))
	}
	b.WriteString("exit 0\n")
	return b.String()
}

// probeTest renders the sh test expression for one requirement.
func probeTest(r Requirement) string {
	q := shellQuote(r.Name)
	switch r.Kind {
	case KindBinary:
		return "command -v " + q + " >/dev/null 2>&1"
	case KindDir:
		return "[ -d " + q + " ]"
	case KindFile:
		// -e, not -f: several of these are bind-mount targets that a running
		// sandbox may have replaced with a directory or a socket. Existence is
		// what the contract requires; the type is the mount's business.
		return "[ -e " + q + " ]"
	case KindUser:
		return "id -u " + q + " >/dev/null 2>&1"
	default:
		// An unhandled Kind must fail loudly rather than silently pass: a new
		// Kind added without a probe would otherwise report every image as
		// conformant.
		return "false"
	}
}

// shellQuote wraps s in single quotes, escaping any embedded single quote.
// Requirement names are compile-time constants today, but this script is
// interpolated into a shell that runs as root inside a container, so it does
// not rely on that staying true.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ProbeResult pairs a requirement with whether the probed image satisfied it.
type ProbeResult struct {
	Req     Requirement
	Present bool
}

// ParseProbe matches the probe script's output back to reqs, returning one
// result per requirement in the same order.
//
// A requirement whose line is absent from the output is reported as missing:
// the probe emits one line per requirement unconditionally, so a gap means the
// script was truncated — an image that killed the shell partway is not one to
// report as conformant.
//
// Unrecognised lines are ignored, which is what makes this safe against images
// whose shell startup prints banners.
func ParseProbe(reqs []Requirement, output string) []ProbeResult {
	seen := make(map[string]bool, len(reqs))
	for _, line := range strings.Split(output, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), strings.TrimSpace(resultPrefix))
		if !ok {
			continue
		}
		status, name, ok := strings.Cut(strings.TrimLeft(rest, "\t "), "\t")
		if !ok {
			continue
		}
		// A name probed twice (a requirement listed in both tiers) is present
		// only if every probe of it passed.
		if prev, dup := seen[name]; dup {
			seen[name] = prev && status == "OK"
			continue
		}
		seen[name] = status == "OK"
	}

	out := make([]ProbeResult, len(reqs))
	for i, r := range reqs {
		out[i] = ProbeResult{Req: r, Present: seen[r.Name]}
	}
	return out
}

// HasResults reports whether output contains at least one probe result line.
// It distinguishes "the probe ran, and here is what it found" from "the
// container never produced output" (a bad image ref, an unrunnable image, a
// missing shell) — a distinction a caller needs to decide whether a non-zero
// container exit is fatal or just reports missing requirements.
func HasResults(output string) bool {
	marker := strings.TrimSpace(resultPrefix)
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// Missing filters results down to the unsatisfied ones, preserving order.
func Missing(results []ProbeResult) []ProbeResult {
	var out []ProbeResult
	for _, r := range results {
		if !r.Present {
			out = append(out, r)
		}
	}
	return out
}
