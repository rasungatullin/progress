package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	DirName = ".progress/execution-runs"
	DBName  = "execution.db"
)

type Run struct {
	CreatedAt           string
	Status              string
	Summary             string
	Name                string
	ProfileName         string
	Runner              string
	Model               string
	LaunchDirectory     string
	RawStructuredInput  string
	RawOutputPath       string
	RawStructuredOutput string
	RunRecordPath       string
	Error               string
}

type Handle struct {
	Root      string
	RunID     int64
	RequestID int64
}

type ListedRun struct {
	ID                  int64  `json:"id"`
	CreatedAt           string `json:"created_at"`
	Status              string `json:"status"`
	Summary             string `json:"summary"`
	Name                string `json:"name,omitempty"`
	ProfileName         string `json:"profile_name"`
	Runner              string `json:"runner"`
	Model               string `json:"model"`
	LaunchDirectory     string `json:"launch_directory"`
	RawStructuredInput  string `json:"raw_structured_input,omitempty"`
	RawOutputPath       string `json:"raw_output_path,omitempty"`
	RawStructuredOutput string `json:"raw_structured_output,omitempty"`
	RunRecordPath       string `json:"run_record_path,omitempty"`
	Error               string `json:"error,omitempty"`
}

type ListFilter struct {
	Limit  int
	Name   string
	Status string
}

func DatabasePath(root string) string {
	return filepath.Join(root, DirName, DBName)
}

func StructuredInputJSON(input *model.StructuredInput) string {
	if input == nil {
		return ""
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return ""
	}

	return string(payload)
}

func StructuredOutputJSON(output *model.StructuredOutput, raw string) string {
	if output != nil {
		payload, err := json.Marshal(output)
		if err == nil {
			return string(payload)
		}
	}

	return strings.TrimSpace(raw)
}

func Store(ctx context.Context, root string, run Run) error {
	handle, err := Begin(ctx, root, run)
	if err != nil {
		return err
	}

	return Update(ctx, handle, run)
}

func Begin(ctx context.Context, root string, run Run) (Handle, error) {
	dbPath := DatabasePath(root)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return Handle{}, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return Handle{}, err
	}
	defer db.Close()

	if err := ensureSchema(ctx, db); err != nil {
		return Handle{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Handle{}, err
	}
	defer tx.Rollback()

	requestResult, err := tx.ExecContext(ctx, `INSERT INTO execution_requests (model, launch_directory, raw_structured_input) VALUES (?, ?, ?)`, run.Model, run.LaunchDirectory, run.RawStructuredInput)
	if err != nil {
		return Handle{}, err
	}
	requestID, err := requestResult.LastInsertId()
	if err != nil {
		return Handle{}, err
	}
	runResult, err := tx.ExecContext(ctx, `INSERT INTO execution_runs (created_at, status, summary, name, profile_name, runner, request_id, result_id, run_record_path, error) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`, run.CreatedAt, run.Status, run.Summary, nullable(run.Name), run.ProfileName, run.Runner, requestID, nullable(run.RunRecordPath), nullable(run.Error))
	if err != nil {
		return Handle{}, err
	}
	runID, err := runResult.LastInsertId()
	if err != nil {
		return Handle{}, err
	}

	if err := tx.Commit(); err != nil {
		return Handle{}, err
	}

	handle := Handle{Root: root, RunID: runID, RequestID: requestID}
	if hasResult(run) {
		if err := Update(ctx, handle, run); err != nil {
			return Handle{}, err
		}
	}
	return handle, nil
}

func Update(ctx context.Context, handle Handle, run Run) error {
	if strings.TrimSpace(handle.Root) == "" || handle.RunID == 0 || handle.RequestID == 0 {
		return nil
	}

	db, err := sql.Open("sqlite3", DatabasePath(handle.Root))
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureSchema(ctx, db); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE execution_requests SET model = ?, launch_directory = ?, raw_structured_input = ? WHERE id = ?`, run.Model, run.LaunchDirectory, run.RawStructuredInput, handle.RequestID); err != nil {
		return err
	}

	var resultID any
	if hasResult(run) {
		var existingResultID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT result_id FROM execution_runs WHERE id = ?`, handle.RunID).Scan(&existingResultID); err != nil {
			return err
		}
		if existingResultID.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE execution_results SET raw_output_path = ?, raw_structured_output = ? WHERE id = ?`, run.RawOutputPath, run.RawStructuredOutput, existingResultID.Int64); err != nil {
				return err
			}
			resultID = existingResultID.Int64
		} else {
			resultResult, err := tx.ExecContext(ctx, `INSERT INTO execution_results (raw_output_path, raw_structured_output) VALUES (?, ?)`, run.RawOutputPath, run.RawStructuredOutput)
			if err != nil {
				return err
			}
			id, err := resultResult.LastInsertId()
			if err != nil {
				return err
			}
			resultID = id
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE execution_runs SET status = ?, summary = ?, name = ?, profile_name = ?, runner = ?, result_id = ?, run_record_path = ?, error = ? WHERE id = ?`, run.Status, run.Summary, nullable(run.Name), run.ProfileName, run.Runner, resultID, nullable(run.RunRecordPath), nullable(run.Error), handle.RunID); err != nil {
		return err
	}

	return tx.Commit()
}

func List(ctx context.Context, root string, filter ListFilter) ([]ListedRun, error) {
	dbPath := DatabasePath(root)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := ensureSchema(ctx, db); err != nil {
		return nil, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	where := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if strings.TrimSpace(filter.Name) != "" {
		where = append(where, "r.name = ?")
		args = append(args, strings.TrimSpace(filter.Name))
	}
	if strings.TrimSpace(filter.Status) != "" {
		where = append(where, "r.status = ?")
		args = append(args, strings.TrimSpace(filter.Status))
	}

	query := `SELECT r.id, r.created_at, r.status, r.summary, COALESCE(r.name, ''), r.profile_name, r.runner, q.model, q.launch_directory, q.raw_structured_input, COALESCE(s.raw_output_path, ''), COALESCE(s.raw_structured_output, ''), COALESCE(r.run_record_path, ''), COALESCE(r.error, '') FROM execution_runs r JOIN execution_requests q ON q.id = r.request_id LEFT JOIN execution_results s ON s.id = r.result_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY r.id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]ListedRun, 0)
	for rows.Next() {
		var run ListedRun
		if err := rows.Scan(&run.ID, &run.CreatedAt, &run.Status, &run.Summary, &run.Name, &run.ProfileName, &run.Runner, &run.Model, &run.LaunchDirectory, &run.RawStructuredInput, &run.RawOutputPath, &run.RawStructuredOutput, &run.RunRecordPath, &run.Error); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return runs, nil
}

func ensureSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS execution_requests (id INTEGER PRIMARY KEY AUTOINCREMENT, model TEXT NOT NULL, launch_directory TEXT NOT NULL, raw_structured_input TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS execution_results (id INTEGER PRIMARY KEY AUTOINCREMENT, raw_output_path TEXT NOT NULL, raw_structured_output TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS execution_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, status TEXT NOT NULL, summary TEXT NOT NULL, name TEXT, profile_name TEXT NOT NULL, runner TEXT NOT NULL, request_id INTEGER NOT NULL REFERENCES execution_requests(id), result_id INTEGER REFERENCES execution_results(id), run_record_path TEXT, error TEXT)`,
		`CREATE INDEX IF NOT EXISTS execution_runs_created_at_idx ON execution_runs(created_at)`,
		`CREATE INDEX IF NOT EXISTS execution_runs_name_idx ON execution_runs(name)`,
		`CREATE INDEX IF NOT EXISTS execution_runs_status_idx ON execution_runs(status)`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlite schema: %w", err)
		}
	}

	return nil
}

func hasResult(run Run) bool {
	return strings.TrimSpace(run.RawOutputPath) != "" || strings.TrimSpace(run.RawStructuredOutput) != ""
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return value
}
