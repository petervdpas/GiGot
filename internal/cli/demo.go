package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/petervdpas/GiGot/internal/admins"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/crypto"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/scaffold"
)

// Demo constants drive both -add-demo-setup and -remove-demo-setup. They
// also shape the defaults in docs/postman/GiGot.local.postman_environment.json
// so the Postman collection runs top-to-bottom against a freshly-provisioned
// server with zero manual env edits beyond pasting the printed token.
const (
	DemoAdminUser      = "demo"
	DemoAdminPassword  = "demo-password"
	DemoRepoName       = "postman-demo"
	DemoCredentialName = "postman-pat"
	DemoCredentialKind = "github_pat"
	demoCredentialBody = "ghp_demo_placeholder_not_a_real_token"
	demoTokenUsername  = "demo"
)

// demoStores bundles the handful of on-disk stores the demo flow touches.
// Opening them directly (rather than via server.New) keeps the demo
// commands out of the HTTP-handler dependency graph — they're strictly
// data-plane operations on the sealed files.
type demoStores struct {
	admins      *admins.Store
	credentials *credentials.Store
	tokens      *auth.TokenStrategy
	git         *gitmanager.Manager
}

func openDemoStores(cfg *config.Config) (*demoStores, error) {
	enc, _, err := crypto.LoadOrGenerate(cfg.Crypto.PrivateKeyPath, cfg.Crypto.PublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load keypair: %w", err)
	}
	adminStore, err := admins.Open(filepath.Join(cfg.Crypto.DataDir, "admins.enc"), enc)
	if err != nil {
		return nil, fmt.Errorf("open admins: %w", err)
	}
	credStore, err := credentials.Open(filepath.Join(cfg.Crypto.DataDir, "credentials.enc"), enc)
	if err != nil {
		return nil, fmt.Errorf("open credentials: %w", err)
	}
	tokenStore, err := auth.NewSealedTokenStore(filepath.Join(cfg.Crypto.DataDir, "tokens.enc"), enc)
	if err != nil {
		return nil, fmt.Errorf("open tokens: %w", err)
	}
	tokenStrategy := auth.NewTokenStrategy()
	if err := tokenStrategy.SetPersister(tokenStore); err != nil {
		return nil, fmt.Errorf("attach token persister: %w", err)
	}
	return &demoStores{
		admins:      adminStore,
		credentials: credStore,
		tokens:      tokenStrategy,
		git:         gitmanager.NewManager(cfg.Storage.RepoRoot),
	}, nil
}

// runAddDemoSetup provisions everything the Postman collection expects:
// admin account, scaffolded repo, dummy credential, and one fresh
// subscription token. Re-running rotates the token and restores the
// admin password to the documented default — intentional, so a repeat
// invocation gives the operator a known-good state to work from.
func runAddDemoSetup(cfg *config.Config, stdout io.Writer) error {
	stores, err := openDemoStores(cfg)
	if err != nil {
		return err
	}

	if _, err := stores.admins.Put(DemoAdminUser, DemoAdminPassword); err != nil {
		return fmt.Errorf("provision admin: %w", err)
	}
	fmt.Fprintf(stdout, "  admin      %-16s (password: %s)\n", DemoAdminUser, DemoAdminPassword)

	if !stores.git.Exists(DemoRepoName) {
		if err := stores.git.InitBare(DemoRepoName); err != nil {
			return fmt.Errorf("init repo: %w", err)
		}
		files, err := scaffold.FormidableFiles(time.Now())
		if err != nil {
			return fmt.Errorf("build scaffold files: %w", err)
		}
		if err := stores.git.Scaffold(DemoRepoName, gitmanager.ScaffoldOptions{
			CommitterName:  scaffold.CommitterName,
			CommitterEmail: scaffold.CommitterEmail,
			Message:        scaffold.CommitMessage,
			Files:          files,
		}); err != nil {
			return fmt.Errorf("scaffold repo: %w", err)
		}
		fmt.Fprintf(stdout, "  repo       %-16s (scaffolded)\n", DemoRepoName)
	} else {
		fmt.Fprintf(stdout, "  repo       %-16s (already present, left alone)\n", DemoRepoName)
	}

	if _, err := stores.credentials.Put(credentials.Credential{
		Name:   DemoCredentialName,
		Kind:   DemoCredentialKind,
		Secret: demoCredentialBody,
		Notes:  "Provisioned by -add-demo-setup. Not a real PAT.",
	}); err != nil {
		return fmt.Errorf("provision credential: %w", err)
	}
	fmt.Fprintf(stdout, "  credential %-16s (kind: %s)\n", DemoCredentialName, DemoCredentialKind)

	token, err := stores.tokens.Issue(demoTokenUsername, []string{DemoRepoName})
	if err != nil {
		return fmt.Errorf("issue token: %w", err)
	}
	fmt.Fprintf(stdout, "  token      %s\n", token)
	fmt.Fprintf(stdout, "\nPaste the token above into the Postman environment's `subscriptionToken`.\n")
	fmt.Fprintf(stdout, "Admin user/password are already the defaults in GiGot.local.postman_environment.json.\n")
	return nil
}

// runRemoveDemoSetup reverses -add-demo-setup. Missing artefacts are
// not errors — the operator's intent is "this should not be on disk
// afterwards" and we achieve that either way. All token entries issued
// to the demo username are revoked, not just the most recent one,
// because repeat -add-demo-setup invocations accumulate them.
func runRemoveDemoSetup(cfg *config.Config, stdout io.Writer) error {
	stores, err := openDemoStores(cfg)
	if err != nil {
		return err
	}

	revoked := 0
	for _, entry := range stores.tokens.List() {
		if entry.Username == demoTokenUsername {
			if stores.tokens.Revoke(entry.Token) {
				revoked++
			}
		}
	}
	fmt.Fprintf(stdout, "  revoked %d token(s) issued to %q\n", revoked, demoTokenUsername)

	if err := stores.credentials.Remove(DemoCredentialName); err != nil {
		// credentials.ErrNotFound is the idempotent path — anything else is fatal.
		fmt.Fprintf(stdout, "  credential %s: %v (ok if already gone)\n", DemoCredentialName, err)
	} else {
		fmt.Fprintf(stdout, "  credential %s removed\n", DemoCredentialName)
	}

	if stores.git.Exists(DemoRepoName) {
		if err := stores.git.Delete(DemoRepoName); err != nil {
			return fmt.Errorf("delete repo: %w", err)
		}
		fmt.Fprintf(stdout, "  repo       %s removed\n", DemoRepoName)
	} else {
		fmt.Fprintf(stdout, "  repo       %s (already absent)\n", DemoRepoName)
	}

	if err := stores.admins.Remove(DemoAdminUser); err != nil {
		fmt.Fprintf(stdout, "  admin %s: %v (ok if already gone)\n", DemoAdminUser, err)
	} else {
		fmt.Fprintf(stdout, "  admin      %s removed\n", DemoAdminUser)
	}

	fmt.Fprintln(stdout, "\nDemo setup removed.")
	return nil
}
