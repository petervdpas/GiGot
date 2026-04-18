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
)

// Options is the parsed, validated CLI invocation. Holding the result as
// a value — rather than calling into the flag package from Execute —
// lets us unit-test the full arg-handling surface without any side
// effects (no config writes, no server starts, no log.Fatalf).
type Options struct {
	Mode                Mode
	ConfigPath          string
	InitFormidableFirst bool
	AddAdminUsername    string
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
		help           bool
		helpShort      bool
		configPath     string
		initFlag       bool
		formidableFirst bool
		addAdmin       string
		rotateKeys     bool
	)
	fs.BoolVar(&help, "help", false, "show this help and exit")
	fs.BoolVar(&helpShort, "h", false, "alias for -help")
	fs.StringVar(&configPath, "config", "", "path to gigot.json (default: ./gigot.json)")
	fs.BoolVar(&initFlag, "init", false, "write a fresh gigot.json and exit")
	fs.BoolVar(&formidableFirst, "formidable-first", false, "with -init, pre-enable server.formidable_first in the emitted config")
	fs.StringVar(&addAdmin, "add-admin", "", "create/update an admin account with the given username and exit")
	fs.BoolVar(&rotateKeys, "rotate-keys", false, "rotate the server keypair and rewrap sealed stores (stop the server first)")

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
	if oneShots > 1 {
		return Options{}, fmt.Errorf("only one of -init, -add-admin, -rotate-keys can be used per invocation")
	}

	// -formidable-first only makes sense alongside -init. Silently
	// ignoring it when set without -init would hide a real typo.
	if formidableFirst && !initFlag {
		return Options{}, fmt.Errorf("-formidable-first is only valid with -init")
	}

	opts := Options{
		ConfigPath:          configPath,
		InitFormidableFirst: formidableFirst,
		AddAdminUsername:    addAdmin,
	}
	switch {
	case initFlag:
		opts.Mode = ModeInit
	case addAdmin != "":
		opts.Mode = ModeAddAdmin
	case rotateKeys:
		opts.Mode = ModeRotateKeys
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

Help:
  -help, -h               Show this help and exit.
`
}

// writeHelpTo renders helpText to the given writer. Useful for tests
// and for Execute()'s stdout path alike.
func writeHelpTo(w io.Writer) {
	fmt.Fprint(w, helpText())
}
