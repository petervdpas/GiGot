package integration

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/petervdpas/GiGot/internal/accounts"
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
	return tc.startServerWithFormidableFirst(false)
}

// theServerIsRunningInFormidableFirstMode boots a server with
// cfg.Server.FormidableFirst = true so scenarios can exercise the
// server-level default branch of the §2.7 marker-provisioning matrix.
func (tc *testContext) theServerIsRunningInFormidableFirstMode() error {
	return tc.startServerWithFormidableFirst(true)
}

func (tc *testContext) startServerWithFormidableFirst(first bool) error {
	tc.tmpDir, _ = os.MkdirTemp("", "gigot-test-*")
	cfg := configInTempDir(tc.tmpDir)
	cfg.Server.FormidableFirst = first
	os.MkdirAll(cfg.Storage.RepoRoot, 0755)
	tc.cfg = cfg
	tc.git = gitmanager.NewManager(cfg.Storage.RepoRoot)
	tc.srv = server.New(cfg)
	tc.tokenStrategy = tc.srv.TokenStrategy()
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
		return fmt.Errorf("expected status %d, got %d (body=%s)", code, tc.resp.StatusCode, tc.respBody)
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

func (tc *testContext) theResponseBodyShouldNotContain(text string) error {
	if contains(tc.respBody, text) {
		return fmt.Errorf("expected body NOT to contain %q, got: %s", text, tc.respBody)
	}
	return nil
}

func (tc *testContext) aRepositoryExists(name string) error {
	return tc.git.InitBare(name)
}

func (tc *testContext) theRepositoryHasCommits(name string, expected string) error {
	path := tc.git.RepoPath(name)
	// --branches scopes the check to refs/heads/* so server-owned refs like
	// refs/audit/main (GiGot writes one on repo create) don't flip the
	// "has commits" signal — the scenario cares whether user content
	// landed, not whether bookkeeping exists.
	out, err := exec.Command("git", "-C", path, "rev-list", "--branches", "--max-count=1").Output()
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

// aLocalGitSourceExists creates a non-bare git repo with one README commit
// and saves its filesystem path under the given name, so a scenario can
// reference it as ${name} inside a POST body's source_url field.
func (tc *testContext) aLocalGitSourceExists(name string) error {
	return tc.seedLocalSource(name, sourceSeedPlain)
}

// aLocalGitSourceExistsWithMarker is aLocalGitSourceExists plus a
// pre-existing .formidable/context.json commit — used to prove clone-stamp
// idempotence at the feature level.
func (tc *testContext) aLocalGitSourceExistsWithMarker(name string) error {
	return tc.seedLocalSource(name, sourceSeedValidMarker)
}

// aLocalGitSourceExistsWithBrokenMarker seeds a source whose
// .formidable/context.json is malformed (non-JSON). Used to verify the
// stamp path replaces a garbage marker with a valid one at the wire
// level — the corresponding unit/handler tests cover the inner and
// handler layers.
func (tc *testContext) aLocalGitSourceExistsWithBrokenMarker(name string) error {
	return tc.seedLocalSource(name, sourceSeedBrokenMarker)
}

type sourceSeedKind int

const (
	sourceSeedPlain sourceSeedKind = iota
	sourceSeedValidMarker
	sourceSeedBrokenMarker
)

func (tc *testContext) seedLocalSource(name string, kind sourceSeedKind) error {
	if tc.tmpDir == "" {
		var err error
		tc.tmpDir, err = os.MkdirTemp("", "gigot-test-*")
		if err != nil {
			return err
		}
	}
	dir := filepath.Join(tc.tmpDir, "sources", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		return fmt.Errorf("git init %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		return err
	}
	cmds := [][]string{
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "add", "README.md"},
		{"-C", dir, "commit", "-m", "initial"},
	}
	if kind != sourceSeedPlain {
		markerDir := filepath.Join(dir, ".formidable")
		if err := os.MkdirAll(markerDir, 0755); err != nil {
			return err
		}
		var body []byte
		var msg string
		switch kind {
		case sourceSeedValidMarker:
			body = []byte(`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}` + "\n")
			msg = "add marker"
		case sourceSeedBrokenMarker:
			body = []byte("this is not json\n")
			msg = "add broken marker"
		}
		if err := os.WriteFile(filepath.Join(markerDir, "context.json"), body, 0644); err != nil {
			return err
		}
		cmds = append(cmds,
			[][]string{
				{"-C", dir, "add", ".formidable/context.json"},
				{"-C", dir, "commit", "-m", msg},
			}...,
		)
	}
	for _, args := range cmds {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
	}
	tc.savedValues[name] = dir
	return nil
}

// theRepositoryHasExactCommits pins an exact commit count at HEAD — used
// to prove clone-stamp idempotence (source has N commits, cloned repo
// still has N, no sneaky extra stamp commit) and that the stamp path adds
// exactly one commit when it fires.
func (tc *testContext) theRepositoryHasExactCommits(repo string, want int) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("rev-list %s: %w", repo, err)
	}
	got := strings.TrimSpace(string(out))
	if got != fmt.Sprintf("%d", want) {
		return fmt.Errorf("repo %q commit count = %s, want %d", repo, got, want)
	}
	return nil
}

// theAuditRefHasEntries pins the count of commits on refs/audit/main so a
// scenario can prove that a write path wrote exactly one audit entry (no
// duplicates, no silent drops).
func (tc *testContext) theAuditRefHasEntries(repo string, want int) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "rev-list", "--count", "refs/audit/main").Output()
	if err != nil {
		// Missing ref → 0 entries, which is a legitimate answer for a repo
		// that has not received any audited operation yet.
		if want == 0 {
			return nil
		}
		return fmt.Errorf("rev-list refs/audit/main on %s: %w", repo, err)
	}
	got := strings.TrimSpace(string(out))
	if got != fmt.Sprintf("%d", want) {
		return fmt.Errorf("repo %q audit-entry count = %s, want %d", repo, got, want)
	}
	return nil
}

// theAuditTopEventIs pulls event.json at refs/audit/main and asserts the
// `type` field. Exact JSON equality is overkill — type is the discriminator
// everything else hangs off.
func (tc *testContext) theAuditTopEventIs(repo, wantType string) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "show", "refs/audit/main:event.json").Output()
	if err != nil {
		return fmt.Errorf("show refs/audit/main:event.json on %s: %w", repo, err)
	}
	var ev struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(out, &ev); err != nil {
		return fmt.Errorf("parse audit event on %s: %w", repo, err)
	}
	if ev.Type != wantType {
		return fmt.Errorf("repo %q top audit event type = %q, want %q", repo, ev.Type, wantType)
	}
	return nil
}

// aClientPushesOneCommitViaSmartHTTP drives a real `git push` against the
// httptest server so the receive-pack audit path is exercised end-to-end
// over the wire. Locks in the README roadmap item: smart-HTTP pushes are
// instrumented with a push_received audit entry.
func (tc *testContext) aClientPushesOneCommitViaSmartHTTP(repo string) error {
	work := filepath.Join(tc.tmpDir, "push-work-"+repo)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return fmt.Errorf("mkdir work: %w", err)
	}
	cloneURL := tc.ts.URL + "/git/" + repo + ".git"
	steps := [][]string{
		{"clone", cloneURL, work},
		{"-C", work, "config", "user.email", "push@example.com"},
		{"-C", work, "config", "user.name", "Push"},
		{"-C", work, "commit", "--allow-empty", "-m", "pushed-commit"},
		{"-C", work, "push", "origin", "HEAD:refs/heads/main"},
	}
	for _, args := range steps {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

func (tc *testContext) theRepositoryDoesNotContainFile(repo, file string) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "ls-tree", "-r", "HEAD", "--name-only").Output()
	if err != nil {
		return fmt.Errorf("ls-tree %s: %w", repo, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == file {
			return fmt.Errorf("repo %q should not contain %q but does", repo, file)
		}
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

// theRepositoryFileContains reads HEAD:file from the named repo and
// checks that the body contains the given substring. Kept substring-
// based (not regex) because the common case is asserting a literal
// marker/path — regex is over-scoped for scenario readability.
func (tc *testContext) theRepositoryFileContains(repo, file, want string) error {
	path := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", path, "show", "HEAD:"+file).Output()
	if err != nil {
		return fmt.Errorf("show HEAD:%s in %q: %w", file, repo, err)
	}
	if !strings.Contains(string(out), want) {
		return fmt.Errorf("file %q in %q does not contain %q; got:\n%s", file, repo, want, string(out))
	}
	return nil
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

// iPutARecord builds a minimal Formidable record JSON ({meta,data}),
// base64-encodes it, and PUTs it at the given path. The meta carries
// fixed id/template and created timestamps so scenarios only need to
// vary updated + data. Variant with an explicit created is handled by
// iPutARecordWithCreated.
func (tc *testContext) iPutARecord(path, repo, dataJSON, updated, parent string) error {
	return tc.putRecordWithCreated(path, repo, dataJSON, "2024-01-01T00:00:00Z", updated, parent)
}

func (tc *testContext) iPutARecordWithCreated(path, repo, dataJSON, created, updated, parent string) error {
	return tc.putRecordWithCreated(path, repo, dataJSON, created, updated, parent)
}

func (tc *testContext) putRecordWithCreated(path, repo, dataJSON, created, updated, parent string) error {
	var data map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return fmt.Errorf("parse data %q: %w", dataJSON, err)
	}
	rec := map[string]any{
		"meta": map[string]any{
			"id":       "fixed-id",
			"template": "addresses.yaml",
			"created":  created,
			"updated":  updated,
		},
		"data": data,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	body := map[string]any{
		"parent_version": tc.expandSaved(parent),
		"content_b64":    base64.StdEncoding.EncodeToString(raw),
		"message":        "test merge scenario",
	}
	rawBody, _ := json.Marshal(body)
	return tc.doRequest(http.MethodPut, "/api/repos/"+repo+"/files/"+path, string(rawBody))
}

// theResultingRecordHasDataField reads the file at HEAD, parses as
// {meta,data}, and asserts data[key] == want. Used to verify merged
// record content without having to decode the merge response body.
func (tc *testContext) theResultingRecordHasDataField(path, repo, key, want string) error {
	p := tc.git.RepoPath(repo)
	out, err := exec.Command("git", "-C", p, "show", "HEAD:"+path).Output()
	if err != nil {
		return fmt.Errorf("show HEAD:%s in %q: %w", path, repo, err)
	}
	var decoded struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		return fmt.Errorf("record %q is not valid JSON: %v (raw: %s)", path, err, string(out))
	}
	got, ok := decoded.Data[key]
	if !ok {
		return fmt.Errorf("data.%s missing in %q (raw: %s)", key, path, string(out))
	}
	if fmt.Sprintf("%v", got) != want {
		return fmt.Errorf("data.%s = %v, want %q", key, got, want)
	}
	return nil
}

// iPutBinaryFile builds a PUT /files/{path} body for an arbitrary
// hex-encoded binary blob and fires it. Used by F3 scenarios that
// need to prove binary transport works under
// storage/<template>/images/.
func (tc *testContext) iPutBinaryFile(path, repo, hexBody, parent string) error {
	raw, err := hex.DecodeString(hexBody)
	if err != nil {
		return fmt.Errorf("decode hex %q: %w", hexBody, err)
	}
	body := map[string]any{
		"parent_version": tc.expandSaved(parent),
		"content_b64":    base64.StdEncoding.EncodeToString(raw),
		"message":        "binary transport test",
	}
	rawBody, _ := json.Marshal(body)
	return tc.doRequest(http.MethodPut, "/api/repos/"+repo+"/files/"+path, string(rawBody))
}

// theResponseBodyBase64DecodesToHex reads a {content_b64:"..."}
// JSON payload (as returned by GET /files/{path}) and asserts the
// decoded bytes equal the given hex. Used by F3 scenarios.
func (tc *testContext) theResponseBodyBase64DecodesToHex(wantHex string) error {
	var decoded struct {
		ContentB64 string `json:"content_b64"`
	}
	if err := json.Unmarshal([]byte(tc.respBody), &decoded); err != nil {
		return fmt.Errorf("decode response: %v (body: %s)", err, tc.respBody)
	}
	raw, err := base64.StdEncoding.DecodeString(decoded.ContentB64)
	if err != nil {
		return fmt.Errorf("base64: %v", err)
	}
	got := hex.EncodeToString(raw)
	if got != wantHex {
		return fmt.Errorf("hex mismatch: got %s, want %s", got, wantHex)
	}
	return nil
}

// theRecordsResponseContainsNRecords asserts records[] length.
func (tc *testContext) theRecordsResponseContainsNRecords(n int) error {
	var decoded struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(tc.respBody), &decoded); err != nil {
		return fmt.Errorf("decode records response: %v", err)
	}
	if len(decoded.Records) != n {
		return fmt.Errorf("records count: got %d, want %d (body: %s)", len(decoded.Records), n, tc.respBody)
	}
	return nil
}

// theRecordsResponseRecordHasDataField asserts records[i].data[key]
// stringifies to want. Used by F4 scenarios to inspect the filter
// and sort result without introducing a full JSONPath step.
func (tc *testContext) theRecordsResponseRecordHasDataField(index int, key, want string) error {
	var decoded struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(tc.respBody), &decoded); err != nil {
		return fmt.Errorf("decode: %v", err)
	}
	if index < 0 || index >= len(decoded.Records) {
		return fmt.Errorf("index %d out of range (%d records)", index, len(decoded.Records))
	}
	data, ok := decoded.Records[index]["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("record %d has no data map", index)
	}
	got, ok := data[key]
	if !ok {
		return fmt.Errorf("data.%s missing in record %d", key, index)
	}
	if fmt.Sprintf("%v", got) != want {
		return fmt.Errorf("data.%s = %v, want %q", key, got, want)
	}
	return nil
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
	token, err := tc.tokenStrategy.Issue(username, nil, nil)
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
	token, err := tc.tokenStrategy.Issue(username, repos, nil)
	if err != nil {
		return err
	}
	tc.currentToken = token
	return nil
}

func (tc *testContext) thatTokenHasAbility(ability string) error {
	if tc.currentToken == "" {
		return fmt.Errorf("no current token to grant ability to")
	}
	return tc.tokenStrategy.UpdateAbilities(tc.currentToken, []string{ability})
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

func (tc *testContext) iPOSTWithThatToken(path string) error {
	return tc.postWithToken(path, tc.currentToken)
}

func (tc *testContext) postWithToken(path, token string) error {
	req, err := http.NewRequest(http.MethodPost, tc.ts.URL+path, nil)
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
	return tc.doRequest(http.MethodGet, tc.expandSaved(path), "")
}

func (tc *testContext) iPOSTWithBody(path, body string) error {
	return tc.doRequest(http.MethodPost, tc.expandSaved(path), tc.expandSaved(body))
}

// iPUTWithBody sends a PUT with JSON body. Tokens of the form ${key} in the
// body are expanded from the savedValues map, so scenarios can chain a GET
// /head → save → PUT cycle without hardcoding SHAs.
func (tc *testContext) iPUTWithBody(path, body string) error {
	return tc.doRequest(http.MethodPut, tc.expandSaved(path), tc.expandSaved(body))
}

func (tc *testContext) expandSaved(s string) string {
	for k, v := range tc.savedValues {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}

func (tc *testContext) iDELETE(path string) error {
	return tc.doRequest(http.MethodDelete, tc.expandSaved(path), "")
}

// iPATCHWithBody sends a PATCH with JSON body. Same substitution rules as
// iPUTWithBody — ${key} tokens get expanded from savedValues.
func (tc *testContext) iPATCHWithBody(path, body string) error {
	return tc.doRequest(http.MethodPatch, tc.expandSaved(path), tc.expandSaved(body))
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

func (tc *testContext) theJSONResponseBoolShouldBe(key, expected string) error {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(tc.respBody), &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	val, ok := body[key]
	if !ok {
		return fmt.Errorf("key %q not found in response", key)
	}
	b, ok := val.(bool)
	if !ok {
		return fmt.Errorf("expected bool for %q, got %T", key, val)
	}
	want := expected == "true"
	if b != want {
		return fmt.Errorf("expected %s=%v, got %v", key, want, b)
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
	store := tc.srv.Accounts()
	if _, err := store.Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: username,
		Role:       accounts.RoleAdmin,
	}); err != nil {
		return err
	}
	return store.SetPassword(username, password)
}

// aRegularAccountExists provisions the (local, regular) account the
// subscription-to-account binding requires (§6 Phase 2). Used as a
// prerequisite in every feature that issues a token against a specific
// username — Phase 1's permissive auto-create is gone.
func (tc *testContext) aRegularAccountExists(username string) error {
	if tc.srv == nil {
		return fmt.Errorf("server must be running")
	}
	_, err := tc.srv.Accounts().Put(accounts.Account{
		Provider:   accounts.ProviderLocal,
		Identifier: username,
		Role:       accounts.RoleRegular,
	})
	return err
}

// aScopedRegularAccountExists is the non-local counterpart —
// provisions a (provider, regular) row so scenarios can exercise the
// scoped "provider:identifier" token-username shape introduced with
// OAuth. See accounts.md §6.
func (tc *testContext) aScopedRegularAccountExists(provider, identifier string) error {
	if tc.srv == nil {
		return fmt.Errorf("server must be running")
	}
	_, err := tc.srv.Accounts().Put(accounts.Account{
		Provider:   provider,
		Identifier: identifier,
		Role:       accounts.RoleRegular,
	})
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

// iSaveTheCurrentTokenAs makes tc.currentToken (set by "a token is
// issued for user X" and similar helpers that bypass the HTTP layer)
// addressable through the same ${name} substitution the JSON-response
// saver uses. Lets a scenario chain a direct tokenStrategy.Issue into
// an HTTP POST body without going through the admin API first.
func (tc *testContext) iSaveTheCurrentTokenAs(saveKey string) error {
	if tc.currentToken == "" {
		return fmt.Errorf("no current token to save")
	}
	tc.savedValues[saveKey] = tc.currentToken
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
	ctx.Step(`^the server is running in formidable-first mode$`, tc.theServerIsRunningInFormidableFirstMode)
	ctx.Step(`^I request "([^"]*)"$`, tc.iRequest)
	ctx.Step(`^the response status should be (\d+)$`, tc.theResponseStatusShouldBe)
	ctx.Step(`^the response should contain JSON key "([^"]*)" with value "([^"]*)"$`, tc.theResponseShouldContainJSONKeyWithValue)
	ctx.Step(`^the response content type should contain "([^"]*)"$`, tc.theResponseContentTypeShouldContain)
	ctx.Step(`^the response body should contain "([^"]*)"$`, tc.theResponseBodyShouldContain)
	ctx.Step(`^the response body should not contain "([^"]*)"$`, tc.theResponseBodyShouldNotContain)
	ctx.Step(`^a repository "([^"]*)" exists$`, tc.aRepositoryExists)
	ctx.Step(`^a local git source "([^"]*)" exists$`, tc.aLocalGitSourceExists)
	ctx.Step(`^a local git source "([^"]*)" exists with a formidable marker$`, tc.aLocalGitSourceExistsWithMarker)
	ctx.Step(`^a local git source "([^"]*)" exists with a broken formidable marker$`, tc.aLocalGitSourceExistsWithBrokenMarker)
	ctx.Step(`^the repository "([^"]*)" (has commits|has no commits)$`, tc.theRepositoryHasCommits)
	ctx.Step(`^the repository "([^"]*)" has (\d+) commits$`, tc.theRepositoryHasExactCommits)
	ctx.Step(`^the audit ref in repo "([^"]*)" has (\d+) entries$`, tc.theAuditRefHasEntries)
	ctx.Step(`^the top audit event in repo "([^"]*)" has type "([^"]*)"$`, tc.theAuditTopEventIs)
	ctx.Step(`^a client pushes one commit to "([^"]*)" via smart-HTTP$`, tc.aClientPushesOneCommitViaSmartHTTP)
	ctx.Step(`^the repository "([^"]*)" contains file "([^"]*)"$`, tc.theRepositoryContainsFile)
	ctx.Step(`^the repository "([^"]*)" does not contain file "([^"]*)"$`, tc.theRepositoryDoesNotContainFile)
	ctx.Step(`^the repository "([^"]*)" file "([^"]*)" is valid JSON with field "([^"]*)" equal to "([^"]*)"$`, tc.theRepositoryFileIsJSONWithField)
	ctx.Step(`^the repository "([^"]*)" file "([^"]*)" contains "([^"]*)"$`, tc.theRepositoryFileContains)
	ctx.Step(`^the repository "([^"]*)" head commit is authored by "([^"]*)"$`, tc.theRepositoryHeadCommitAuthor)
	ctx.Step(`^I put a record "([^"]*)" in repo "([^"]*)" with data '([^']*)' updated "([^"]*)" and parent "([^"]*)"$`, tc.iPutARecord)
	ctx.Step(`^I put a record "([^"]*)" in repo "([^"]*)" with data '([^']*)' created "([^"]*)" updated "([^"]*)" and parent "([^"]*)"$`, tc.iPutARecordWithCreated)
	ctx.Step(`^the resulting record "([^"]*)" in repo "([^"]*)" has data field "([^"]*)" equal to "([^"]*)"$`, tc.theResultingRecordHasDataField)
	ctx.Step(`^I put binary file "([^"]*)" in repo "([^"]*)" with bytes "([^"]*)" and parent "([^"]*)"$`, tc.iPutBinaryFile)
	ctx.Step(`^the response body base64-decodes to hex "([^"]*)"$`, tc.theResponseBodyBase64DecodesToHex)
	ctx.Step(`^the records response contains (\d+) records$`, tc.theRecordsResponseContainsNRecords)
	ctx.Step(`^the records response record (\d+) has data field "([^"]*)" equal to "([^"]*)"$`, tc.theRecordsResponseRecordHasDataField)

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
	ctx.Step(`^that token has ability "([^"]*)"$`, tc.thatTokenHasAbility)
	ctx.Step(`^the policy is deny-all$`, tc.thePolicyIsDenyAll)
	ctx.Step(`^the server keypair is rotated$`, tc.theServerKeypairIsRotated)
	ctx.Step(`^the JSON response "([^"]*)" should differ from saved "([^"]*)"$`, tc.theJSONResponseShouldDifferFromSaved)
	ctx.Step(`^I request "([^"]*)" without a token$`, tc.iRequestWithoutAToken)
	ctx.Step(`^I request "([^"]*)" with that token$`, tc.iRequestWithThatToken)
	ctx.Step(`^I POST "([^"]*)" with that token$`, tc.iPOSTWithThatToken)
	ctx.Step(`^I request "([^"]*)" with token "([^"]*)"$`, tc.iRequestWithToken)
	ctx.Step(`^I request "([^"]*)" with saved token "([^"]*)"$`, tc.iRequestWithSavedToken)
	ctx.Step(`^that token is revoked$`, tc.thatTokenIsRevoked)

	// API steps
	ctx.Step(`^I GET "([^"]*)"$`, tc.iGET)
	ctx.Step(`^I POST "([^"]*)" with body '([^']*)'$`, tc.iPOSTWithBody)
	ctx.Step(`^I PUT "([^"]*)" with body '([^']*)'$`, tc.iPUTWithBody)
	ctx.Step(`^I PATCH "([^"]*)" with body '([^']*)'$`, tc.iPATCHWithBody)
	ctx.Step(`^I DELETE "([^"]*)"$`, tc.iDELETE)
	ctx.Step(`^I DELETE "([^"]*)" with saved token "([^"]*)"$`, tc.iDELETEWithSavedToken)
	ctx.Step(`^the JSON response "([^"]*)" should be (\d+)$`, tc.theJSONResponseShouldBe)
	ctx.Step(`^the JSON response "([^"]*)" should be (true|false)$`, tc.theJSONResponseBoolShouldBe)
	ctx.Step(`^the JSON response "([^"]*)" should be "([^"]*)"$`, tc.theJSONResponseStringShouldBe)
	ctx.Step(`^the JSON response "([^"]*)" should not be empty$`, tc.theJSONResponseShouldNotBeEmpty)
	ctx.Step(`^I save the JSON response "([^"]*)" as "([^"]*)"$`, tc.iSaveTheJSONResponseAs)
	ctx.Step(`^I save the current token as "([^"]*)"$`, tc.iSaveTheCurrentTokenAs)
	ctx.Step(`^the JSON response "([^"]*)" should equal saved "([^"]*)"$`, tc.theJSONResponseShouldEqualSaved)
	ctx.Step(`^the server restarts$`, tc.theServerRestarts)
	ctx.Step(`^the server restarts with auth (enabled|disabled)$`, tc.theServerRestartsWithAuth)

	// Client enrollment steps
	ctx.Step(`^a fresh client keypair "([^"]*)"$`, tc.aFreshClientKeypair)
	ctx.Step(`^I enroll client "([^"]*)" with keypair "([^"]*)"$`, tc.iEnrollClientWithKeypair)

	// Admin steps
	ctx.Step(`^an admin "([^"]*)" exists with password "([^"]*)"$`, tc.anAdminExistsWithPassword)
	ctx.Step(`^a regular account "([^"]*)" exists$`, tc.aRegularAccountExists)
	ctx.Step(`^a regular account "([^"]*)" exists on provider "([^"]*)"$`, func(identifier, provider string) error {
		return tc.aScopedRegularAccountExists(provider, identifier)
	})
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
