package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Mode is which top-level behaviour the user asked for. Exactly one mode
// is selected per invocation; combinations are rejected by Parse with a
// usage-friendly error.
type Mode int

const (
	// ModeServe runs the HTTP server. This is the default when no
	// one-shot command flag is set.
	ModeServe Mode = iota
	// ModeHelp prints helpText() to stdout and exits.
	ModeHelp
	// ModeInit writes a fresh gigot.json.
	ModeInit
	// ModeAddAdmin creates or updates an admin account.
	ModeAddAdmin
	// ModeRotateKeys rotates the server keypair and rewraps sealed
	// stores.
	ModeRotateKeys
	// ModeWipe destructively removes on-disk state. Which slice of
	// state is controlled by WipeTargets on Options.
	ModeWipe
	// ModeAddDemoSetup provisions the Postman-collection demo admin /
	// repo / credential / subscription token. See internal/cli/demo.go.
	ModeAddDemoSetup
	// ModeRemoveDemoSetup tears down everything ModeAddDemoSetup set up.
	ModeRemoveDemoSetup
	// ModeHealthcheck probes the configured server.host:server.port and
	// exits 0 on a 2xx response, non-zero otherwise. Wired from the
	// Dockerfile's HEALTHCHECK because distroless images carry no
	// curl/wget binary.
	ModeHealthcheck
)

// WipeTargets is the set of on-disk artefacts a ModeWipe invocation
// should remove. -factory-reset sets every field; the granular
// -wipe-* flags each set exactly one.
type WipeTargets struct {
	Repos        bool
	Admins       bool
	Tokens       bool
	Clients      bool
	Sessions     bool
	Credentials  bool
	Destinations bool
	// Keys is only set via -factory-reset. A standalone keypair wipe
	// would leave every sealed store unreadable, which is indistinguishable
	// from a bricked server; we refuse to offer that footgun on its own.
	Keys bool
}

// Any reports whether at least one target is selected. Used by Parse
// to distinguish "wipe mode" from "no wipe flags at all".
func (w WipeTargets) Any() bool {
	return w.Repos || w.Admins || w.Tokens || w.Clients ||
		w.Sessions || w.Credentials || w.Destinations || w.Keys
}

// Options is the parsed, validated CLI invocation. Holding the result as
// a value — rather than calling into the flag package from Execute —
// lets us unit-test the full arg-handling surface without any side
// effects (no config writes, no server starts, no log.Fatalf).
type Options struct {
	Mode                Mode
	ConfigPath          string
	InitFormidableFirst bool
	AddAdminUsername    string
	Wipe                WipeTargets
	WipeAssumeYes       bool
	// AllowLocalOverride is the resolved value of `-allow-local`
	// when that flag was explicitly set, and nil when it wasn't —
	// nil means "leave cfg.Auth.AllowLocal alone."
	AllowLocalOverride *bool
}

// ErrHelpRequested is returned by Parse when -help / -h was passed so
// the caller can print helpText() and exit cleanly. It is a value, not a
// usage error, so it is distinguishable from real parse failures.
var ErrHelpRequested = errors.New("help requested")

// Parse turns os.Args[1:] into a validated Options. It returns an error
// (not a log.Fatal) on every failure mode so tests can assert behaviour
// exhaustively. Errors fall into three buckets:
//   - ErrHelpRequested when the user asked for help
//   - flag.ErrHelp / unknown-flag errors from the flag package
//   - validation errors for disallowed flag combinations
//
// All errors are safe to render to the user as-is.
func Parse(args []string) (Options, error) {
	fs := flag.NewFlagSet("gigot", flag.ContinueOnError)
	// Capture the flag package's own error output so callers see a
	// coherent error rather than stderr leakage during tests.
	var errBuf bytes.Buffer
	fs.SetOutput(&errBuf)
	fs.Usage = func() { fmt.Fprint(fs.Output(), helpText()) }

	var (
		help             bool
		helpShort        bool
		configPath       string
		initFlag         bool
		formidableFirst  bool
		addAdmin         string
		rotateKeys       bool
		wipeRepos        bool
		wipeAdmins       bool
		wipeTokens       bool
		wipeClients      bool
		wipeSessions     bool
		wipeCredentials  bool
		wipeDestinations bool
		factoryReset     bool
		assumeYes        bool
		addDemoSetup     bool
		removeDemoSetup  bool
		healthcheck      bool
		allowLocal       bool
	)
	fs.BoolVar(&help, "help", false, "show this help and exit")
	fs.BoolVar(&helpShort, "h", false, "alias for -help")
	fs.StringVar(&configPath, "config", "", "path to gigot.json (default: ./gigot.json)")
	fs.BoolVar(&initFlag, "init", false, "write a fresh gigot.json and exit")
	fs.BoolVar(&formidableFirst, "formidable-first", false, "with -init, pre-enable server.formidable_first in the emitted config")
	fs.StringVar(&addAdmin, "add-admin", "", "create/update an admin account with the given username and exit")
	fs.BoolVar(&rotateKeys, "rotate-keys", false, "rotate the server keypair and rewrap sealed stores (stop the server first)")
	fs.BoolVar(&wipeRepos, "wipe-repos", false, "delete every bare repository under storage.repo_root (stop the server first)")
	fs.BoolVar(&wipeAdmins, "wipe-admins", false, "delete data/accounts.enc + data/admins.enc (all accounts; legacy store is migration backup)")
	fs.BoolVar(&wipeTokens, "wipe-tokens", false, "delete data/tokens.enc (all subscription keys)")
	fs.BoolVar(&wipeClients, "wipe-clients", false, "delete data/clients.enc (all enrolled client pubkeys)")
	fs.BoolVar(&wipeSessions, "wipe-sessions", false, "delete data/sessions.enc (all active admin sessions)")
	fs.BoolVar(&wipeCredentials, "wipe-credentials", false, "delete data/credentials.enc (outbound credential vault)")
	fs.BoolVar(&wipeDestinations, "wipe-destinations", false, "delete data/destinations.enc (per-repo mirror destinations)")
	fs.BoolVar(&factoryReset, "factory-reset", false, "wipe every sealed store, every repo, the keypair, and rotation backups (stop the server first)")
	fs.BoolVar(&assumeYes, "yes", false, "skip the interactive confirmation prompt for wipe flags")
	fs.BoolVar(&addDemoSetup, "add-demo-setup", false, "provision the Postman demo admin, repo, credential and subscription token (stop the server first)")
	fs.BoolVar(&removeDemoSetup, "remove-demo-setup", false, "tear down everything -add-demo-setup provisioned (stop the server first)")
	fs.BoolVar(&healthcheck, "healthcheck", false, "probe http://<server.host>:<server.port>/ and exit 0/1 (used by Dockerfile HEALTHCHECK)")
	fs.BoolVar(&allowLocal, "allow-local", true, "override cfg.Auth.AllowLocal for this invocation (-allow-local=false disables local password login; only meaningful with serve)")

	if err := fs.Parse(args); err != nil {
		// flag.ErrHelp is returned when the flag package's builtin
		// -h/-help triggers. Surface it as our own ErrHelpRequested so
		// Execute() renders our grouped help, not flag's flat dump.
		if errors.Is(err, flag.ErrHelp) {
			return Options{Mode: ModeHelp}, ErrHelpRequested
		}
		// Any other parse error: the flag package has already written a
		// message to errBuf — include it so the user sees what broke.
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return Options{}, fmt.Errorf("%s", msg)
	}
	if help || helpShort {
		return Options{Mode: ModeHelp}, ErrHelpRequested
	}

	granularWipes := WipeTargets{
		Repos:        wipeRepos,
		Admins:       wipeAdmins,
		Tokens:       wipeTokens,
		Clients:      wipeClients,
		Sessions:     wipeSessions,
		Credentials:  wipeCredentials,
		Destinations: wipeDestinations,
	}
	wantsWipe := factoryReset || granularWipes.Any()

	// Validate mutually-exclusive one-shot commands. Running the
	// server is the implicit default only when no one-shot was
	// requested.
	oneShots := 0
	if initFlag {
		oneShots++
	}
	if addAdmin != "" {
		oneShots++
	}
	if rotateKeys {
		oneShots++
	}
	if wantsWipe {
		oneShots++
	}
	if addDemoSetup {
		oneShots++
	}
	if removeDemoSetup {
		oneShots++
	}
	if healthcheck {
		oneShots++
	}
	if oneShots > 1 {
		return Options{}, fmt.Errorf("only one of -init, -add-admin, -rotate-keys, -add-demo-setup, -remove-demo-setup, -healthcheck, or the -wipe-*/-factory-reset family can be used per invocation")
	}

	// -formidable-first only makes sense alongside -init. Silently
	// ignoring it when set without -init would hide a real typo.
	if formidableFirst && !initFlag {
		return Options{}, fmt.Errorf("-formidable-first is only valid with -init")
	}

	// -factory-reset already implies every granular wipe, so combining
	// the two is always a user mistake — reject it rather than silently
	// treating the granular flags as redundant.
	if factoryReset && granularWipes.Any() {
		return Options{}, fmt.Errorf("-factory-reset already covers every -wipe-* target; do not combine them")
	}

	// -yes is meaningless outside a wipe invocation. Catching it here
	// avoids the confusing UX of "I passed -yes and nothing happened".
	if assumeYes && !wantsWipe {
		return Options{}, fmt.Errorf("-yes is only valid with a -wipe-* or -factory-reset flag")
	}

	opts := Options{
		ConfigPath:          configPath,
		InitFormidableFirst: formidableFirst,
		AddAdminUsername:    addAdmin,
		WipeAssumeYes:       assumeYes,
	}
	// Only treat -allow-local as an override if the user actually
	// passed it. A nil AllowLocalOverride means "keep cfg.Auth.AllowLocal
	// as-is" — critical so tests / scripts that never touch this flag
	// don't silently flip a config value.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "allow-local" {
			v := allowLocal
			opts.AllowLocalOverride = &v
		}
	})
	switch {
	case initFlag:
		opts.Mode = ModeInit
	case addAdmin != "":
		opts.Mode = ModeAddAdmin
	case rotateKeys:
		opts.Mode = ModeRotateKeys
	case factoryReset:
		opts.Mode = ModeWipe
		opts.Wipe = WipeTargets{
			Repos:        true,
			Admins:       true,
			Tokens:       true,
			Clients:      true,
			Sessions:     true,
			Credentials:  true,
			Destinations: true,
			Keys:         true,
		}
	case granularWipes.Any():
		opts.Mode = ModeWipe
		opts.Wipe = granularWipes
	case addDemoSetup:
		opts.Mode = ModeAddDemoSetup
	case removeDemoSetup:
		opts.Mode = ModeRemoveDemoSetup
	case healthcheck:
		opts.Mode = ModeHealthcheck
	default:
		opts.Mode = ModeServe
	}
	return opts, nil
}

// helpText is the grouped help output for `gigot -help`. Returning a
// string (not writing to stdout) keeps it pure so tests can assert the
// presence of every flag and every rule.
func helpText() string {
	return `Usage: gigot [FLAGS]

GiGot is a git-backed server for Formidable clients. With no flags it
runs the HTTP server using the config at -config (or ./gigot.json).

Run mode (default when no one-shot flag is set):
  -config <path>          Path to gigot.json (default: ./gigot.json).
  -allow-local=<bool>     Override cfg.Auth.AllowLocal for this
                          invocation. Passing the flag at all sets the
                          override; omitting it leaves the config
                          value untouched. -allow-local=false disables
                          the /admin/login local password path (useful
                          for break-glass testing of OAuth once Phase 3
                          ships).

One-shot commands (each exits after running; mutually exclusive):
  -init                   Write a fresh gigot.json in the current
                          directory.
    -formidable-first     With -init, pre-enable server.formidable_first
                          so both init and clone stamp the Formidable
                          context marker by default (design doc §2.7).
  -add-admin <username>   Create or update an admin account. Prompts for
                          a password on stdin.
  -rotate-keys            Rotate the server keypair and rewrap every
                          sealed store. Stop the server first.
  -wipe-repos             Delete every bare repository under
                          storage.repo_root. Stop the server first.
  -wipe-admins            Delete data/accounts.enc (and the legacy
                          data/admins.enc backup). All admin and
                          regular accounts gone; recreate with
                          -add-admin.
  -wipe-tokens            Delete data/tokens.enc (all subscription keys).
  -wipe-clients           Delete data/clients.enc (all enrolled clients).
  -wipe-sessions          Delete data/sessions.enc (all admin sessions).
  -wipe-credentials       Delete data/credentials.enc (credential vault).
  -wipe-destinations      Delete data/destinations.enc (mirror targets).
  -factory-reset          Wipe every sealed store, every repo, the
                          keypair, and rotation backups — restores a
                          fresh-install state, preserving only
                          gigot.json. Stop the server first.
    -yes                  Skip the interactive confirmation prompt for
                          any -wipe-* or -factory-reset invocation
                          (intended for non-interactive scripts).
  -add-demo-setup         Provision the Postman-collection demo state:
                          admin "demo" / password "demo-password",
                          scaffolded repo "postman-demo", credential
                          "postman-pat", and a fresh subscription token
                          (printed). Stop the server first.
  -remove-demo-setup      Tear down everything -add-demo-setup created.
  -healthcheck            Probe http://<server.host>:<server.port>/ with a
                          short timeout and exit 0 on a 2xx response, 1
                          otherwise. Wired from the Dockerfile HEALTHCHECK
                          because the distroless runtime image carries no
                          curl or wget. Resolves a 0.0.0.0 / :: bind to
                          127.0.0.1 so the probe stays loopback-local.

  The -wipe-* flags compose (e.g. -wipe-admins -wipe-tokens removes
  both stores in one invocation). -factory-reset is a shorthand that
  covers every target; combining it with granular -wipe-* flags is
  rejected.

Help:
  -help, -h               Show this help and exit.
`
}

// writeHelpTo renders helpText to the given writer. Useful for tests
// and for Execute()'s stdout path alike.
func writeHelpTo(w io.Writer) {
	fmt.Fprint(w, helpText())
}
