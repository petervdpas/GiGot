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
		{
			name: "-wipe-admins alone selects admins target",
			args: []string{"-wipe-admins"},
			want: Options{Mode: ModeWipe, Wipe: WipeTargets{Admins: true}},
		},
		{
			name: "multiple granular wipes compose",
			args: []string{"-wipe-admins", "-wipe-tokens", "-wipe-repos"},
			want: Options{Mode: ModeWipe, Wipe: WipeTargets{Admins: true, Tokens: true, Repos: true}},
		},
		{
			name: "-wipe-admins with -yes bypasses the prompt",
			args: []string{"-wipe-admins", "-yes"},
			want: Options{Mode: ModeWipe, Wipe: WipeTargets{Admins: true}, WipeAssumeYes: true},
		},
		{
			name: "-factory-reset sets every target including keys",
			args: []string{"-factory-reset"},
			want: Options{Mode: ModeWipe, Wipe: WipeTargets{
				Repos: true, Admins: true, Tokens: true, Clients: true,
				Sessions: true, Credentials: true, Destinations: true, Keys: true,
			}},
		},
		{
			name: "-factory-reset with -yes",
			args: []string{"-factory-reset", "-yes"},
			want: Options{Mode: ModeWipe, Wipe: WipeTargets{
				Repos: true, Admins: true, Tokens: true, Clients: true,
				Sessions: true, Credentials: true, Destinations: true, Keys: true,
			}, WipeAssumeYes: true},
		},
		{
			name: "-add-demo-setup triggers provisioning mode",
			args: []string{"-add-demo-setup"},
			want: Options{Mode: ModeAddDemoSetup},
		},
		{
			name: "-remove-demo-setup triggers teardown mode",
			args: []string{"-remove-demo-setup"},
			want: Options{Mode: ModeRemoveDemoSetup},
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

func TestParse_AllowLocalOverride(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantSet  bool
		wantVal  bool
	}{
		{"unset leaves override nil", nil, false, false},
		{"-allow-local alone is true", []string{"-allow-local"}, true, true},
		{"-allow-local=true explicit", []string{"-allow-local=true"}, true, true},
		{"-allow-local=false disables", []string{"-allow-local=false"}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, err := Parse(c.args)
			if err != nil {
				t.Fatalf("Parse(%v): %v", c.args, err)
			}
			got := opts.AllowLocalOverride
			if c.wantSet {
				if got == nil {
					t.Fatalf("AllowLocalOverride nil, want pointer to %v", c.wantVal)
				}
				if *got != c.wantVal {
					t.Errorf("AllowLocalOverride = %v, want %v", *got, c.wantVal)
				}
			} else if got != nil {
				t.Errorf("AllowLocalOverride = %v, want nil", *got)
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
		{
			name:      "-factory-reset combined with a granular wipe is rejected",
			args:      []string{"-factory-reset", "-wipe-admins"},
			wantMatch: "-factory-reset already covers",
		},
		{
			name:      "-wipe-admins combined with -rotate-keys is rejected",
			args:      []string{"-wipe-admins", "-rotate-keys"},
			wantMatch: "only one of",
		},
		{
			name:      "-wipe-repos combined with -init is rejected",
			args:      []string{"-wipe-repos", "-init"},
			wantMatch: "only one of",
		},
		{
			name:      "-yes without any wipe flag is rejected",
			args:      []string{"-yes"},
			wantMatch: "-yes is only valid",
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
		"-allow-local",
		"-init",
		"-formidable-first",
		"-add-admin",
		"-rotate-keys",
		"-wipe-repos",
		"-wipe-admins",
		"-wipe-tokens",
		"-wipe-clients",
		"-wipe-sessions",
		"-wipe-credentials",
		"-wipe-destinations",
		"-factory-reset",
		"-yes",
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
