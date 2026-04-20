package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/petervdpas/GiGot/internal/config"
)

// wipeItem is one line in the plan the confirmation prompt shows and
// runWipe iterates over. Label is the human-readable description; Path
// is the actual filesystem target; IsDir flips RemoveAll vs Remove.
// Glob, when non-empty, expands at execution time (used for rotation
// backups during -factory-reset).
type wipeItem struct {
	Label string
	Path  string
	IsDir bool
	Glob  string
}

// buildWipePlan turns a (cfg, targets) pair into an ordered list of
// concrete filesystem operations. Keeping this pure makes the plan
// testable and the confirmation prompt honest — what the user sees is
// exactly what executeWipePlan will act on.
func buildWipePlan(cfg *config.Config, targets WipeTargets) []wipeItem {
	dataDir := cfg.Crypto.DataDir
	var plan []wipeItem
	if targets.Repos {
		plan = append(plan, wipeItem{
			Label: fmt.Sprintf("every bare repository under %s", cfg.Storage.RepoRoot),
			Path:  cfg.Storage.RepoRoot,
			IsDir: true,
		})
	}
	if targets.Admins {
		plan = append(plan, wipeItem{
			Label: "accounts (accounts.enc)",
			Path:  filepath.Join(dataDir, "accounts.enc"),
		})
		plan = append(plan, wipeItem{
			Label: "legacy admin store (admins.enc, migration backup)",
			Path:  filepath.Join(dataDir, "admins.enc"),
		})
	}
	if targets.Tokens {
		plan = append(plan, wipeItem{
			Label: "subscription keys (tokens.enc)",
			Path:  filepath.Join(dataDir, "tokens.enc"),
		})
	}
	if targets.Clients {
		plan = append(plan, wipeItem{
			Label: "enrolled client pubkeys (clients.enc)",
			Path:  filepath.Join(dataDir, "clients.enc"),
		})
	}
	if targets.Sessions {
		plan = append(plan, wipeItem{
			Label: "active admin sessions (sessions.enc)",
			Path:  filepath.Join(dataDir, "sessions.enc"),
		})
	}
	if targets.Credentials {
		plan = append(plan, wipeItem{
			Label: "outbound credential vault (credentials.enc)",
			Path:  filepath.Join(dataDir, "credentials.enc"),
		})
	}
	if targets.Destinations {
		plan = append(plan, wipeItem{
			Label: "mirror-sync destinations (destinations.enc)",
			Path:  filepath.Join(dataDir, "destinations.enc"),
		})
	}
	if targets.Keys {
		plan = append(plan, wipeItem{
			Label: "server private key",
			Path:  cfg.Crypto.PrivateKeyPath,
		})
		plan = append(plan, wipeItem{
			Label: "server public key",
			Path:  cfg.Crypto.PublicKeyPath,
		})
		// Rotation backups seal old state to the key we're about to
		// delete; keeping them would both defeat the reset and leave
		// unreadable cruft on disk.
		plan = append(plan, wipeItem{
			Label: "rotation backups (*.bak.*)",
			Glob:  filepath.Join(dataDir, "*.bak.*"),
		})
	}
	return plan
}

// runWipe renders the plan, confirms with the user (unless assumeYes),
// and removes each target. stdout and stdin are injected so tests can
// drive confirmation deterministically; in production Execute passes
// os.Stdout / os.Stdin.
func runWipe(cfg *config.Config, targets WipeTargets, assumeYes bool, stdout io.Writer, stdin io.Reader) error {
	plan := buildWipePlan(cfg, targets)
	if len(plan) == 0 {
		// Defense in depth: Parse should already reject "wipe mode with
		// no targets", but a zero-item plan would silently succeed and
		// look like a wipe, which is worse than a loud error.
		return fmt.Errorf("no wipe targets selected")
	}

	fmt.Fprintln(stdout, "The following will be permanently deleted:")
	for _, item := range plan {
		if item.Glob != "" {
			fmt.Fprintf(stdout, "  - %s (pattern: %s)\n", item.Label, item.Glob)
			continue
		}
		fmt.Fprintf(stdout, "  - %s (%s)\n", item.Label, item.Path)
	}

	if !assumeYes {
		fmt.Fprint(stdout, "Type 'yes' to proceed: ")
		reader := bufio.NewReader(stdin)
		answer, err := reader.ReadString('\n')
		if err != nil && answer == "" {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if strings.TrimSpace(answer) != "yes" {
			fmt.Fprintln(stdout, "Aborted. Nothing was removed.")
			return nil
		}
	}

	return executeWipePlan(plan, stdout)
}

// executeWipePlan removes each item. A non-existent path is treated as
// already-done, not a failure — the operator's intent was "this should
// not be on disk afterwards" and we achieve that either way. Real
// errors (permission denied, I/O) are collected and reported as a
// single joined error so the caller sees everything, not just the
// first failure.
func executeWipePlan(plan []wipeItem, stdout io.Writer) error {
	var failures []string
	for _, item := range plan {
		if item.Glob != "" {
			matches, err := filepath.Glob(item.Glob)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", item.Glob, err))
				continue
			}
			for _, m := range matches {
				if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
					failures = append(failures, fmt.Sprintf("%s: %v", m, err))
					continue
				}
				fmt.Fprintf(stdout, "  removed %s\n", m)
			}
			continue
		}

		var err error
		if item.IsDir {
			err = os.RemoveAll(item.Path)
		} else {
			err = os.Remove(item.Path)
		}
		if err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Sprintf("%s: %v", item.Path, err))
			continue
		}
		fmt.Fprintf(stdout, "  removed %s\n", item.Path)
	}
	if len(failures) > 0 {
		return fmt.Errorf("wipe completed with %d error(s): %s",
			len(failures), strings.Join(failures, "; "))
	}
	fmt.Fprintln(stdout, "Wipe complete.")
	return nil
}
