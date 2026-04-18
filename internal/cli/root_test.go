package cli

import (
	"path/filepath"
	"testing"

	"github.com/petervdpas/GiGot/internal/config"
)

// TestWriteInitConfig covers both --init and --init-formidable payloads:
// formidable_first must flip on exactly when requested, and every other
// default must survive the write so operators who pick formidable mode
// aren't silently losing unrelated defaults.
func TestWriteInitConfig(t *testing.T) {
	cases := []struct {
		name               string
		formidableFirst    bool
		wantFormidableFlag bool
	}{
		{"plain init writes formidable_first=false", false, false},
		{"formidable init writes formidable_first=true", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gigot.json")
			if err := writeInitConfig(path, c.formidableFirst); err != nil {
				t.Fatalf("writeInitConfig: %v", err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Server.FormidableFirst != c.wantFormidableFlag {
				t.Errorf("FormidableFirst = %v, want %v",
					cfg.Server.FormidableFirst, c.wantFormidableFlag)
			}
			// Non-path defaults unrelated to the flag must survive —
			// guards against a future refactor where flipping
			// formidable-first accidentally drops other fields.
			// (repo_root etc. get resolved to absolute paths by Load,
			// so we only check values Load doesn't rewrite.)
			defaults := config.Defaults()
			if cfg.Server.Host != defaults.Server.Host {
				t.Errorf("host = %q, want %q (default)", cfg.Server.Host, defaults.Server.Host)
			}
			if cfg.Server.Port != defaults.Server.Port {
				t.Errorf("port = %d, want %d (default)", cfg.Server.Port, defaults.Server.Port)
			}
			if cfg.Logging.Level != defaults.Logging.Level {
				t.Errorf("logging level = %q, want %q (default)", cfg.Logging.Level, defaults.Logging.Level)
			}
		})
	}
}

func TestCleanPasswordInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "secret", "secret"},
		{"trailing newline from piped stdin", "secret\n", "secret"},
		{"trailing CRLF from Windows/paste", "secret\r\n", "secret"},
		{"trailing carriage return only", "secret\r", "secret"},
		{"trailing spaces", "secret   ", "secret"},
		{"trailing tab", "secret\t", "secret"},
		{"mixed trailing whitespace", "secret \t\r\n", "secret"},
		{"leading whitespace preserved", "  secret", "  secret"},
		{"internal whitespace preserved", "two words", "two words"},
		{"empty input", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cleanPasswordInput(c.input)
			if got != c.want {
				t.Fatalf("cleanPasswordInput(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestCleanPasswordInput_ConfirmMatch guards against the original bug:
// a trailing CR silently smuggled into one of the two prompts produced
// "passwords do not match" even though the user typed the same characters
// both times.
func TestCleanPasswordInput_ConfirmMatch(t *testing.T) {
	first := cleanPasswordInput("hunter2")       // TTY branch output
	second := cleanPasswordInput("hunter2\r\n") // piped/paste output
	if first != second {
		t.Fatalf("inputs should normalise to equal strings, got %q vs %q", first, second)
	}
}
