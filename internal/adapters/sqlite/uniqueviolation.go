package sqlite

import (
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	driver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// isUniqueConstraintViolation reports whether err is SQLite's own UNIQUE or
// PRIMARY KEY constraint failure, decided from the driver's typed result code
// (never from the error's message text): the one fact "an insert collided
// with a row that already exists" the application layer can act on.
func isUniqueConstraintViolation(err error) bool {
	var driverErr *driver.Error
	if !errors.As(err, &driverErr) {
		return false
	}
	switch driverErr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
		return true
	default:
		return false
	}
}

// mapAlreadyExists converts a UNIQUE/PRIMARY KEY failure of an INSERT into
// the typed ports.ErrPersistenceAlreadyExists this codebase already uses for
// "this record is already there" (V9-09, closing LIM-06 and the
// `definition create` duplicate-id gap): the sentinel is wrapped with a
// description of what collided (what, e.g. "repository r-1"), so callers
// branch on errors.Is and an operator reads a precise message instead of
// MapSQLiteError's opaque "sqlite: unexpected error". Any other error is
// handed to MapSQLiteError unchanged, exactly as before.
//
// This is the backstop for a race a pre-insert existence check cannot close
// (two processes sharing one database file); the explicit check in the
// callers is what produces the common-case answer.
func mapAlreadyExists(err error, what string) error {
	if isUniqueConstraintViolation(err) {
		return fmt.Errorf("%w: %s", ports.ErrPersistenceAlreadyExists, what)
	}
	return MapSQLiteError(err)
}
