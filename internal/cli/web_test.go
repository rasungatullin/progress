package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/history"
)

func TestReadSafeTextAllowsAllowedFiles(t *testing.T) {
	root := t.TempDir()

	recordRoot := filepath.Join(root, ".progress", "execution-runs")
	rawRoot := filepath.Join(root, ".progress", "runner-output")
	if err := os.MkdirAll(recordRoot, 0o755); err != nil {
		t.Fatalf("mkdir record root: %v", err)
	}
	if err := os.MkdirAll(rawRoot, 0o755); err != nil {
		t.Fatalf("mkdir raw root: %v", err)
	}

	recordPath := filepath.Join(recordRoot, "execution-1.json")
	rawPath := filepath.Join(rawRoot, "execution-1.log")
	if err := os.WriteFile(recordPath, []byte(`{"name":"run"}`), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if err := os.WriteFile(rawPath, []byte("runner output"), 0o600); err != nil {
		t.Fatalf("write raw output: %v", err)
	}

	content, err := readSafeText(root, ".progress/execution-runs/execution-1.json")
	if err != nil {
		t.Fatalf("read allowed record: %v", err)
	}
	if strings.TrimSpace(content) != `{"name":"run"}` {
		t.Fatalf("unexpected record content: %q", content)
	}

	content, err = readSafeText(root, rawPath)
	if err != nil {
		t.Fatalf("read allowed raw output: %v", err)
	}
	if strings.TrimSpace(content) != "runner output" {
		t.Fatalf("unexpected raw output content: %q", content)
	}
}

func TestReadSafeTextRejectsTraversalOutsideRoot(t *testing.T) {
	root := t.TempDir()

	workplaces := filepath.Join(root, ".progress", "workplaces", "task-1")
	if err := os.MkdirAll(filepath.Join(workplaces, ".progress", "execution-runs"), 0o755); err != nil {
		t.Fatalf("prepare workplace execution-runs: %v", err)
	}

	evilTarget := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(evilTarget, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write evil file: %v", err)
	}

	evilPath := filepath.Join(workplaces, ".progress", "execution-runs", "linked.txt")
	if err := os.Symlink(evilTarget, evilPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := readSafeText(root, evilPath)
	if !errors.Is(err, errPathTraversal) {
		t.Fatalf("expected traversal error, got: %v", err)
	}
}

func TestReadSafeTextRejectsSymlinkedArtifactDirectoryOutsideRoot(t *testing.T) {
	root := t.TempDir()

	progressRoot := filepath.Join(root, ".progress")
	if err := os.MkdirAll(progressRoot, 0o755); err != nil {
		t.Fatalf("mkdir progress root: %v", err)
	}

	externalRawRoot := filepath.Join(t.TempDir(), "runner-output")
	if err := os.MkdirAll(externalRawRoot, 0o755); err != nil {
		t.Fatalf("mkdir external raw root: %v", err)
	}
	externalRawPath := filepath.Join(externalRawRoot, "execution-1.log")
	if err := os.WriteFile(externalRawPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write external raw output: %v", err)
	}

	if err := os.Symlink(externalRawRoot, filepath.Join(progressRoot, "runner-output")); err != nil {
		t.Fatalf("create runner-output symlink: %v", err)
	}

	_, err := readSafeText(root, externalRawPath)
	if !errors.Is(err, errPathTraversal) {
		t.Fatalf("expected traversal error for external symlink target, got: %v", err)
	}

	_, err = readSafeText(root, filepath.Join(root, ".progress", "runner-output", "execution-1.log"))
	if !errors.Is(err, errPathTraversal) {
		t.Fatalf("expected traversal error through symlinked artifact directory, got: %v", err)
	}
}

func TestWebHandlersServeExecutionRuns(t *testing.T) {
	root := t.TempDir()

	recordRoot := filepath.Join(root, ".progress", "execution-runs")
	rawRoot := filepath.Join(root, ".progress", "runner-output")
	if err := os.MkdirAll(recordRoot, 0o755); err != nil {
		t.Fatalf("mkdir execution-runs: %v", err)
	}
	if err := os.MkdirAll(rawRoot, 0o755); err != nil {
		t.Fatalf("mkdir runner-output: %v", err)
	}

	rawPath := filepath.Join(rawRoot, "execution-1.log")
	if err := os.WriteFile(rawPath, []byte("full raw output"), 0o600); err != nil {
		t.Fatalf("write raw output: %v", err)
	}

	recordPath := filepath.Join(recordRoot, "execution-1.json")
	if err := os.WriteFile(recordPath, []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatalf("write execution record: %v", err)
	}

	if err := history.Store(context.Background(), root, history.Run{
		CreatedAt:           "2026-06-10T10:00:00Z",
		Status:              "completed",
		Summary:             "done",
		Name:                "task-1",
		ProfileName:         "default",
		Runner:              "opencode",
		Model:               "openai/gpt-5.4",
		RunnerSessionID:     "session-1",
		LaunchDirectory:     root,
		RawOutputPath:       rawPath,
		RawStructuredOutput: `{"summary":"ok"}`,
		RunRecordPath:       recordPath,
	}); err != nil {
		t.Fatalf("store run: %v", err)
	}

	handler, err := newWebHandler(root)
	if err != nil {
		t.Fatalf("new web handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("runs endpoint status: %d", w.Code)
	}

	var runs []history.ListedRun
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].RunnerSessionID != "session-1" {
		t.Fatalf("unexpected run session id: %#v", runs)
	}

	runID := runs[0].ID
	request = httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10), nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("run detail endpoint status: %d", w.Code)
	}

	var details struct {
		history.ListedRun
		RunRecord string `json:"run_record"`
	}
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&details); err != nil {
		t.Fatalf("decode run details: %v", err)
	}
	if details.RunnerSessionID != "session-1" {
		t.Fatalf("unexpected run detail session id: %#v", details)
	}
	if details.RunRecord != "{\"x\":1}" {
		t.Fatalf("unexpected run record: %q", details.RunRecord)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runID, 10)+"/raw-output", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("raw output status: %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "full raw output" {
		t.Fatalf("unexpected raw output body: %q", w.Body.String())
	}
}

func TestWebHandlerServesSidebarNavigationShell(t *testing.T) {
	root := t.TempDir()
	handler, err := newWebHandler(root)
	if err != nil {
		t.Fatalf("new web handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("index status: %d", w.Code)
	}

	body := w.Body.String()
	for _, snippet := range []string{"Прогресс", "progress", "История", "Структурированный вывод"} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("index page is missing %q", snippet)
		}
	}
}

func TestWebHandlerOmitRunnerSessionIDWhenUnknown(t *testing.T) {
	root := t.TempDir()

	handler, err := newWebHandler(root)
	if err != nil {
		t.Fatalf("new web handler: %v", err)
	}

	if err := history.Store(context.Background(), root, history.Run{
		CreatedAt:       "2026-06-10T10:00:00Z",
		Status:          "completed",
		Summary:         "done",
		Name:            "task-2",
		ProfileName:     "default",
		Runner:          "opencode",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: root,
	}); err != nil {
		t.Fatalf("store run: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("runs endpoint status: %d", w.Code)
	}

	var runs []map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if _, ok := runs[0]["runner_session_id"]; ok {
		t.Fatalf("runner_session_id must be omitted when unknown: %#v", runs[0])
	}

	request = httptest.NewRequest(http.MethodGet, "/api/runs/1", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("run detail endpoint status: %d", w.Code)
	}

	var details map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&details); err != nil {
		t.Fatalf("decode run details: %v", err)
	}
	if _, ok := details["runner_session_id"]; ok {
		t.Fatalf("run detail should not include runner_session_id when unknown: %#v", details)
	}
}

func TestWebHandlersServeWorkspaceRuns(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, ".progress", "workplaces", "repo", "task-a")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if err := history.Store(context.Background(), workspace, history.Run{
		CreatedAt:          "2026-06-10T11:00:00Z",
		Status:             "failed",
		Summary:            "broken",
		Name:               "workspace-run",
		ProfileName:        "default",
		Runner:             "opencode",
		Model:              "openai/gpt-5.4",
		LaunchDirectory:    workspace,
		RawStructuredInput: `{"task":"inspect"}`,
		Error:              "boom",
	}); err != nil {
		t.Fatalf("store workspace run: %v", err)
	}

	handler, err := newWebHandler(root)
	if err != nil {
		t.Fatalf("new web handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runs?workspace="+workspace, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("workspace runs endpoint status: %d", w.Code)
	}

	var runs []history.ListedRun
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&runs); err != nil {
		t.Fatalf("decode workspace runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Name != "workspace-run" || runs[0].Error != "boom" {
		t.Fatalf("unexpected workspace runs: %#v", runs)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/runs/"+strconv.FormatInt(runs[0].ID, 10)+"?workspace="+workspace, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("workspace detail endpoint status: %d", w.Code)
	}
}

func TestWebHandlersDoNotCreateHistoryStateWhenDatabaseIsAbsent(t *testing.T) {
	root := t.TempDir()
	handler, err := newWebHandler(root)
	if err != nil {
		t.Fatalf("new web handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("runs endpoint status: %d", w.Code)
	}

	var runs []history.ListedRun
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs, got %d", len(runs))
	}

	request = httptest.NewRequest(http.MethodGet, "/api/runs/1", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusNotFound {
		t.Fatalf("detail endpoint status: %d", w.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/runs/1/raw-output", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusNotFound {
		t.Fatalf("raw output endpoint status: %d", w.Code)
	}

	if _, err := os.Stat(filepath.Join(root, ".progress", "execution-runs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no execution-runs directory, got err: %v", err)
	}
}

func TestWebHandlersRejectUnknownWorkspace(t *testing.T) {
	root := t.TempDir()
	handler, err := newWebHandler(root)
	if err != nil {
		t.Fatalf("new web handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runs?workspace="+filepath.Join(root, "unknown"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown workspace status: %d", w.Code)
	}
}

func TestCollectWorkspaceRootsIncludesNestedWorkplaces(t *testing.T) {
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, ".progress", "workplaces", "repo", "task-a"), 0o755); err != nil {
		t.Fatalf("create nested workplace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".progress", "workplaces", "task-b"), 0o755); err != nil {
		t.Fatalf("create direct workplace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "workplaces", "task-b", ".git"), []byte("gitdir: ../.git/worktrees/task-b"), 0o600); err != nil {
		t.Fatalf("create direct workplace git marker: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".progress", "workplaces", "task-b", "internal"), 0o755); err != nil {
		t.Fatalf("create direct workplace child dir: %v", err)
	}

	paths, err := collectWorkspaceRoots(root)
	if err != nil {
		t.Fatalf("collect workplaces: %v", err)
	}
	sort.Strings(paths)

	if len(paths) != 2 {
		t.Fatalf("expected 2 workplace roots, got %d", len(paths))
	}

	expectedNested := filepath.Clean(filepath.Join(root, ".progress", "workplaces", "repo", "task-a"))
	expectedDirect := filepath.Clean(filepath.Join(root, ".progress", "workplaces", "task-b"))
	if paths[0] != expectedNested || paths[1] != expectedDirect {
		t.Fatalf("expected nested and direct roots: %#v", paths)
	}
}
