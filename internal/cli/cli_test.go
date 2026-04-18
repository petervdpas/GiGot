package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestParse_Success covers every valid invocation of the CLI. It's the
// central guarantee that adding/removing/renaming a flag changes the
// parse layer in one visible place — every mode and every flag
// combination lives here.
func TestParse_Success(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want Options
	}{
		{
			name: "no args starts the server with default config",
			args: nil,
			want: Options{Mode: ModeServe},
		},
		{
			name: "empty args equivalent to no args",
			args: []string{},
			want: Options{Mode: ModeServe},
		},
		{
			name: "-config path serves with a custom config",
			args: []string{"-config", "/etc/gigot/gigot.json"},
			want: Options{Mode: ModeServe, ConfigPath: "/etc/gigot/gigot.json"},
		},
		{
			name: "long double-dash also works (Go flag compat)",
			args: []string{"--config", "/etc/gigot/gigot.json"},
			want: Options{Mode: ModeServe, ConfigPath: "/etc/gigot/gigot.json"},
		},
		{
			name: "-init alone writes a default config",
			args: []string{"-init"},
			want: Options{Mode: ModeInit, InitFormidableFirst: false},
		},
		{
			name: "-init with -formidable-first emits the Formidable-first config",
			args: []string{"-init", "-formidable-first"},
			want: Options{Mode: ModeInit, InitFormidableFirst: true},
		},
		{
			name: "-init with -formidable-first in reversed order",
			args: []string{"-formidable-first", "-init"},
			want: Options{Mode: ModeInit, InitFormidableFirst: true},
		},
		{
			name: "-add-admin with username",
			args: []string{"-add-admin", "alice"},
			want: Options{Mode: ModeAddAdmin, AddAdminUsername: "alice"},
		},
		{
			name: "-add-admin with username and custom config",
			args: []string{"-add-admin", "alice", "-config", "/c.json"},
			want: Options{Mode: ModeAddAdmin, AddAdminUsername: "alice", ConfigPath: "/c.json"},
		},
		{
			name: "-rotate-keys triggers rotation mode",
			args: []string{"-rotate-keys"},
			want: Options{Mode: ModeRotateKeys},
		},
		{
			name: "-rotate-keys with custom config",
			args: []string{"-rotate-keys", "-config", "/c.json"},
			want: Options{Mode: ModeRotateKeys, ConfigPath: "/c.json"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.args)
			if err != nil {
				t.Fatalf("Parse(%v): unexpected error %v", c.args, err)
			}
			if got != c.want {
				t.Errorf("Parse(%v):\n  got  %+v\n  want %+v", c.args, got, c.want)
			}
		})
	}
}

// TestParse_Help verifies every help-triggering spelling produces
// ErrHelpRequested and Mode=ModeHelp. The flag-package builtin -h/-help
// handling is slightly different from our own bool flags, so both
// paths are checked.
func TestParse_Help(t *testing.T) {
	spellings := [][]string{
		{"-help"},
		{"--help"},
		{"-h"},
		{"--h"},
	}
	for _, args := range spellings {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opts, err := Parse(args)
			if !errors.Is(err, ErrHelpRequested) {
				t.Fatalf("want ErrHelpRequested, got %v", err)
			}
			if opts.Mode != ModeHelp {
				t.Errorf("mode: want ModeHelp, got %v", opts.Mode)
			}
		})
	}
}

// TestParse_ValidationErrors pins every rejection rule. Each case must
// trip a specific validation branch so tests catch the drift when a
// future flag refactor accidentally relaxes a rule.
func TestParse_ValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantMatch string // substring expected in error message
	}{
		{
			name:      "-formidable-first without -init is rejected",
			args:      []string{"-formidable-first"},
			wantMatch: "-formidable-first is only valid with -init",
		},
		{
			name:      "-init and -add-admin together is rejected",
			args:      []string{"-init", "-add-admin", "alice"},
			wantMatch: "only one of",
		},
		{
			name:      "-init and -rotate-keys together is rejected",
			args:      []string{"-init", "-rotate-keys"},
			wantMatch: "only one of",
		},
		{
			name:      "-add-admin and -rotate-keys together is rejected",
			args:      []string{"-add-admin", "alice", "-rotate-keys"},
			wantMatch: "only one of",
		},
		{
			name:      "all three one-shot flags together is rejected",
			args:      []string{"-init", "-add-admin", "alice", "-rotate-keys"},
			wantMatch: "only one of",
		},
		{
			name:      "unknown flag is rejected by the flag package",
			args:      []string{"-definitely-not-a-flag"},
			wantMatch: "flag provided but not defined",
		},
		{
			name:      "-config without a value is rejected",
			args:      []string{"-config"},
			wantMatch: "flag needs an argument",
		},
		{
			name:      "-add-admin without a value is rejected",
			args:      []string{"-add-admin"},
			wantMatch: "flag needs an argument",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.args)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantMatch)
			}
			if errors.Is(err, ErrHelpRequested) {
				t.Fatalf("want validation error, got ErrHelpRequested")
			}
			if !strings.Contains(err.Error(), c.wantMatch) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantMatch)
			}
		})
	}
}

// TestHelpText_AdvertisesEveryFlag is a presence check — every flag the
// parser accepts must also show up in -help output, so adding a flag
// without documenting it fails the test.
func TestHelpText_AdvertisesEveryFlag(t *testing.T) {
	text := helpText()
	required := []string{
		"-config",
		"-init",
		"-formidable-first",
		"-add-admin",
		"-rotate-keys",
		"-help",
		"-h",
	}
	for _, flag := range required {
		if !strings.Contains(text, flag) {
			t.Errorf("helpText missing %q", flag)
		}
	}

	// Also sanity-check the grouping headers and key constraint sentences
	// so the grouped-help contract doesn't silently drift to a flat dump.
	expectedPhrases := []string{
		"Run mode",
		"One-shot commands",
		"mutually exclusive",
		"Help:",
	}
	for _, phrase := range expectedPhrases {
		if !strings.Contains(text, phrase) {
			t.Errorf("helpText missing phrase %q", phrase)
		}
	}
}

// TestHelpText_MentionsFormidableFirstIsInitOnly guards the most subtle
// rule — that -formidable-first only takes effect alongside -init — by
// asserting the help copy spells it out. If someone renames the flag or
// relaxes the rule, the help and the validator must move together.
func TestHelpText_MentionsFormidableFirstIsInitOnly(t *testing.T) {
	text := helpText()
	// The subflag line is deliberately indented under -init; assert that
	// -formidable-first appears AFTER -init so the subordination is
	// visible at a glance.
	initIdx := strings.Index(text, "-init")
	subIdx := strings.Index(text, "-formidable-first")
	if initIdx == -1 || subIdx == -1 {
		t.Fatalf("help missing -init or -formidable-first")
	}
	if subIdx < initIdx {
		t.Errorf("-formidable-first should be documented after -init, indices: init=%d sub=%d", initIdx, subIdx)
	}
	if !strings.Contains(text, "With -init") {
		t.Error("help should say -formidable-first requires -init (phrase: 'With -init')")
	}
}
