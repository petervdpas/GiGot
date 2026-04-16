package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"net/http/cookiejar"
	"os/exec"

	"github.com/cucumber/godog"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/config"
	"github.com/petervdpas/GiGot/internal/crypto"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
	"github.com/petervdpas/GiGot/internal/server"
)

type testKeypair struct {
	Priv crypto.Key
	Pub  crypto.Key
}

type testContext struct {
	tmpDir        string
	configPath    string
	cfg           *config.Config
	srv           *server.Server
	ts            *httptest.Server
	git           *gitmanager.Manager
	tokenStrategy *auth.TokenStrategy
	client        *http.Client
	currentToken  string
	savedValues   map[string]string
	keypairs      map[string]*testKeypair
	resp          *http.Response
	respBody      string
	repoList      []string
	lastErr       error
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
	tc.tokenStrategy = nil
	tc.currentToken = ""
	tc.savedValues = make(map[string]string)
	tc.keypairs = make(map[string]*testKeypair)
	jar, _ := cookiejar.New(nil)
	tc.client = &http.Client{Jar: jar}
	tc.resp = nil
	tc.respBody = ""
	tc.repoList = nil
	tc.lastErr = nil
}

// --- Server steps ---

func (tc *testContext) theServerIsRunning() error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-test-*")
	cfg := configInTempDir(tc.tmpDir)
	os.MkdirAll(cfg.Storage.RepoRoot, 0755)
	tc.cfg = cfg
	tc.git = gitmanager.NewManager(cfg.Storage.RepoRoot)
	tc.srv = server.New(cfg)
	tc.ts = httptest.NewServer(tc.srv.Handler())
	return nil
}

func configInTempDir(dir string) *config.Config {
	cfg := config.Defaults()
	cfg.Storage.RepoRoot = filepath.Join(dir, "repos")
	cfg.Crypto.PrivateKeyPath = filepath.Join(dir, "server.key")
	cfg.Crypto.PublicKeyPath = filepath.Join(dir, "server.pub")
	cfg.Crypto.DataDir = filepath.Join(dir, "data")
	return cfg
}

func (tc *testContext) iRequest(path string) error {
	resp, err := tc.client.Get(tc.ts.URL + path)
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

func (tc *testContext) theRepositoryHasCommits(name string, expected string) error {
	path := tc.git.RepoPath(name)
	out, err := exec.Command("git", "-C", path, "rev-list", "--all", "--max-count=1").Output()
	if err != nil {
		return fmt.Errorf("rev-list %s: %w", name, err)
	}
	has := len(out) > 0
	want := expected == "has commits"
	if has != want {
		return fmt.Errorf("repo %q has-commits = %v, want %v", name, has, want)
	}
	return nil
}

func (tc *testContext) theRepositoryContainsFile(repo, file string) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "ls-tree", "-r", "HEAD", "--name-only").Output()
	if err != nil {
		return fmt.Errorf("ls-tree %s: %w", repo, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == file {
			return nil
		}
	}
	return fmt.Errorf("repo %q does not contain %q (tree: %s)", repo, file, string(out))
}

func (tc *testContext) theRepositoryFileIsJSONWithField(repo, file, key, want string) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "show", "HEAD:"+file).Output()
	if err != nil {
		return fmt.Errorf("show HEAD:%s in %q: %w", file, repo, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		return fmt.Errorf("file %q in %q is not valid JSON: %v (raw: %s)", file, repo, err, string(out))
	}
	got, ok := decoded[key]
	if !ok {
		return fmt.Errorf("file %q in %q: key %q missing (keys: %v)", file, repo, key, keysOfAny(decoded))
	}
	if fmt.Sprintf("%v", got) != want {
		return fmt.Errorf("file %q in %q: field %q = %v, want %q", file, repo, key, got, want)
	}
	return nil
}

func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (tc *testContext) theRepositoryHeadCommitAuthor(repo, author string) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "log", "-1", "--pretty=format:%an").Output()
	if err != nil {
		return fmt.Errorf("log %s: %w", repo, err)
	}
	got := strings.TrimSpace(string(out))
	if got != author {
		return fmt.Errorf("repo %q head author = %q, want %q", repo, got, author)
	}
	return nil
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
	cfg := configInTempDir(tc.tmpDir)
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

// --- Auth steps ---

func (tc *testContext) startServerWithAuth(enabled bool) error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-test-*")
	cfg := configInTempDir(tc.tmpDir)
	cfg.Auth.Enabled = enabled
	os.MkdirAll(cfg.Storage.RepoRoot, 0755)
	tc.cfg = cfg
	tc.git = gitmanager.NewManager(cfg.Storage.RepoRoot)
	tc.srv = server.New(cfg)
	tc.tokenStrategy = tc.srv.TokenStrategy()
	tc.ts = httptest.NewServer(tc.srv.Handler())
	return nil
}

func (tc *testContext) theServerIsRunningWithAuthDisabled() error {
	return tc.startServerWithAuth(false)
}

func (tc *testContext) theServerIsRunningWithAuthEnabled() error {
	return tc.startServerWithAuth(true)
}

func (tc *testContext) theServerKeypairIsRotated() error {
	if tc.ts == nil {
		return fmt.Errorf("server must be running before rotation")
	}
	// Rotation must happen while the server is stopped — match production.
	tc.ts.Close()
	tc.ts = nil
	_, err := crypto.Rotate(
		tc.cfg.Crypto.PrivateKeyPath,
		tc.cfg.Crypto.PublicKeyPath,
		crypto.DefaultSealedFiles(tc.cfg.Crypto.DataDir),
	)
	return err
}

func (tc *testContext) theJSONResponseShouldDifferFromSaved(key, saveKey string) error {
	saved, ok := tc.savedValues[saveKey]
	if !ok {
		return fmt.Errorf("no saved value for %q", saveKey)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, _ := body[key].(string)
	if val == saved {
		return fmt.Errorf("expected %s to differ from saved %q, but it matched", key, saveKey)
	}
	return nil
}

func (tc *testContext) thePolicyIsDenyAll() error {
	if tc.srv == nil {
		return fmt.Errorf("server must be running")
	}
	tc.srv.SetPolicy(policy.DenyAll{})
	return nil
}

func (tc *testContext) aTokenIsIssuedForUser(username string) error {
	token, err := tc.tokenStrategy.Issue(username, nil)
	if err != nil {
		return err
	}
	tc.currentToken = token
	return nil
}

func (tc *testContext) aTokenIsIssuedForUserWithRepos(username, reposCSV string) error {
	var repos []string
	for _, r := range strings.Split(reposCSV, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			repos = append(repos, r)
		}
	}
	token, err := tc.tokenStrategy.Issue(username, repos)
	if err != nil {
		return err
	}
	tc.currentToken = token
	return nil
}

func (tc *testContext) adminRescopesThatTokenTo(reposCSV string) error {
	var repos []string
	for _, r := range strings.Split(reposCSV, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			repos = append(repos, r)
		}
	}
	return tc.tokenStrategy.UpdateRepos(tc.currentToken, repos)
}

func (tc *testContext) iRequestWithoutAToken(path string) error {
	resp, err := tc.client.Get(tc.ts.URL + path)
	if err != nil {
		return err
	}
	tc.resp = resp
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tc.respBody = string(body)
	return nil
}

func (tc *testContext) iRequestWithThatToken(path string) error {
	return tc.requestWithToken(path, tc.currentToken)
}

func (tc *testContext) iRequestWithSavedToken(path, saveKey string) error {
	token, ok := tc.savedValues[saveKey]
	if !ok {
		return fmt.Errorf("no saved value for %q", saveKey)
	}
	return tc.requestWithToken(path, token)
}

func (tc *testContext) iRequestWithToken(path, token string) error {
	return tc.requestWithToken(path, token)
}

func (tc *testContext) requestWithToken(path, token string) error {
	req, err := http.NewRequest(http.MethodGet, tc.ts.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := tc.client.Do(req)
	if err != nil {
		return err
	}
	tc.resp = resp
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tc.respBody = string(body)
	return nil
}

func (tc *testContext) thatTokenIsRevoked() error {
	tc.tokenStrategy.Revoke(tc.currentToken)
	return nil
}

// --- API steps ---

func (tc *testContext) iGET(path string) error {
	return tc.doRequest(http.MethodGet, path, "")
}

func (tc *testContext) iPOSTWithBody(path, body string) error {
	return tc.doRequest(http.MethodPost, path, body)
}

// iPUTWithBody sends a PUT with JSON body. Tokens of the form ${key} in the
// body are expanded from the savedValues map, so scenarios can chain a GET
// /head → save → PUT cycle without hardcoding SHAs.
func (tc *testContext) iPUTWithBody(path, body string) error {
	return tc.doRequest(http.MethodPut, path, tc.expandSaved(body))
}

func (tc *testContext) expandSaved(s string) string {
	for k, v := range tc.savedValues {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}

func (tc *testContext) iDELETE(path string) error {
	return tc.doRequest(http.MethodDelete, path, "")
}

func (tc *testContext) iDELETEWithSavedToken(path, key string) error {
	token, ok := tc.savedValues[key]
	if !ok {
		return fmt.Errorf("no saved value for key %q", key)
	}
	body := fmt.Sprintf(`{"token":"%s"}`, token)
	return tc.doRequest(http.MethodDelete, path, body)
}

func (tc *testContext) doRequest(method, path, body string) error {
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, tc.ts.URL+path, bytes.NewBufferString(body))
	} else {
		req, err = http.NewRequest(method, tc.ts.URL+path, nil)
	}
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := tc.client.Do(req)
	if err != nil {
		return err
	}
	tc.resp = resp
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tc.respBody = string(respBody)
	return nil
}

func (tc *testContext) theJSONResponseShouldBe(key string, expected int) error {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not found in response", key)
	}
	// JSON numbers are float64.
	num, ok := val.(float64)
	if !ok {
		return fmt.Errorf("expected number for %q, got %T", key, val)
	}
	if int(num) != expected {
		return fmt.Errorf("expected %s=%d, got %v", key, expected, num)
	}
	return nil
}

func (tc *testContext) theJSONResponseStringShouldBe(key, expected string) error {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not found in response", key)
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("expected string for %q, got %T", key, val)
	}
	if str != expected {
		return fmt.Errorf("expected %s=%q, got %q", key, expected, str)
	}
	return nil
}

func (tc *testContext) theJSONResponseShouldNotBeEmpty(key string) error {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not found in response", key)
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("expected string for %q, got %T", key, val)
	}
	if str == "" {
		return fmt.Errorf("expected %q to be non-empty", key)
	}
	return nil
}

func (tc *testContext) theServerRestarts() error {
	return tc.restartServer(tc.cfg.Auth.Enabled)
}

func (tc *testContext) theServerRestartsWithAuth(onOff string) error {
	return tc.restartServer(onOff == "enabled")
}

func (tc *testContext) restartServer(authEnabled bool) error {
	if tc.cfg == nil {
		return fmt.Errorf("server was never started")
	}
	if tc.ts != nil {
		tc.ts.Close()
	}
	// Reuse the same tmpDir so on-disk state (keypair, data dir) persists.
	cfg := configInTempDir(tc.tmpDir)
	cfg.Auth.Enabled = authEnabled
	tc.cfg = cfg
	tc.git = gitmanager.NewManager(cfg.Storage.RepoRoot)
	tc.srv = server.New(cfg)
	tc.tokenStrategy = tc.srv.TokenStrategy()
	tc.ts = httptest.NewServer(tc.srv.Handler())
	return nil
}

func (tc *testContext) theJSONResponseShouldEqualSaved(key, saveKey string) error {
	saved, ok := tc.savedValues[saveKey]
	if !ok {
		return fmt.Errorf("no saved value for %q", saveKey)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not found in response", key)
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("expected string for %q, got %T", key, val)
	}
	if str != saved {
		return fmt.Errorf("expected %s=%q (saved as %s), got %q", key, saved, saveKey, str)
	}
	return nil
}

func (tc *testContext) anAdminExistsWithPassword(username, password string) error {
	if tc.srv == nil {
		return fmt.Errorf("server must be running")
	}
	_, err := tc.srv.Admins().Put(username, password)
	return err
}

func (tc *testContext) iLogInAsAdminWithPassword(username, password string) error {
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	return tc.doRequest(http.MethodPost, "/admin/login", body)
}

func (tc *testContext) theCurrentResponseSetsSessionCookie() error {
	if tc.resp == nil {
		return fmt.Errorf("no response")
	}
	for _, c := range tc.resp.Cookies() {
		if c.Name == "gigot_session" && c.Value != "" {
			return nil
		}
	}
	return fmt.Errorf("no gigot_session cookie in response")
}

func (tc *testContext) aFreshClientKeypair(name string) error {
	priv, pub, err := crypto.GenerateKeyPair()
	if err != nil {
		return err
	}
	tc.keypairs[name] = &testKeypair{Priv: priv, Pub: pub}
	return nil
}

func (tc *testContext) iEnrollClientWithKeypair(clientID, kpName string) error {
	kp, ok := tc.keypairs[kpName]
	if !ok {
		return fmt.Errorf("unknown keypair %q", kpName)
	}
	body := fmt.Sprintf(`{"client_id":%q,"public_key":%q}`, clientID, kp.Pub.Encode())
	return tc.doRequest(http.MethodPost, "/api/clients/enroll", body)
}

// --- Sealed request helpers ---

func (tc *testContext) serverPubKey() (crypto.Key, error) {
	resp, err := http.Get(tc.ts.URL + "/api/crypto/pubkey")
	if err != nil {
		return crypto.Key{}, err
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return crypto.Key{}, err
	}
	return crypto.DecodeKey(body["public_key"])
}

func (tc *testContext) clientPOSTsSealed(clientID, kpName, path, plaintext string) error {
	kp, ok := tc.keypairs[kpName]
	if !ok {
		return fmt.Errorf("unknown keypair %q", kpName)
	}
	serverPub, err := tc.serverPubKey()
	if err != nil {
		return err
	}
	enc, err := crypto.New(kp.Priv)
	if err != nil {
		return err
	}
	sealed, err := enc.SealString(serverPub, []byte(plaintext))
	if err != nil {
		return err
	}
	return tc.sendSealed(clientID, path, sealed)
}

func (tc *testContext) clientPOSTsRawSealed(clientID, path, raw string) error {
	return tc.sendSealed(clientID, path, raw)
}

func (tc *testContext) sendSealed(clientID, path, body string) error {
	req, err := http.NewRequest(http.MethodPost, tc.ts.URL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.gigot.sealed+b64")
	req.Header.Set("X-Client-Id", clientID)
	resp, err := tc.client.Do(req)
	if err != nil {
		return err
	}
	tc.resp = resp
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tc.respBody = string(data)
	return nil
}

func (tc *testContext) openingResponseGivesJSONKeyEquals(kpName, key, expected string) error {
	kp, ok := tc.keypairs[kpName]
	if !ok {
		return fmt.Errorf("unknown keypair %q", kpName)
	}
	serverPub, err := tc.serverPubKey()
	if err != nil {
		return err
	}
	enc, err := crypto.New(kp.Priv)
	if err != nil {
		return err
	}
	plain, err := enc.OpenString(serverPub, strings.TrimSpace(tc.respBody))
	if err != nil {
		return fmt.Errorf("opening sealed response: %w (body=%q)", err, tc.respBody)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(plain, &body); err != nil {
		return fmt.Errorf("response plaintext is not JSON: %w (body=%q)", err, plain)
	}
	got, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not in response", key)
	}
	str, ok := got.(string)
	if !ok {
		return fmt.Errorf("expected string for %q, got %T", key, got)
	}
	if str != expected {
		return fmt.Errorf("expected %s=%q, got %q", key, expected, str)
	}
	return nil
}

func (tc *testContext) iSaveTheJSONResponseAs(key, saveKey string) error {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not found in response", key)
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("expected string for %q, got %T", key, val)
	}
	tc.savedValues[saveKey] = str
	return nil
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
	ctx.Step(`^the repository "([^"]*)" (has commits|has no commits)$`, tc.theRepositoryHasCommits)
	ctx.Step(`^the repository "([^"]*)" contains file "([^"]*)"$`, tc.theRepositoryContainsFile)
	ctx.Step(`^the repository "([^"]*)" file "([^"]*)" is valid JSON with field "([^"]*)" equal to "([^"]*)"$`, tc.theRepositoryFileIsJSONWithField)
	ctx.Step(`^the repository "([^"]*)" head commit is authored by "([^"]*)"$`, tc.theRepositoryHeadCommitAuthor)

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

	// Auth steps
	ctx.Step(`^the server is running with auth disabled$`, tc.theServerIsRunningWithAuthDisabled)
	ctx.Step(`^the server is running with auth enabled$`, tc.theServerIsRunningWithAuthEnabled)
	ctx.Step(`^a token is issued for user "([^"]*)"$`, tc.aTokenIsIssuedForUser)
	ctx.Step(`^a token is issued for user "([^"]*)" with repos "([^"]*)"$`, tc.aTokenIsIssuedForUserWithRepos)
	ctx.Step(`^the admin rescopes that token to "([^"]*)"$`, tc.adminRescopesThatTokenTo)
	ctx.Step(`^the policy is deny-all$`, tc.thePolicyIsDenyAll)
	ctx.Step(`^the server keypair is rotated$`, tc.theServerKeypairIsRotated)
	ctx.Step(`^the JSON response "([^"]*)" should differ from saved "([^"]*)"$`, tc.theJSONResponseShouldDifferFromSaved)
	ctx.Step(`^I request "([^"]*)" without a token$`, tc.iRequestWithoutAToken)
	ctx.Step(`^I request "([^"]*)" with that token$`, tc.iRequestWithThatToken)
	ctx.Step(`^I request "([^"]*)" with token "([^"]*)"$`, tc.iRequestWithToken)
	ctx.Step(`^I request "([^"]*)" with saved token "([^"]*)"$`, tc.iRequestWithSavedToken)
	ctx.Step(`^that token is revoked$`, tc.thatTokenIsRevoked)

	// API steps
	ctx.Step(`^I GET "([^"]*)"$`, tc.iGET)
	ctx.Step(`^I POST "([^"]*)" with body '([^']*)'$`, tc.iPOSTWithBody)
	ctx.Step(`^I PUT "([^"]*)" with body '([^']*)'$`, tc.iPUTWithBody)
	ctx.Step(`^I DELETE "([^"]*)"$`, tc.iDELETE)
	ctx.Step(`^I DELETE "([^"]*)" with saved token "([^"]*)"$`, tc.iDELETEWithSavedToken)
	ctx.Step(`^the JSON response "([^"]*)" should be (\d+)$`, tc.theJSONResponseShouldBe)
	ctx.Step(`^the JSON response "([^"]*)" should be "([^"]*)"$`, tc.theJSONResponseStringShouldBe)
	ctx.Step(`^the JSON response "([^"]*)" should not be empty$`, tc.theJSONResponseShouldNotBeEmpty)
	ctx.Step(`^I save the JSON response "([^"]*)" as "([^"]*)"$`, tc.iSaveTheJSONResponseAs)
	ctx.Step(`^the JSON response "([^"]*)" should equal saved "([^"]*)"$`, tc.theJSONResponseShouldEqualSaved)
	ctx.Step(`^the server restarts$`, tc.theServerRestarts)
	ctx.Step(`^the server restarts with auth (enabled|disabled)$`, tc.theServerRestartsWithAuth)

	// Client enrollment steps
	ctx.Step(`^a fresh client keypair "([^"]*)"$`, tc.aFreshClientKeypair)
	ctx.Step(`^I enroll client "([^"]*)" with keypair "([^"]*)"$`, tc.iEnrollClientWithKeypair)

	// Admin steps
	ctx.Step(`^an admin "([^"]*)" exists with password "([^"]*)"$`, tc.anAdminExistsWithPassword)
	ctx.Step(`^I log in as admin "([^"]*)" with password "([^"]*)"$`, tc.iLogInAsAdminWithPassword)
	ctx.Step(`^the response sets a session cookie$`, tc.theCurrentResponseSetsSessionCookie)

	// Sealed body steps
	ctx.Step(`^client "([^"]*)" with keypair "([^"]*)" POSTs sealed "([^"]*)" with body '([^']*)'$`, tc.clientPOSTsSealed)
	ctx.Step(`^client "([^"]*)" POSTs "([^"]*)" with raw sealed body "([^"]*)"$`, tc.clientPOSTsRawSealed)
	ctx.Step(`^opening the response with keypair "([^"]*)" gives JSON with "([^"]*)" equal to "([^"]*)"$`, tc.openingResponseGivesJSONKeyEquals)
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
