package cli

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/spf13/cobra"
)

//go:embed web_assets/index.html
var webAssets embed.FS

var (
	errPathTraversal = errors.New("path escapes allowed directories")
)

type webWorkspace struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type runDetails struct {
	history.ListedRun
	RunRecord      string `json:"run_record,omitempty"`
	RunRecordError string `json:"run_record_error,omitempty"`
}

type webServer struct {
	root  string
	index string
}

type webCommandFlags struct {
	port int
}

func newWebCommand() *cobra.Command {
	flags := webCommandFlags{port: 8080}

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Запустить локальный веб-интерфейс execution runs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
			}

			return runWebServer(cmd, root, flags.port)
		},
	}

	cmd.Flags().IntVar(&flags.port, "port", flags.port, "Порт для локального веб-сервера")
	return cmd
}

func runWebServer(cmd *cobra.Command, root string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid --port: %d", port)
	}

	handler, err := newWebHandler(root)
	if err != nil {
		return err
	}

	server := &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(port), Handler: handler}

	fmt.Fprintf(cmd.OutOrStdout(), "Execution runs UI available at http://127.0.0.1:%d/\n", port)
	return server.ListenAndServe()
}

func newWebHandler(root string) (http.Handler, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	assets, err := fs.Sub(webAssets, "web_assets")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	handler := &webServer{root: root, index: string(index)}
	mux.HandleFunc("/api/workspaces", handler.handleWorkspaces)
	mux.HandleFunc("/api/runs", handler.handleRuns)
	mux.HandleFunc("/api/runs/", handler.handleRun)
	mux.HandleFunc("/", handler.handleIndex)
	return mux, nil
}

func (h *webServer) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(h.index))
}

func (h *webServer) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	workspaces, err := collectWebWorkspaces(h.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, workspaces)
}

func (h *webServer) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()
	root, err := h.resolveWorkspaceRoot(query.Get("workspace"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filter := history.ListFilter{Limit: 20}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		filter.Limit = limit
	}
	filter.Name = strings.TrimSpace(query.Get("name"))
	filter.Status = strings.TrimSpace(query.Get("status"))

	runs, err := history.List(r.Context(), root, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, runs)
}

func (h *webServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if path == "" {
		http.Error(w, "run id is required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(path, "/raw-output") {
		path = strings.TrimSuffix(path, "/raw-output")
		path = strings.TrimSuffix(path, "/")
		handleRunRawOutput(w, r, h, path)
		return
	}

	if strings.Contains(path, "/") {
		http.Error(w, "unknown run route", http.StatusNotFound)
		return
	}

	id, err := parseIntID(path)
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	root, err := h.resolveWorkspaceRoot(r.URL.Query().Get("workspace"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	run, err := history.Get(r.Context(), root, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := runDetails{ListedRun: run}
	if strings.TrimSpace(response.RunRecordPath) != "" {
		recordContent, err := readSafeText(h.root, response.RunRecordPath)
		if err != nil {
			response.RunRecordError = err.Error()
		} else {
			response.RunRecord = recordContent
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func handleRunRawOutput(w http.ResponseWriter, r *http.Request, h *webServer, path string) {
	id, err := parseIntID(path)
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	root, err := h.resolveWorkspaceRoot(r.URL.Query().Get("workspace"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	run, err := history.Get(r.Context(), root, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pathToOutput := strings.TrimSpace(run.RawOutputPath)
	if pathToOutput == "" {
		http.Error(w, "raw output is not available", http.StatusNotFound)
		return
	}

	content, err := readSafeText(h.root, pathToOutput)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		if errors.Is(err, errPathTraversal) {
			status = http.StatusForbidden
		}
		if status != http.StatusBadRequest {
			http.Error(w, err.Error(), status)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func collectWebWorkspaces(root string) ([]webWorkspace, error) {
	paths := []string{root}
	if workplaceDirs, err := collectWorkspaceRoots(root); err == nil {
		paths = append(paths, workplaceDirs...)
	} else if err != nil {
		return nil, err
	}

	workspaces := make([]webWorkspace, 0, len(paths))
	for _, path := range paths {
		rel := path
		if relPath, err := filepath.Rel(root, path); err == nil {
			rel = relPath
		}
		name := filepath.Base(path)
		if rel != "." {
			name = rel
		}
		workspaces = append(workspaces, webWorkspace{Name: name, Path: path})
	}

	sort.Slice(workspaces, func(i, j int) bool {
		return workspaces[i].Path < workspaces[j].Path
	})
	return workspaces, nil
}

func (h *webServer) resolveWorkspaceRoot(value string) (string, error) {
	requested := strings.TrimSpace(value)
	if requested == "" {
		return h.root, nil
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(h.root, requested)
	}

	requested, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	requested = filepath.Clean(requested)

	allowed := []string{h.root}
	workspaces, err := collectWorkspaceRoots(h.root)
	if err != nil {
		return "", err
	}
	allowed = append(allowed, workspaces...)
	for _, root := range allowed {
		if requested == filepath.Clean(root) {
			return requested, nil
		}
	}

	return "", errPathTraversal
}

func collectWorkspaceRoots(root string) ([]string, error) {
	workplacesBase := filepath.Join(root, ".progress", "workplaces")
	entries, err := os.ReadDir(workplacesBase)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var workspaces []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		firstLevel := filepath.Join(workplacesBase, entry.Name())
		if isWorkspaceRoot(firstLevel) {
			workspaces = append(workspaces, firstLevel)
			continue
		}

		hasNested := false
		nested, err := os.ReadDir(firstLevel)
		if err == nil {
			for _, child := range nested {
				if !child.IsDir() {
					continue
				}
				workspaces = append(workspaces, filepath.Join(firstLevel, child.Name()))
				hasNested = true
			}
		}
		if !hasNested {
			workspaces = append(workspaces, firstLevel)
		}
	}

	sort.Strings(workspaces)
	return uniqueStrings(workspaces), nil
}

func isWorkspaceRoot(path string) bool {
	for _, marker := range []string{".git", ".progress"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func collectAllowedWorkRoots(root string) ([]string, error) {
	roots := make([]string, 0, 4)

	workplaces, err := collectWorkspaceRoots(root)
	if err != nil {
		return nil, err
	}
	for _, workplace := range workplaces {
		roots = append(roots,
			filepath.Join(workplace, ".progress", "execution-runs"),
			filepath.Join(workplace, ".progress", "runner-output"),
		)
	}

	roots = append(roots,
		filepath.Join(root, ".progress", "execution-runs"),
		filepath.Join(root, ".progress", "runner-output"),
	)

	var allowed []string
	for _, rootPath := range roots {
		if err := appendExistingDirectory(&allowed, rootPath); err != nil {
			return nil, err
		}
	}

	sort.Strings(allowed)
	return uniqueStrings(allowed), nil
}

func readSafeText(root, candidate string) (string, error) {
	if strings.TrimSpace(candidate) == "" {
		return "", os.ErrNotExist
	}

	resolved := strings.TrimSpace(candidate)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	resolved = filepath.Clean(resolved)

	if _, err := os.Stat(resolved); err != nil {
		return "", err
	}

	resolvedSymlinks, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", err
	}
	resolved = resolvedSymlinks

	allowed, err := collectAllowedWorkRoots(root)
	if err != nil {
		return "", err
	}

	if !isInAllowedRoots(resolved, allowed) {
		return "", errPathTraversal
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func parseIntID(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("invalid id")
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return id, nil
}

func isInAllowedRoots(candidate string, allowed []string) bool {
	for _, root := range allowed {
		rel, err := filepath.Rel(root, candidate)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel == "" {
			continue
		}
		if rel == ".." {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

func appendExistingDirectory(values *[]string, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	resolved := absPath
	if target, err := filepath.EvalSymlinks(absPath); err == nil {
		resolved = target
	}

	if stat, err := os.Stat(resolved); err != nil {
		return err
	} else if !stat.IsDir() {
		return nil
	}

	*values = append(*values, resolved)
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
