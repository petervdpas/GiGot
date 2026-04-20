package cli

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/crypto"
	"github.com/petervdpas/GiGot/internal/server"
	"golang.org/x/term"
)

// Execute is the entry point invoked from main.go. It parses argv,
// renders help or an error as needed, and dispatches to the mode
// handler. All non-trivial logic lives in Parse (pure) or the runXxx
// helpers (pure aside from filesystem side effects), so this function
// stays a thin switch that is safe to call and easy to audit.
func Execute() {
	opts, err := Parse(os.Args[1:])
	if err != nil {
		if errors.Is(err, ErrHelpRequested) {
			writeHelpTo(os.Stdout)
			return
		}
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Run `gigot -help` for usage.")
		os.Exit(2)
	}

	switch opts.Mode {
	case ModeHelp:
		writeHelpTo(os.Stdout)
		return
	case ModeInit:
		if err := writeInitConfig("gigot.json", opts.InitFormidableFirst); err != nil {
			log.Fatalf("failed to write gigot.json: %v", err)
		}
		if opts.InitFormidableFirst {
			fmt.Println("Wrote gigot.json (Formidable-first mode enabled)")
		} else {
			fmt.Println("Wrote default gigot.json")
		}
		return
	}

	// All remaining modes need a loaded config.
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	switch opts.Mode {
	case ModeAddAdmin:
		if err := runAddAdmin(cfg, opts.AddAdminUsername); err != nil {
			log.Fatalf("add-admin: %v", err)
		}
		return
	case ModeRotateKeys:
		if err := runRotateKeys(cfg); err != nil {
			log.Fatalf("rotate-keys: %v", err)
		}
		return
	case ModeWipe:
		if err := runWipe(cfg, opts.Wipe, opts.WipeAssumeYes, os.Stdout, os.Stdin); err != nil {
			log.Fatalf("wipe: %v", err)
		}
		return
	case ModeAddDemoSetup:
		if err := runAddDemoSetup(cfg, os.Stdout); err != nil {
			log.Fatalf("add-demo-setup: %v", err)
		}
		return
	case ModeRemoveDemoSetup:
		if err := runRemoveDemoSetup(cfg, os.Stdout); err != nil {
			log.Fatalf("remove-demo-setup: %v", err)
		}
		return
	case ModeServe:
		if opts.AllowLocalOverride != nil {
			cfg.Auth.AllowLocal = *opts.AllowLocalOverride
		}
		fmt.Printf("GiGot server starting on %s:%d\n", cfg.Server.Host, cfg.Server.Port)
		fmt.Printf("Repository root: %s\n", cfg.Storage.RepoRoot)
		fmt.Printf("Admin UI: http://%s:%d/admin\n", cfg.Server.Host, cfg.Server.Port)
		if !cfg.Auth.AllowLocal {
			fmt.Println("Local password login is DISABLED (auth.allow_local=false).")
		}
		srv := server.New(cfg)
		if err := srv.Start(); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}
}

// writeInitConfig emits a fresh gigot.json at path. When formidableFirst
// is true, server.formidable_first is pre-enabled in the emitted config so
// operators don't have to hand-edit the default to run in Formidable-first
// mode. Extracted from Execute() so the flag path is exhaustively testable
// without a subprocess harness.
func writeInitConfig(path string, formidableFirst bool) error {
	cfg := config.Defaults()
	if formidableFirst {
		cfg.Server.FormidableFirst = true
	}
	return cfg.Save(path)
}

func runRotateKeys(cfg *config.Config) error {
	fmt.Println("Rotating server keypair...")
	res, err := crypto.Rotate(
		cfg.Crypto.PrivateKeyPath,
		cfg.Crypto.PublicKeyPath,
		crypto.DefaultSealedFiles(cfg.Crypto.DataDir),
	)
	if err != nil {
		return err
	}
	fmt.Printf("  old pubkey: %s\n", res.OldPublicKey.Encode())
	fmt.Printf("  new pubkey: %s\n", res.NewPublicKey.Encode())
	fmt.Printf("  backup suffix: .bak.%s\n", res.BackupSuffix)
	if len(res.Rewrapped) == 0 {
		fmt.Println("  no sealed stores needed rewrapping")
	} else {
		fmt.Printf("  rewrapped: %d file(s)\n", len(res.Rewrapped))
		for _, p := range res.Rewrapped {
			fmt.Printf("    - %s\n", p)
		}
	}
	fmt.Println("Rotation complete. Formidable clients will pick up the new pubkey on their next /api/crypto/pubkey fetch.")
	return nil
}

func runAddAdmin(cfg *config.Config, username string) error {
	prompt := newPrompter()
	pw, err := prompt.password("Password for " + username + ": ")
	if err != nil {
		return err
	}
	if pw == "" {
		return fmt.Errorf("password must not be empty")
	}
	confirm, err := prompt.password("Confirm password: ")
	if err != nil {
		return err
	}
	if pw != confirm {
		return fmt.Errorf("passwords do not match (first had %d chars, second had %d chars)", len(pw), len(confirm))
	}

	srv := server.New(cfg)
	store := srv.Accounts()
	if _, err := store.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: username,
		Role:       accounts.RoleAdmin,
	}); err != nil {
		return err
	}
	if err := store.SetPassword(username, pw); err != nil {
		return err
	}
	fmt.Printf("Admin %q saved\n", username)
	return nil
}

type prompter struct {
	reader *bufio.Reader
	isTTY  bool
}

func newPrompter() *prompter {
	return &prompter{
		reader: bufio.NewReader(os.Stdin),
		isTTY:  term.IsTerminal(int(syscall.Stdin)),
	}
}

func (p *prompter) password(label string) (string, error) {
	fmt.Print(label)
	if p.isTTY {
		pw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return "", err
		}
		return cleanPasswordInput(string(pw)), nil
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return cleanPasswordInput(line), nil
}

// cleanPasswordInput normalises a password string read from the terminal or
// piped stdin. Terminals and pastes occasionally smuggle in a trailing CR, a
// stray newline, or whitespace; trimming prevents a silent mismatch between
// the initial entry and the confirmation prompt.
func cleanPasswordInput(s string) string {
	return strings.TrimRight(s, "\r\n\t ")
}
