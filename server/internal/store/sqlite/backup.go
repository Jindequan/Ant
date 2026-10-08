package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

const backupBudget = 30 * time.Minute

type restoreTimestamp domain.Millis

func (c restoreTimestamp) Now() domain.Millis { return domain.Millis(c) }

// Backup writes a consistent SQLite snapshot to destination. The output is
// verified before it becomes visible at the requested path, and an existing
// destination is never replaced. Because this runs on the writer loop, the
// snapshot cannot race an in-flight write command.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if ctx == nil {
		return errors.New("sqlite: backup context is nil")
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return errors.New("sqlite: backup destination is empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("sqlite: resolve backup destination: %w", err)
	}
	parent := filepath.Dir(absDestination)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("sqlite: inspect backup directory: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("sqlite: backup directory must be a directory with no group or world permissions")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("sqlite: resolve backup directory: %w", err)
	}
	absDestination = filepath.Join(resolvedParent, filepath.Base(absDestination))
	if _, err := os.Lstat(absDestination); err == nil {
		return fmt.Errorf("sqlite: backup destination already exists: %s", absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sqlite: inspect backup destination: %w", err)
	}

	temp, err := os.CreateTemp(resolvedParent, ".ant-backup-*.sqlite")
	if err != nil {
		return fmt.Errorf("sqlite: create temporary backup: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("sqlite: set temporary backup permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("sqlite: close temporary backup: %w", err)
	}
	defer os.Remove(tempPath)

	err = s.submitMaintenance(ctx, backupBudget, func(maintenanceCtx context.Context, db *sql.DB) error {
		var source string
		rows, err := db.QueryContext(maintenanceCtx, "PRAGMA database_list")
		if err != nil {
			return fmt.Errorf("sqlite: inspect source database: %w", err)
		}
		for rows.Next() {
			var seq int
			var name, file string
			if err := rows.Scan(&seq, &name, &file); err != nil {
				_ = rows.Close()
				return fmt.Errorf("sqlite: read source database path: %w", err)
			}
			if name == "main" {
				source = file
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("sqlite: list source databases: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("sqlite: close source database list: %w", err)
		}
		if source != "" {
			resolvedSource, err := filepath.EvalSymlinks(source)
			if err == nil && filepath.Clean(resolvedSource) == filepath.Clean(absDestination) {
				return errors.New("sqlite: backup destination is the source database")
			}
		}
		if _, err := db.ExecContext(maintenanceCtx, "VACUUM INTO ?", tempPath); err != nil {
			return fmt.Errorf("sqlite: create consistent backup: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("sqlite: backup: %w", err)
	}

	if err := verifyBackup(tempPath); err != nil {
		return err
	}
	file, err := os.OpenFile(tempPath, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("sqlite: open completed backup: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("sqlite: set backup permissions: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sqlite: sync backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("sqlite: close backup: %w", err)
	}
	// Linking publishes the already-complete file atomically and fails if the
	// destination appeared after the initial check; it never overwrites it.
	if err := os.Link(tempPath, absDestination); err != nil {
		return fmt.Errorf("sqlite: publish backup: %w", err)
	}
	dir, err := os.Open(resolvedParent)
	if err != nil {
		_ = os.Remove(absDestination)
		return fmt.Errorf("sqlite: open backup directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		_ = os.Remove(absDestination)
		return fmt.Errorf("sqlite: sync backup directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		_ = os.Remove(absDestination)
		return fmt.Errorf("sqlite: close backup directory: %w", err)
	}
	return nil
}

// RestoreBackup installs a verified snapshot while the target server is
// stopped. Before the restored database can be opened by the application, it
// durably closes restore_guard and writes a system audit/receipt into the
// restored copy. Thus a guard value captured in an old snapshot cannot
// silently reopen dispatch through this supported restore path.
func RestoreBackup(ctx context.Context, sourcePath, destinationPath string, now domain.Millis) error {
	if ctx == nil || now < 0 {
		return errors.New("sqlite: restore context and non-negative timestamp are required")
	}
	var err error
	sourcePath, err = filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return fmt.Errorf("sqlite: resolve restore source: %w", err)
	}
	destinationPath, err = filepath.Abs(strings.TrimSpace(destinationPath))
	if err != nil {
		return fmt.Errorf("sqlite: resolve restore destination: %w", err)
	}
	if sourcePath == destinationPath {
		return errors.New("sqlite: restore source and destination must differ")
	}
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("sqlite: inspect restore source: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() || sourceInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("sqlite: restore source must be a private regular file")
	}
	if err := verifyBackup(sourcePath); err != nil {
		return fmt.Errorf("sqlite: verify restore source: %w", err)
	}
	parent := filepath.Dir(destinationPath)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("sqlite: inspect restore directory: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("sqlite: restore directory must have no group or world permissions")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("sqlite: resolve restore directory: %w", err)
	}
	destinationPath = filepath.Join(resolvedParent, filepath.Base(destinationPath))
	if err := rejectSQLiteSidecars(destinationPath); err != nil {
		return err
	}
	if info, err := os.Lstat(destinationPath); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("sqlite: restore destination must be a regular file")
		}
		if os.SameFile(sourceInfo, info) {
			return errors.New("sqlite: restore source and destination refer to the same file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sqlite: inspect restore destination: %w", err)
	}

	temp, err := os.CreateTemp(resolvedParent, ".ant-restore-*.sqlite")
	if err != nil {
		return fmt.Errorf("sqlite: create restore staging file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sqlite: protect restore staging file: %w", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("sqlite: open restore source: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), source); err != nil {
		_ = source.Close()
		_ = temp.Close()
		return fmt.Errorf("sqlite: stage restore source: %w", err)
	}
	if err := source.Close(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sqlite: close restore source: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sqlite: sync restore staging file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("sqlite: close restore staging file: %w", err)
	}
	if err := verifyBackup(tempPath); err != nil {
		return fmt.Errorf("sqlite: verify staged restore: %w", err)
	}

	st, err := Open(tempPath, Config{Clock: restoreTimestamp(now)})
	if err != nil {
		return fmt.Errorf("sqlite: open staged restore: %w", err)
	}
	if err := Migrate(ctx, st); err != nil {
		_ = st.Close(context.Background())
		return fmt.Errorf("sqlite: migrate staged restore: %w", err)
	}
	sourceDigest := hex.EncodeToString(hash.Sum(nil))
	commandID := domain.ID("restore-" + sourceDigest[:24])
	writeErr := st.SubmitWrite(ctx, store.WriteCommand{ID: "restore-guard/" + string(commandID), Control: true, Run: func(tx store.WriterTx) error {
		guardTx, ok := tx.(store.RestoreGuardWriterTx)
		if !ok {
			return store.ErrUnavailable
		}
		version, enabled, err := guardTx.GetControlVersion()
		if err != nil {
			return err
		}
		if enabled == 0 {
			applied, err := guardTx.SetRestoreGuard(version, true)
			if err != nil {
				return err
			}
			if !applied {
				return errors.New("sqlite: restore guard changed during restore")
			}
			version++
		}
		result, err := json.Marshal(struct {
			Enabled        bool   `json:"enabled"`
			ControlVersion int64  `json:"controlVersion"`
			SourceSHA256   string `json:"sourceSha256"`
		}{true, version, sourceDigest})
		if err != nil {
			return err
		}
		if err := tx.InsertReceipt(store.ReceiptRecord{Scope: "runtime/restore-guard", CommandID: commandID, Kind: "restore_guard", InputHash: sourceDigest, ResultJSON: result, CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertAudit(store.AuditRecord{AuditID: domain.ID("restore/" + string(commandID)), PrincipalID: "system:restore", Namespace: "system", Action: "runtime.restore_guard.close", TargetRef: domain.ID(destinationPath), EvidenceRef: domain.ID("sha256:" + sourceDigest), CreatedAt: now})
	}})
	closeErr := st.Close(context.Background())
	if writeErr != nil {
		return fmt.Errorf("sqlite: close dispatch after restore: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("sqlite: close staged restore: %w", closeErr)
	}
	if err := verifyBackup(tempPath); err != nil {
		return fmt.Errorf("sqlite: verify guarded restore: %w", err)
	}
	file, err := os.OpenFile(tempPath, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("sqlite: open guarded restore: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sqlite: sync guarded restore: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("sqlite: close guarded restore file: %w", err)
	}

	previousPath := ""
	if _, err := os.Lstat(destinationPath); err == nil {
		previousPath = destinationPath + ".pre-restore-" + sourceDigest[:16]
		if _, err := os.Lstat(previousPath); err == nil {
			return errors.New("sqlite: pre-restore destination already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(destinationPath, previousPath); err != nil {
			return fmt.Errorf("sqlite: preserve previous database: %w", err)
		}
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		if previousPath != "" {
			_ = os.Rename(previousPath, destinationPath)
		}
		return fmt.Errorf("sqlite: publish guarded restore: %w", err)
	}
	dir, err := os.Open(resolvedParent)
	if err != nil {
		return fmt.Errorf("sqlite: open restore directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("sqlite: sync restore directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("sqlite: close restore directory: %w", err)
	}
	return nil
}

func rejectSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("sqlite: restore destination has active SQLite sidecar %s; stop the server first", suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sqlite: inspect restore sidecar: %w", err)
		}
	}
	return nil
}

func verifyBackup(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("sqlite: open backup for verification: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("sqlite: check backup integrity: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("sqlite: backup integrity check failed: %s", result)
	}
	return nil
}
