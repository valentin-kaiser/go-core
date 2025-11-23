package database

import (
	"database/sql"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/version"
)

// Step represents a migration step with a version and an action to be performed
// The action is a function that takes a sql.DB instance and returns an error
type Step struct {
	Version version.Release
	Action  func(db *sql.DB) error
}

// RegisterMigrationStep registers a new migration step with a version and an action
// The action is a function that takes a sql.DB instance and returns an error
// The version is a struct that must contain the Git tag and commit hash of the migration step
// Example:
//
//	db := database.New("mydb")
//	db.RegisterMigrationStep(version.Release{
//		GitTag:    "v1.0.0",
//		GitCommit: "abc123",
//	}, func(db *sql.DB) error {
//		_, err := db.Exec(`CREATE TABLE users (
//			id INTEGER PRIMARY KEY,
//			name TEXT NOT NULL
//		)`)
//		return err
//	})
func (d *Database) RegisterMigrationStep(version version.Release, action func(db *sql.DB) error) {
	d.migrationMutex.Lock()
	defer d.migrationMutex.Unlock()

	if _, ok := d.migrationSteps[version.GitTag]; !ok {
		d.migrationSteps[version.GitTag] = make(map[string][]Step)
	}

	if _, ok := d.migrationSteps[version.GitTag][version.GitCommit]; !ok {
		d.migrationSteps[version.GitTag][version.GitCommit] = make([]Step, 0)
	}

	if version.GitTag == "" || version.GitCommit == "" {
		panic(apperror.NewErrorf("version information is not set"))
	}

	d.migrationSteps[version.GitTag][version.GitCommit] = append(d.migrationSteps[version.GitTag][version.GitCommit], Step{
		Version: version,
		Action:  action,
	})
}

// setup initializes the database schema with the version table
func setup(db *sql.DB, driver string) error {
	var createTableSQL string

	switch driver {
	case "sqlite":
		createTableSQL = `
			CREATE TABLE IF NOT EXISTS releases (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				git_tag TEXT NOT NULL,
				git_commit TEXT NOT NULL,
				git_short TEXT,
				build_date TEXT,
				go_version TEXT,
				platform TEXT,
				UNIQUE(git_tag, git_commit, build_date, go_version, platform)
			);
		`
	case "mysql", "mariadb":
		createTableSQL = `
			CREATE TABLE IF NOT EXISTS releases (
				id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
				git_tag VARCHAR(255) NOT NULL,
				git_commit VARCHAR(255) NOT NULL,
				git_short VARCHAR(255),
				build_date VARCHAR(255),
				go_version VARCHAR(255),
				platform VARCHAR(255),
				UNIQUE KEY unique_version (git_tag, git_commit, build_date, go_version, platform)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
		`
	case "postgres":
		createTableSQL = `
			CREATE TABLE IF NOT EXISTS releases (
				id BIGSERIAL PRIMARY KEY,
				git_tag VARCHAR(255) NOT NULL,
				git_commit VARCHAR(255) NOT NULL,
				git_short VARCHAR(255),
				build_date VARCHAR(255),
				go_version VARCHAR(255),
				platform VARCHAR(255),
				UNIQUE(git_tag, git_commit, build_date, go_version, platform)
			);
		`
	default:
		return apperror.NewErrorf("unsupported driver for setup: %s", driver)
	}

	_, err := db.Exec(createTableSQL)
	if err != nil {
		return apperror.NewError("failed to create releases table").AddError(err)
	}

	return nil
}

// getMigrationSteps returns all registered migration steps for this database instance
func (d *Database) getMigrationSteps() [][]Step {
	d.migrationMutex.RLock()
	defer d.migrationMutex.RUnlock()

	steps := make([][]Step, 0)
	for _, step := range d.migrationSteps {
		for _, s := range step {
			steps = append(steps, s)
		}
	}

	return steps
}

// insertVersion inserts a version record into the database
func insertVersion(db *sql.DB, driver string, v version.Release) error {
	var insertSQL string

	switch driver {
	case "sqlite":
		insertSQL = `
			INSERT INTO releases (git_tag, git_commit, git_short, build_date, go_version, platform)
			VALUES (?, ?, ?, ?, ?, ?)
		`
	case "mysql", "mariadb":
		insertSQL = `
			INSERT INTO releases (git_tag, git_commit, git_short, build_date, go_version, platform)
			VALUES (?, ?, ?, ?, ?, ?)
		`
	case "postgres":
		insertSQL = `
			INSERT INTO releases (git_tag, git_commit, git_short, build_date, go_version, platform)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
	default:
		return apperror.NewErrorf("unsupported driver for insertVersion: %s", driver)
	}

	_, err := db.Exec(insertSQL,
		v.GitTag,
		v.GitCommit,
		v.GitShort,
		v.BuildDate,
		v.GoVersion,
		v.Platform,
	)
	if err != nil {
		return apperror.NewError("failed to insert version").AddError(err)
	}

	return nil
}

// versionExists checks if a version already exists in the database
func versionExists(db *sql.DB, driver string, gitTag, gitCommit string) (bool, error) {
	var selectSQL string

	switch driver {
	case "sqlite", "mysql", "mariadb":
		selectSQL = "SELECT COUNT(*) FROM releases WHERE git_tag = ? AND git_commit = ?"
	case "postgres":
		selectSQL = "SELECT COUNT(*) FROM releases WHERE git_tag = $1 AND git_commit = $2"
	default:
		return false, apperror.NewErrorf("unsupported driver for versionExists: %s", driver)
	}

	var count int
	err := db.QueryRow(selectSQL, gitTag, gitCommit).Scan(&count)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, apperror.NewError("failed to check version existence").AddError(err)
	}

	return count > 0, nil
}
