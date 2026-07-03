package localtracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/rasungatullin/progress/internal/integration/model"
)

const (
	defaultDriver       = "sqlite"
	defaultDatabasePath = ".progress/local-tracker/tasks.sqlite"
)

type Service struct {
	config          model.IntegrationSystemConfig
	resolveRepoRoot func(context.Context) (string, error)
	openDatabase    func(string) (*sql.DB, error)
	now             func() time.Time
}

type taskRecord struct {
	Number     int
	ExternalID string
	Title      string
	Body       string
	State      string
	Traits     []string
	Attributes map[string]string
	Author     string
	CreatedAt  string
	UpdatedAt  string
}

type commentRecord struct {
	ID         int
	ExternalID string
	TaskNumber int
	Author     string
	Body       string
	CreatedAt  string
	UpdatedAt  string
}

func NewService(config model.IntegrationSystemConfig) *Service {
	return &Service{
		config:          config,
		resolveRepoRoot: resolveRepoRoot,
		openDatabase:    openSQLite,
		now:             func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: model.IntegrationTypeTracker,
		System:          req.System,
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
	}

	db, dbPath, err := s.open(ctx)
	if err != nil {
		return failureResponse(response, model.FailureKindInvalidRequest, err)
	}
	defer db.Close()

	if req.Resource == "auth" && req.Operation == "status" {
		response.AuthStatus = &model.AuthStatus{
			System:        req.System,
			State:         "ready",
			Available:     true,
			Authenticated: true,
			Command:       "sqlite",
			Path:          dbPath,
			Message:       "local tracker storage is available",
			Diagnostics:   []string{"driver=sqlite", "database=" + dbPath},
		}
		response.Status = model.ResponseStatusOK
		return response, nil
	}

	switch {
	case isTaskObject(req) && req.Operation == "create":
		return s.createTask(ctx, db, response, req)
	case isTaskObject(req) && req.Operation == "get":
		return s.getTask(ctx, db, response, req)
	case isTaskObject(req) && req.Operation == "search":
		return s.searchTasks(ctx, db, response, req)
	case isTaskObject(req) && req.Operation == "update":
		return s.updateTask(ctx, db, response, req)
	case isTaskObject(req) && isCommentListOperation(req.Operation):
		return s.listComments(ctx, db, response, req)
	case isTaskCommentObject(req) && isCommentListOperation(req.Operation):
		return s.listComments(ctx, db, response, req)
	case isTaskCommentObject(req) && req.Operation == "create":
		return s.createComment(ctx, db, response, req)
	case isTaskLabelObject(req) && req.Operation == "add":
		return s.changeLabels(ctx, db, response, req, true)
	case isTaskLabelObject(req) && req.Operation == "remove":
		return s.changeLabels(ctx, db, response, req, false)
	default:
		return failureResponse(response, model.FailureKindUnsupportedOperation, fmt.Errorf("local tracker does not support %s %s", firstNonEmpty(req.ObjectType, req.Resource), req.Operation))
	}
}

func (s *Service) createTask(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest) (model.Response, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return failureResponse(response, model.FailureKindInvalidRequest, fmt.Errorf("local tracker task title is required"))
	}
	now := s.now().Format(time.RFC3339)
	state := firstNonEmpty(req.State, "open")
	traits := normalizeStrings(req.Labels)
	traitsJSON, _ := json.Marshal(traits)
	attrsJSON, _ := json.Marshal(map[string]string{})
	result, err := db.ExecContext(ctx, `INSERT INTO tasks(external_id,title,body,state,traits_json,attributes_json,author,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, nullableString(req.ExternalID), title, req.Body, state, string(traitsJSON), string(attrsJSON), "", now, now)
	if err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, fmt.Errorf("create local tracker task: %w", err))
	}
	id, _ := result.LastInsertId()
	task, err := s.taskByNumber(ctx, db, int(id))
	if err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, err)
	}
	applyTask(&response, req.System, task)
	response.OperationResult = operationResult(req.System, "task", "create", strconv.Itoa(task.Number), "", "local tracker task created")
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) getTask(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest) (model.Response, error) {
	task, err := s.findTask(ctx, db, req)
	if err != nil {
		return responseForFindError(response, err)
	}
	applyTask(&response, req.System, task)
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) searchTasks(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest) (model.Response, error) {
	records, err := s.queryTasks(ctx, db, req)
	if err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, err)
	}
	response.SearchResults = make([]model.TrackerSearchResult, 0, len(records))
	for _, record := range records {
		task := canonicalTask(req.System, record)
		response.SearchResults = append(response.SearchResults, model.TrackerSearchResult{
			System:    req.System,
			Kind:      "task",
			Number:    task.Number,
			Title:     task.Title,
			State:     task.State,
			URL:       task.URL,
			UpdatedAt: task.UpdatedAt,
		})
	}
	response.Metadata = map[string]string{"count": strconv.Itoa(len(response.SearchResults))}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) updateTask(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest) (model.Response, error) {
	task, err := s.findTask(ctx, db, req)
	if err != nil {
		return responseForFindError(response, err)
	}
	if strings.TrimSpace(req.Title) != "" {
		task.Title = strings.TrimSpace(req.Title)
	}
	if strings.TrimSpace(req.Body) != "" {
		task.Body = req.Body
	}
	if strings.TrimSpace(req.State) != "" {
		task.State = strings.TrimSpace(req.State)
	}
	if len(req.Labels) > 0 {
		task.Traits = normalizeStrings(req.Labels)
	}
	task.UpdatedAt = s.now().Format(time.RFC3339)
	traitsJSON, _ := json.Marshal(task.Traits)
	attrsJSON, _ := json.Marshal(task.Attributes)
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET title=?, body=?, state=?, traits_json=?, attributes_json=?, updated_at=? WHERE number=?`, task.Title, task.Body, task.State, string(traitsJSON), string(attrsJSON), task.UpdatedAt, task.Number); err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, fmt.Errorf("update local tracker task: %w", err))
	}
	applyTask(&response, req.System, task)
	response.OperationResult = operationResult(req.System, "task", "update", strconv.Itoa(task.Number), "", "local tracker task updated")
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) listComments(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest) (model.Response, error) {
	task, err := s.findTask(ctx, db, req)
	if err != nil {
		return responseForFindError(response, err)
	}
	rows, err := db.QueryContext(ctx, `SELECT id, external_id, task_number, author, body, created_at, updated_at FROM task_comments WHERE task_number=? ORDER BY id`, task.Number)
	if err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, fmt.Errorf("list local tracker comments: %w", err))
	}
	defer rows.Close()
	for rows.Next() {
		var record commentRecord
		var externalID sql.NullString
		if err := rows.Scan(&record.ID, &externalID, &record.TaskNumber, &record.Author, &record.Body, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return failureResponse(response, model.FailureKindExternalFailure, err)
		}
		record.ExternalID = externalID.String
		comment := taskComment(req.System, record)
		response.TaskComments = append(response.TaskComments, comment)
		response.Comments = append(response.Comments, trackerComment(comment))
	}
	response.Metadata = map[string]string{"number": strconv.Itoa(task.Number), "count": strconv.Itoa(len(response.TaskComments))}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) createComment(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest) (model.Response, error) {
	task, err := s.findTask(ctx, db, req)
	if err != nil {
		return responseForFindError(response, err)
	}
	body := strings.TrimSpace(firstNonEmpty(req.Body, req.Text))
	if body == "" {
		return failureResponse(response, model.FailureKindInvalidRequest, fmt.Errorf("local tracker comment body is required"))
	}
	now := s.now().Format(time.RFC3339)
	result, err := db.ExecContext(ctx, `INSERT INTO task_comments(task_number, author, body, created_at, updated_at) VALUES(?,?,?,?,?)`, task.Number, "", body, now, now)
	if err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, fmt.Errorf("create local tracker comment: %w", err))
	}
	id, _ := result.LastInsertId()
	record := commentRecord{ID: int(id), TaskNumber: task.Number, Body: body, CreatedAt: now, UpdatedAt: now}
	comment := taskComment(req.System, record)
	response.TaskComments = []model.TaskComment{comment}
	response.Comments = []model.TrackerComment{trackerComment(comment)}
	response.OperationResult = operationResult(req.System, "comment", "create", strconv.Itoa(record.ID), "", "local tracker comment created")
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) changeLabels(ctx context.Context, db *sql.DB, response model.Response, req model.ProviderRequest, add bool) (model.Response, error) {
	task, err := s.findTask(ctx, db, req)
	if err != nil {
		return responseForFindError(response, err)
	}
	labels := normalizeStrings(req.Labels)
	if len(labels) == 0 {
		return failureResponse(response, model.FailureKindInvalidRequest, fmt.Errorf("local tracker label is required"))
	}
	if add {
		task.Traits = mergeStrings(task.Traits, labels)
	} else {
		task.Traits = removeStrings(task.Traits, labels)
	}
	task.UpdatedAt = s.now().Format(time.RFC3339)
	traitsJSON, _ := json.Marshal(task.Traits)
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET traits_json=?, updated_at=? WHERE number=?`, string(traitsJSON), task.UpdatedAt, task.Number); err != nil {
		return failureResponse(response, model.FailureKindExternalFailure, fmt.Errorf("update local tracker labels: %w", err))
	}
	operation := "remove"
	if add {
		operation = "add"
	}
	applyTask(&response, req.System, task)
	response.OperationResult = operationResult(req.System, "label", operation, strconv.Itoa(task.Number), "", "local tracker labels updated")
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) open(ctx context.Context) (*sql.DB, string, error) {
	driver := strings.TrimSpace(strings.ToLower(s.config.Database.Driver))
	if driver == "" {
		driver = defaultDriver
	}
	if driver != defaultDriver {
		return nil, "", fmt.Errorf("local tracker database driver is not supported: %s", driver)
	}
	repoRoot, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return nil, "", err
	}
	dbSource := strings.TrimSpace(s.config.Database.DSN)
	dbPath := dbSource
	if dbSource == "" {
		dbPath = strings.TrimSpace(s.config.Database.Path)
		if dbPath == "" {
			dbPath = defaultDatabasePath
		}
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(repoRoot, dbPath)
		}
		dbSource = sqlitePathDataSourceName(dbPath)
	} else {
		dbSource, dbPath = normalizeSQLiteDataSourceName(dbSource, repoRoot)
	}
	if dbPath != "" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, "", fmt.Errorf("prepare local tracker database directory: %w", err)
		}
	}
	db, err := s.openDatabase(dbSource)
	if err != nil {
		return nil, "", err
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, "", err
	}
	return db, dbPath, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			number INTEGER PRIMARY KEY AUTOINCREMENT,
			external_id TEXT UNIQUE,
			title TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'open',
			traits_json TEXT NOT NULL DEFAULT '[]',
			attributes_json TEXT NOT NULL DEFAULT '{}',
			author TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS task_comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			external_id TEXT,
			task_number INTEGER NOT NULL,
			author TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(task_number) REFERENCES tasks(number)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_comments_task_number ON task_comments(task_number)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_state ON tasks(state)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate local tracker schema: %w", err)
		}
	}
	return nil
}

func (s *Service) findTask(ctx context.Context, db *sql.DB, req model.ProviderRequest) (taskRecord, error) {
	if req.Number > 0 {
		return s.taskByNumber(ctx, db, req.Number)
	}
	externalID := strings.TrimSpace(req.ExternalID)
	if externalID == "" {
		return taskRecord{}, fmt.Errorf("local tracker task number or external_id is required")
	}
	return s.taskByExternalID(ctx, db, externalID)
}

func (s *Service) taskByNumber(ctx context.Context, db *sql.DB, number int) (taskRecord, error) {
	return scanTask(db.QueryRowContext(ctx, `SELECT number, external_id, title, body, state, traits_json, attributes_json, author, created_at, updated_at FROM tasks WHERE number=?`, number))
}

func (s *Service) taskByExternalID(ctx context.Context, db *sql.DB, externalID string) (taskRecord, error) {
	return scanTask(db.QueryRowContext(ctx, `SELECT number, external_id, title, body, state, traits_json, attributes_json, author, created_at, updated_at FROM tasks WHERE external_id=?`, externalID))
}

func (s *Service) queryTasks(ctx context.Context, db *sql.DB, req model.ProviderRequest) ([]taskRecord, error) {
	query := `SELECT number, external_id, title, body, state, traits_json, attributes_json, author, created_at, updated_at FROM tasks`
	var where []string
	var args []any
	labels := normalizeStrings(req.Labels)
	if value := strings.TrimSpace(req.Query); value != "" {
		where = append(where, `(title LIKE ? OR body LIKE ? OR external_id LIKE ?)`)
		pattern := "%" + value + "%"
		args = append(args, pattern, pattern, pattern)
	}
	if value := strings.TrimSpace(req.State); value != "" {
		where = append(where, `state=?`)
		args = append(args, value)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC, number DESC"
	if req.Limit > 0 && len(labels) == 0 {
		query += " LIMIT ?"
		args = append(args, req.Limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search local tracker tasks: %w", err)
	}
	defer rows.Close()
	var records []taskRecord
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if len(labels) > 0 && !hasAllStrings(record.Traits, labels) {
			continue
		}
		records = append(records, record)
		if req.Limit > 0 && len(records) >= req.Limit {
			break
		}
	}
	return records, rows.Err()
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(scanner taskScanner) (taskRecord, error) {
	var record taskRecord
	var externalID sql.NullString
	var traitsJSON string
	var attributesJSON string
	err := scanner.Scan(&record.Number, &externalID, &record.Title, &record.Body, &record.State, &traitsJSON, &attributesJSON, &record.Author, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return taskRecord{}, err
	}
	record.ExternalID = externalID.String
	_ = json.Unmarshal([]byte(traitsJSON), &record.Traits)
	_ = json.Unmarshal([]byte(attributesJSON), &record.Attributes)
	if record.Attributes == nil {
		record.Attributes = map[string]string{}
	}
	return record, nil
}

func applyTask(response *model.Response, system string, record taskRecord) {
	task := canonicalTask(system, record)
	response.Task = &task
	issue := trackerIssue(task)
	response.Issue = &issue
}

func canonicalTask(system string, record taskRecord) model.CanonicalTask {
	return model.CanonicalTask{
		System:     system,
		Number:     record.Number,
		ExternalID: record.ExternalID,
		Title:      record.Title,
		Body:       record.Body,
		State:      record.State,
		Traits:     append([]string(nil), record.Traits...),
		Attributes: record.Attributes,
		Author:     model.User{System: system, Login: record.Author},
		URL:        localTaskURL(system, record.Number),
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
	}
}

func trackerIssue(task model.CanonicalTask) model.TrackerIssue {
	return model.TrackerIssue{
		System:    task.System,
		Number:    task.Number,
		Title:     task.Title,
		Body:      task.Body,
		State:     task.State,
		Labels:    append([]string(nil), task.Traits...),
		Author:    model.TrackerUser{System: task.System, Login: task.Author.Login},
		URL:       task.URL,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}
}

func taskComment(system string, record commentRecord) model.TaskComment {
	return model.TaskComment{
		System:     system,
		TaskNumber: record.TaskNumber,
		ExternalID: firstNonEmpty(record.ExternalID, strconv.Itoa(record.ID)),
		Author:     model.User{System: system, Login: record.Author},
		Body:       record.Body,
		URL:        localCommentURL(system, record.TaskNumber, record.ID),
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
	}
}

func trackerComment(comment model.TaskComment) model.TrackerComment {
	return model.TrackerComment{
		System:    comment.System,
		Number:    comment.TaskNumber,
		Author:    model.TrackerUser{System: comment.System, Login: comment.Author.Login},
		Body:      comment.Body,
		URL:       comment.URL,
		CreatedAt: comment.CreatedAt,
		UpdatedAt: comment.UpdatedAt,
	}
}

func operationResult(system, objectType, operation, externalID, urlValue, message string) *model.OperationResult {
	return &model.OperationResult{
		System:     system,
		ObjectType: objectType,
		Operation:  operation,
		Status:     model.ResponseStatusOK,
		ExternalID: externalID,
		URL:        urlValue,
		Method:     "sqlite",
		Message:    message,
	}
}

func responseForFindError(response model.Response, err error) (model.Response, error) {
	if err == sql.ErrNoRows {
		return failureResponse(response, model.FailureKindNotFound, fmt.Errorf("local tracker task not found"))
	}
	return failureResponse(response, model.FailureKindInvalidRequest, err)
}

func failureResponse(response model.Response, kind string, err error) (model.Response, error) {
	response.Status = model.ResponseStatusFailed
	response.Failure = &model.Failure{Kind: kind, Message: err.Error()}
	return response, err
}

func isTaskObject(req model.ProviderRequest) bool {
	object := normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "task" || object == "issue"
}

func isTaskCommentObject(req model.ProviderRequest) bool {
	object := normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "comment" || object == "task-comment"
}

func isCommentListOperation(operation string) bool {
	switch strings.TrimSpace(strings.ToLower(operation)) {
	case "comments", "list", "list-comments":
		return true
	default:
		return false
	}
}

func isTaskLabelObject(req model.ProviderRequest) bool {
	object := normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "label" || object == "task-label"
}

func normalizeObjectType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "issue":
		return "task"
	case "task-label":
		return "label"
	default:
		return value
	}
}

func normalizeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func mergeStrings(base, added []string) []string {
	return normalizeStrings(append(append([]string(nil), base...), added...))
}

func removeStrings(base, removed []string) []string {
	remove := map[string]struct{}{}
	for _, value := range removed {
		remove[strings.ToLower(value)] = struct{}{}
	}
	var result []string
	for _, value := range base {
		if _, ok := remove[strings.ToLower(value)]; !ok {
			result = append(result, value)
		}
	}
	return result
}

func hasAllStrings(base, required []string) bool {
	values := map[string]struct{}{}
	for _, value := range base {
		values[strings.ToLower(value)] = struct{}{}
	}
	for _, value := range required {
		if _, ok := values[strings.ToLower(value)]; !ok {
			return false
		}
	}
	return true
}

func localTaskURL(system string, number int) string {
	return "local-tracker://" + url.PathEscape(system) + "/tasks/" + strconv.Itoa(number)
}

func localCommentURL(system string, number int, id int) string {
	return localTaskURL(system, number) + "#comment-" + strconv.Itoa(id)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func resolveRepoRoot(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return "", fmt.Errorf("resolve local tracker root: %w", err)
		}
		return wd, nil
	}
	return strings.TrimSpace(string(output)), nil
}

func openSQLite(dataSourceName string) (*sql.DB, error) {
	return sql.Open("sqlite3", dataSourceName)
}

func sqlitePathDataSourceName(path string) string {
	fileURL := url.URL{Scheme: "file", Path: path}
	query := fileURL.Query()
	query.Set("_foreign_keys", "on")
	fileURL.RawQuery = query.Encode()
	return fileURL.String()
}

func normalizeSQLiteDataSourceName(dataSourceName string, repoRoot string) (string, string) {
	dataSourceName = strings.TrimSpace(dataSourceName)
	if dataSourceName == "" || dataSourceName == ":memory:" || strings.HasPrefix(dataSourceName, "file::memory:") {
		return dataSourceName, ""
	}
	if strings.HasPrefix(dataSourceName, "file:") {
		fileURL, err := url.Parse(dataSourceName)
		if err != nil {
			return dataSourceName, ""
		}
		path := strings.TrimSpace(fileURL.Path)
		if path == "" {
			path = strings.TrimSpace(fileURL.Opaque)
		}
		if path == "" {
			return dataSourceName, ""
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, path)
		}
		return (&url.URL{Scheme: "file", Path: path, RawQuery: fileURL.RawQuery}).String(), path
	}
	path, query, hasQuery := strings.Cut(dataSourceName, "?")
	path = strings.TrimSpace(path)
	if path == "" {
		return dataSourceName, ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	if hasQuery {
		return path + "?" + query, path
	}
	return path, path
}
