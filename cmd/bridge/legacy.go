package main

import "os"

// legacyVerbForwards maps a single-token first arg (after os.Args[0]) to its
// modern verb-form equivalent. Used for `bridge away`, `bridge back`, `bridge auto`.
var legacyVerbForwards = map[string][]string{
	"away": {"presence", "away"},
	"back": {"presence", "back"},
	"auto": {"presence", "auto"},
}

// rewriteLegacyArgs takes os.Args-shaped input (program name + user args) and
// applies legacy-flag forwarding so muscle memory from the bash bridge keeps
// working. Pure function — no side effects.
//
// Mapping table (top-level, for the `noop` fallback path — bypasses the shim's
// preflight directive flow):
//
//	bridge -r [trailing...]          → bridge list -r [trailing...]
//	bridge --refresh [trailing...]   → bridge list --refresh [trailing...]
//	bridge -D <name> [trailing...]   → bridge rm <name> --yes [trailing...]
//	bridge -a [trailing...]          → bridge sessions attach [trailing...]
//	bridge --attach [trailing...]    → bridge sessions attach [trailing...]
//	bridge --dashboard [trailing...] → bridge tui [trailing...]
//	bridge away|back|auto            → bridge presence away|back|auto
//
// Known modern verbs and __preflight are left alone.
//
// Note: the shim path (rewriteLegacyPreflight) routes -r/--refresh through the
// picker instead — only direct `command bridge -r` ends up here as `list -r`.
func rewriteLegacyArgs(args []string) []string {
	if len(args) < 2 {
		return args
	}
	first := args[1]

	// __preflight has its own dispatcher (rewriteLegacyPreflight) that mirrors
	// these rewrites at the directive layer.
	if first == "__preflight" {
		return args
	}
	if knownVerbs[first] {
		return args
	}

	switch first {
	case "--status":
		out := []string{args[0], "status"}
		out = append(out, args[2:]...)
		return out
	case "--dashboard":
		out := []string{args[0], "tui"}
		out = append(out, args[2:]...)
		return out
	case "-r":
		out := []string{args[0], "list", "-r"}
		out = append(out, args[2:]...)
		return out
	case "--refresh":
		out := []string{args[0], "list", "--refresh"}
		out = append(out, args[2:]...)
		return out
	case "-a", "--attach":
		out := []string{args[0], "sessions", "attach"}
		out = append(out, args[2:]...)
		return out
	case "-D":
		if len(args) < 3 {
			// Missing name. Let cobra surface the error rather than fabricating one.
			return args
		}
		out := []string{args[0], "rm", args[2], "--yes"}
		out = append(out, args[3:]...)
		return out
	}

	if forward, ok := legacyVerbForwards[first]; ok {
		out := []string{args[0]}
		out = append(out, forward...)
		out = append(out, args[2:]...)
		return out
	}
	return args
}

// rewriteLegacy applies rewriteLegacyArgs to os.Args in-place. Called from main().
func rewriteLegacy() {
	os.Args = rewriteLegacyArgs(os.Args)
}

// rewriteLegacyPreflight is the preflight-side analogue. The args slice here
// excludes both the program name and "__preflight" itself — it's what the user
// typed after `bridge`.
//
// Differs from rewriteLegacyArgs: -r and --refresh are NOT rewritten here.
// dispatchPreflight detects them and routes through the picker instead, which
// is the interactive UX the bash bridge provided.
func rewriteLegacyPreflight(args []string) []string {
	if len(args) == 0 {
		return args
	}
	first := args[0]
	if knownVerbs[first] {
		return args
	}
	switch first {
	case "--status":
		return append([]string{"status"}, args[1:]...)
	case "--dashboard":
		return append([]string{"tui"}, args[1:]...)
	case "-a", "--attach":
		return append([]string{"sessions", "attach"}, args[1:]...)
	case "-D":
		if len(args) < 2 {
			return args
		}
		return append([]string{"rm", args[1], "--yes"}, args[2:]...)
	}
	if forward, ok := legacyVerbForwards[first]; ok {
		out := append([]string{}, forward...)
		return append(out, args[1:]...)
	}
	return args
}
