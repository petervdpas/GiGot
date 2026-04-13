package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
	"github.com/petervdpas/GiGot/internal/config"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/server"
)

type testContext struct {
	tmpDir     string
	configPath string
	cfg        *config.Config
	srv        *server.Server
	ts         *httptest.Server
	git        *gitmanager.Manager
	resp       *http.Response
	respBody   string
	repoList   []string
	lastErr    error
}

func (tc *testContext) reset() {
	if tc.ts != nil {
		tc.ts.Close()
		tc.ts = nil
	}
	tc.tmpDir = ""
	tc.configPath = ""
	tc.cfg = nil
	tc.srv = nil
	tc.git = nil
	tc.resp = nil
	tc.respBody = ""
	tc.repoList = nil
	tc.lastErr = nil
}

// --- Server steps ---

func (tc *testContext) theServerIsRunning() error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-test-*")
	cfg := config.Defaults()
	cfg.Storage.RepoRoot = filepath.Join(tc.tmpDir, "repos")
	os.MkdirAll(cfg.Storage.RepoRoot, 0755)
	tc.cfg = cfg
	tc.git = gitmanager.NewManager(cfg.Storage.RepoRoot)
	tc.srv = server.New(cfg)
	tc.ts = httptest.NewServer(tc.srv.Handler())
	return nil
}

func (tc *testContext) iRequest(path string) error {
	resp, err := http.Get(tc.ts.URL + path)
	if err != nil {
		return err
	}
	tc.resp = resp
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	tc.respBody = string(body)
	return nil
}

func (tc *testContext) theResponseStatusShouldBe(code int) error {
	if tc.resp.StatusCode != code {
		return fmt.Errorf("expected status %d, got %d", code, tc.resp.StatusCode)
	}
	return nil
}

func (tc *testContext) theResponseShouldContainJSONKeyWithValue(key, value string) error {
	var body map[string]string
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("response is not valid JSON: %w", err)
	}
	if body[key] != value {
		return fmt.Errorf("expected %s=%s, got %s", key, value, body[key])
	}
	return nil
}

func (tc *testContext) theResponseContentTypeShouldContain(ct string) error {
	got := tc.resp.Header.Get("Content-Type")
	if got == "" || !contains(got, ct) {
		return fmt.Errorf("expected Content-Type containing %q, got %q", ct, got)
	}
	return nil
}

func (tc *testContext) theResponseBodyShouldContain(text string) error {
	if !contains(tc.respBody, text) {
		return fmt.Errorf("expected body to contain %q", text)
	}
	return nil
}

func (tc *testContext) aRepositoryExists(name string) error {
	return tc.git.InitBare(name)
}

// --- Config steps ---

func (tc *testContext) noConfigFileExists() error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-cfg-test-*")
	tc.configPath = filepath.Join(tc.tmpDir, "gigot.json")
	return nil
}

func (tc *testContext) aConfigFileWithPort(port int) error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-cfg-test-*")
	tc.configPath = filepath.Join(tc.tmpDir, "gigot.json")
	data := fmt.Sprintf(`{"server": {"port": %d}}`, port)
	return os.WriteFile(tc.configPath, []byte(data), 0644)
}

func (tc *testContext) aConfigFileWithOnlyLoggingLevel(level string) error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-cfg-test-*")
	tc.configPath = filepath.Join(tc.tmpDir, "gigot.json")
	data := fmt.Sprintf(`{"logging": {"level": "%s"}}`, level)
	return os.WriteFile(tc.configPath, []byte(data), 0644)
}

func (tc *testContext) theConfigIsLoaded() error {
	path := tc.configPath
	// If the config file doesn't exist, load with empty path to get defaults.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = ""
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	tc.cfg = cfg
	return nil
}

func (tc *testContext) theServerPortShouldBe(port int) error {
	if tc.cfg.Server.Port != port {
		return fmt.Errorf("expected port %d, got %d", port, tc.cfg.Server.Port)
	}
	return nil
}

func (tc *testContext) theRepoRootShouldBe(root string) error {
	if tc.cfg.Storage.RepoRoot != root {
		return fmt.Errorf("expected repo root %q, got %q", root, tc.cfg.Storage.RepoRoot)
	}
	return nil
}

func (tc *testContext) theLoggingLevelShouldBe(level string) error {
	if tc.cfg.Logging.Level != level {
		return fmt.Errorf("expected logging level %q, got %q", level, tc.cfg.Logging.Level)
	}
	return nil
}

func (tc *testContext) iGenerateADefaultConfig() error {
	cfg := config.Defaults()
	tc.configPath = filepath.Join(tc.tmpDir, "gigot.json")
	return cfg.Save(tc.configPath)
}

func (tc *testContext) aFileShouldExist(filename string) error {
	path := filepath.Join(tc.tmpDir, filename)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("expected %s to exist: %w", filename, err)
	}
	return nil
}

func (tc *testContext) loadingThatConfigShouldHavePort(port int) error {
	cfg, err := config.Load(tc.configPath)
	if err != nil {
		return err
	}
	if cfg.Server.Port != port {
		return fmt.Errorf("expected port %d, got %d", port, cfg.Server.Port)
	}
	return nil
}

// --- Repo management steps ---

func (tc *testContext) anEmptyRepoRoot() error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-repo-test-*")
	tc.git = gitmanager.NewManager(tc.tmpDir)
	return nil
}

func (tc *testContext) iCreateRepository(name string) error {
	return tc.git.InitBare(name)
}

func (tc *testContext) iTryToCreateRepositoryAgain(name string) error {
	tc.lastErr = tc.git.InitBare(name)
	return nil
}

func (tc *testContext) itShouldFailWithADuplicateError() error {
	if tc.lastErr == nil {
		return fmt.Errorf("expected an error, got nil")
	}
	if !contains(tc.lastErr.Error(), "already exists") {
		return fmt.Errorf("expected 'already exists' error, got: %v", tc.lastErr)
	}
	return nil
}

func (tc *testContext) theRepositoryShouldExist(name string) error {
	if !tc.git.Exists(name) {
		return fmt.Errorf("expected repository %q to exist", name)
	}
	return nil
}

func (tc *testContext) iListAllRepositories() error {
	repos, err := tc.git.List()
	if err != nil {
		return err
	}
	tc.repoList = repos
	return nil
}

func (tc *testContext) thereShouldBeNRepositories(n int) error {
	if len(tc.repoList) != n {
		return fmt.Errorf("expected %d repos, got %d", n, len(tc.repoList))
	}
	return nil
}

func (tc *testContext) theListShouldContain(name string) error {
	for _, r := range tc.repoList {
		if r == name {
			return nil
		}
	}
	return fmt.Errorf("expected list to contain %q, got %v", name, tc.repoList)
}

func (tc *testContext) aPlainDirectoryExistsInTheRepoRoot(name string) error {
	return os.MkdirAll(filepath.Join(tc.tmpDir, name), 0755)
}

// --- Helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	tc := &testContext{}

	ctx.BeforeScenario(func(sc *godog.Scenario) {
		tc.reset()
	})

	ctx.AfterScenario(func(sc *godog.Scenario, err error) {
		if tc.ts != nil {
			tc.ts.Close()
		}
		if tc.tmpDir != "" {
			os.RemoveAll(tc.tmpDir)
		}
	})

	// Server steps
	ctx.Step(`^the server is running$`, tc.theServerIsRunning)
	ctx.Step(`^I request "([^"]*)"$`, tc.iRequest)
	ctx.Step(`^the response status should be (\d+)$`, tc.theResponseStatusShouldBe)
	ctx.Step(`^the response should contain JSON key "([^"]*)" with value "([^"]*)"$`, tc.theResponseShouldContainJSONKeyWithValue)
	ctx.Step(`^the response content type should contain "([^"]*)"$`, tc.theResponseContentTypeShouldContain)
	ctx.Step(`^the response body should contain "([^"]*)"$`, tc.theResponseBodyShouldContain)
	ctx.Step(`^a repository "([^"]*)" exists$`, tc.aRepositoryExists)

	// Config steps
	ctx.Step(`^no config file exists$`, tc.noConfigFileExists)
	ctx.Step(`^a config file with port (\d+)$`, tc.aConfigFileWithPort)
	ctx.Step(`^a config file with only logging level "([^"]*)"$`, tc.aConfigFileWithOnlyLoggingLevel)
	ctx.Step(`^the config is loaded$`, tc.theConfigIsLoaded)
	ctx.Step(`^the server port should be (\d+)$`, tc.theServerPortShouldBe)
	ctx.Step(`^the repo root should be "([^"]*)"$`, tc.theRepoRootShouldBe)
	ctx.Step(`^the logging level should be "([^"]*)"$`, tc.theLoggingLevelShouldBe)
	ctx.Step(`^I generate a default config$`, tc.iGenerateADefaultConfig)
	ctx.Step(`^a "([^"]*)" file should exist$`, tc.aFileShouldExist)
	ctx.Step(`^loading that config should have port (\d+)$`, tc.loadingThatConfigShouldHavePort)

	// Repo steps
	ctx.Step(`^an empty repo root$`, tc.anEmptyRepoRoot)
	ctx.Step(`^I create repository "([^"]*)"$`, tc.iCreateRepository)
	ctx.Step(`^I try to create repository "([^"]*)" again$`, tc.iTryToCreateRepositoryAgain)
	ctx.Step(`^it should fail with a duplicate error$`, tc.itShouldFailWithADuplicateError)
	ctx.Step(`^the repository "([^"]*)" should exist$`, tc.theRepositoryShouldExist)
	ctx.Step(`^I list all repositories$`, tc.iListAllRepositories)
	ctx.Step(`^there should be (\d+) repositories$`, tc.thereShouldBeNRepositories)
	ctx.Step(`^the list should contain "([^"]*)"$`, tc.theListShouldContain)
	ctx.Step(`^a plain directory "([^"]*)" exists in the repo root$`, tc.aPlainDirectoryExistsInTheRepoRoot)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("integration tests failed")
	}
}
