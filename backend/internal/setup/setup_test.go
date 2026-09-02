package setup

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestDecideAdminBootstrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		totalUsers int64
		adminUsers int64
		should     bool
		reason     string
	}{
		{
			name:       "empty database should create admin",
			totalUsers: 0,
			adminUsers: 0,
			should:     true,
			reason:     adminBootstrapReasonEmptyDatabase,
		},
		{
			name:       "admin exists should skip",
			totalUsers: 10,
			adminUsers: 1,
			should:     false,
			reason:     adminBootstrapReasonAdminExists,
		},
		{
			name:       "users exist without admin should skip",
			totalUsers: 5,
			adminUsers: 0,
			should:     false,
			reason:     adminBootstrapReasonUsersExistWithoutAdmin,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideAdminBootstrap(tc.totalUsers, tc.adminUsers)
			if got.shouldCreate != tc.should {
				t.Fatalf("shouldCreate=%v, want %v", got.shouldCreate, tc.should)
			}
			if got.reason != tc.reason {
				t.Fatalf("reason=%q, want %q", got.reason, tc.reason)
			}
		})
	}
}

func TestSetupDefaultAdminConcurrency(t *testing.T) {
	t.Run("simple mode admin uses higher concurrency", func(t *testing.T) {
		t.Setenv("RUN_MODE", "simple")
		if got := setupDefaultAdminConcurrency(); got != simpleModeAdminConcurrency {
			t.Fatalf("setupDefaultAdminConcurrency()=%d, want %d", got, simpleModeAdminConcurrency)
		}
	})

	t.Run("standard mode keeps existing default", func(t *testing.T) {
		t.Setenv("RUN_MODE", "standard")
		if got := setupDefaultAdminConcurrency(); got != defaultUserConcurrency {
			t.Fatalf("setupDefaultAdminConcurrency()=%d, want %d", got, defaultUserConcurrency)
		}
	})
}

func TestNeedsSetupSkipsWhenSkipSetupIsEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "true", value: "true"},
		{name: "one", value: "1"},
		{name: "yes", value: "yes"},
		{name: "trimmed mixed case true", value: "  TrUe  "},
		{name: "trimmed mixed case yes", value: "  YeS  "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATA_DIR", t.TempDir())
			t.Setenv("SKIP_SETUP", tc.value)

			if NeedsSetup() {
				t.Fatalf("NeedsSetup() = true, want false when SKIP_SETUP is enabled")
			}
		})
	}
}

func TestNeedsSetupFallsBackToFileDetectionWhenSkipSetupIsDisabled(t *testing.T) {
	tests := []struct {
		name         string
		skipSetupSet bool
		skipSetup    string
		markerFile   string
		want         bool
	}{
		{
			name: "unset without installation files",
			want: true,
		},
		{
			name:         "false without installation files",
			skipSetupSet: true,
			skipSetup:    " false ",
			want:         true,
		},
		{
			name:         "invalid value without installation files",
			skipSetupSet: true,
			skipSetup:    "enabled",
			want:         true,
		},
		{
			name:         "config file exists",
			skipSetupSet: true,
			skipSetup:    "false",
			markerFile:   ConfigFileName,
			want:         false,
		},
		{
			name:         "install lock file exists",
			skipSetupSet: true,
			skipSetup:    "invalid",
			markerFile:   InstallLockFile,
			want:         false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("DATA_DIR", dataDir)
			if tc.skipSetupSet {
				t.Setenv("SKIP_SETUP", tc.skipSetup)
			} else {
				originalValue, wasSet := os.LookupEnv("SKIP_SETUP")
				if err := os.Unsetenv("SKIP_SETUP"); err != nil {
					t.Fatalf("Unsetenv(SKIP_SETUP) error = %v", err)
				}
				t.Cleanup(func() {
					if wasSet {
						_ = os.Setenv("SKIP_SETUP", originalValue)
						return
					}
					_ = os.Unsetenv("SKIP_SETUP")
				})
			}

			if tc.markerFile != "" {
				if err := os.WriteFile(filepath.Join(dataDir, tc.markerFile), nil, 0o600); err != nil {
					t.Fatalf("WriteFile(%s) error = %v", tc.markerFile, err)
				}
			}

			if got := NeedsSetup(); got != tc.want {
				t.Fatalf("NeedsSetup() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetupMigrationTimeout(t *testing.T) {
	t.Run("uses default timeout when unset", func(t *testing.T) {
		cfg := &SetupConfig{}
		if got := cfg.migrationTimeout(); got != 60*time.Second {
			t.Fatalf("migrationTimeout()=%s, want 60s", got)
		}
	})

	t.Run("uses configured timeout", func(t *testing.T) {
		cfg := &SetupConfig{MigrationTimeoutSeconds: 300}
		if got := cfg.migrationTimeout(); got != 300*time.Second {
			t.Fatalf("migrationTimeout()=%s, want 300s", got)
		}
	})
}

func TestWriteConfigFileKeepsDefaultUserConcurrency(t *testing.T) {
	t.Setenv("RUN_MODE", "simple")
	t.Setenv("DATA_DIR", t.TempDir())

	if err := writeConfigFile(&SetupConfig{}); err != nil {
		t.Fatalf("writeConfigFile() error = %v", err)
	}

	data, err := os.ReadFile(GetConfigFilePath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if !strings.Contains(string(data), "user_concurrency: 5") {
		t.Fatalf("config missing default user concurrency, got:\n%s", string(data))
	}
}

func TestWriteConfigFileIncludesRedisUsername(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())

	if err := writeConfigFile(&SetupConfig{
		Redis: RedisConfig{
			Host:     "redis",
			Port:     6379,
			Username: "app-user",
		},
	}); err != nil {
		t.Fatalf("writeConfigFile() error = %v", err)
	}

	data, err := os.ReadFile(GetConfigFilePath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if !strings.Contains(string(data), "username: app-user") {
		t.Fatalf("config missing Redis username, got:\n%s", string(data))
	}
}

func TestBuildDatabaseConnectionDSNsUsesPostgresForBootstrap(t *testing.T) {
	cfg := &DatabaseConfig{
		Host:     "db",
		Port:     5432,
		User:     "sub2api",
		Password: "secret",
		DBName:   "sub2api",
		SSLMode:  "disable",
	}

	bootstrapDSN, targetDSN := buildDatabaseConnectionDSNs(cfg)

	bootstrapURL, err := url.Parse(bootstrapDSN)
	if err != nil {
		t.Fatalf("url.Parse(bootstrap DSN) error = %v", err)
	}
	targetURL, err := url.Parse(targetDSN)
	if err != nil {
		t.Fatalf("url.Parse(target DSN) error = %v", err)
	}
	if bootstrapURL.Path != "/postgres" {
		t.Fatalf("bootstrap DSN path = %q, want /postgres", bootstrapURL.Path)
	}
	if targetURL.Path != "/sub2api" {
		t.Fatalf("target DSN path = %q, want /sub2api", targetURL.Path)
	}
}

func TestBuildPostgresDSNPreservesEmptyAndEscapedCredentials(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		password string
	}{
		{name: "empty password", user: "sub2api"},
		{name: "reserved characters", user: "app@tenant", password: "p@ss word:/?#[]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dsn := buildPostgresDSN(&DatabaseConfig{
				Host:     "127.0.0.1",
				Port:     5432,
				User:     tc.user,
				Password: tc.password,
				SSLMode:  "disable",
			}, "target_database")
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			if parsed.User.Username() != tc.user {
				t.Fatalf("username = %q, want %q", parsed.User.Username(), tc.user)
			}
			password, passwordSet := parsed.User.Password()
			if tc.password == "" {
				if passwordSet {
					t.Fatalf("empty password should be omitted, got %q", password)
				}
			} else if !passwordSet || password != tc.password {
				t.Fatalf("password = %q (set %v), want %q", password, passwordSet, tc.password)
			}
			if parsed.Path != "/target_database" || parsed.Query().Get("sslmode") != "disable" {
				t.Fatalf("parsed DSN path/query = %q/%q", parsed.Path, parsed.RawQuery)
			}
		})
	}
}

func TestCreateAdminUserWithDBCreatesCompatibilityRoleAtomically(t *testing.T) {
	t.Setenv("RUN_MODE", "standard")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := &SetupConfig{Admin: AdminConfig{
		Email:    "admin@example.test",
		Password: "correct-horse-battery-staple",
	}}
	expectFreshAdminBootstrap(mock, cfg.Admin.Email, int64(42))

	created, reason, err := createAdminUserWithDB(context.Background(), db, cfg)
	if err != nil {
		t.Fatalf("createAdminUserWithDB() error = %v", err)
	}
	if !created {
		t.Fatal("createAdminUserWithDB() created = false, want true")
	}
	if reason != adminBootstrapReasonEmptyDatabase {
		t.Fatalf("createAdminUserWithDB() reason = %q, want %q", reason, adminBootstrapReasonEmptyDatabase)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestWithInstallationLockRunsOnlyForLockHolder(t *testing.T) {
	t.Run("acquired lock runs callback and unlocks", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
			WithArgs(installationAdvisoryLockID).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
			WithArgs(installationAdvisoryLockID).
			WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))

		called := false
		err = withInstallationLock(context.Background(), db, func() error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatalf("withInstallationLock() error = %v", err)
		}
		if !called {
			t.Fatal("withInstallationLock() did not run callback")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet SQL expectations: %v", err)
		}
	})

	t.Run("contended lock rejects callback", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
			WithArgs(installationAdvisoryLockID).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))

		called := false
		err = withInstallationLock(context.Background(), db, func() error {
			called = true
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "already in progress") {
			t.Fatalf("withInstallationLock() error = %v, want contention error", err)
		}
		if called {
			t.Fatal("withInstallationLock() ran callback without the lock")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet SQL expectations: %v", err)
		}
	})

	t.Run("callback failure still unlocks", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
			WithArgs(installationAdvisoryLockID).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
			WithArgs(installationAdvisoryLockID).
			WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))

		callbackErr := errors.New("installation failed")
		err = withInstallationLock(context.Background(), db, func() error {
			return callbackErr
		})
		if !errors.Is(err, callbackErr) {
			t.Fatalf("withInstallationLock() error = %v, want %v", err, callbackErr)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet SQL expectations: %v", err)
		}
	})
}

func TestCreateAdminUserWithDBRollsBackWhenCompatibilityRoleFails(t *testing.T) {
	t.Setenv("RUN_MODE", "standard")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := &SetupConfig{Admin: AdminConfig{
		Email:    "rollback-admin@example.test",
		Password: "correct-horse-battery-staple",
	}}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(lockUsersForAdminBootstrapSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(1) FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(1) FROM users WHERE role = $1")).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO users .* RETURNING id`).
		WithArgs(
			cfg.Admin.Email,
			sqlmock.AnyArg(),
			"admin",
			float64(0),
			defaultUserConcurrency,
			"active",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(73))
	mock.ExpectExec(regexp.QuoteMeta(deleteStaleBootstrapRoleSQL)).
		WithArgs(int64(73)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(insertBootstrapRoleSQL)).
		WithArgs(int64(73)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(verifyBootstrapRoleSQL)).
		WithArgs(int64(73)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	created, _, err := createAdminUserWithDB(context.Background(), db, cfg)
	if err == nil {
		t.Fatal("createAdminUserWithDB() error = nil, want compatibility role failure")
	}
	if created {
		t.Fatal("createAdminUserWithDB() created = true after rollback")
	}
	if !strings.Contains(err.Error(), "assign admin compatibility role") {
		t.Fatalf("createAdminUserWithDB() error = %q, want compatibility role context", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestCreateAdminUserWithDBRepeatDoesNotDuplicateAdminOrRole(t *testing.T) {
	t.Setenv("RUN_MODE", "standard")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := &SetupConfig{Admin: AdminConfig{
		Email:    "repeat-admin@example.test",
		Password: "correct-horse-battery-staple",
	}}
	expectFreshAdminBootstrap(mock, cfg.Admin.Email, int64(91))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(lockUsersForAdminBootstrapSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(1) FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(1) FROM users WHERE role = $1")).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()

	created, _, err := createAdminUserWithDB(context.Background(), db, cfg)
	if err != nil || !created {
		t.Fatalf("first createAdminUserWithDB() = (created %v, error %v), want (true, nil)", created, err)
	}
	created, reason, err := createAdminUserWithDB(context.Background(), db, cfg)
	if err != nil {
		t.Fatalf("second createAdminUserWithDB() error = %v", err)
	}
	if created {
		t.Fatal("second createAdminUserWithDB() created = true, want false")
	}
	if reason != adminBootstrapReasonAdminExists {
		t.Fatalf("second createAdminUserWithDB() reason = %q, want %q", reason, adminBootstrapReasonAdminExists)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func expectFreshAdminBootstrap(mock sqlmock.Sqlmock, email string, userID int64) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(lockUsersForAdminBootstrapSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(1) FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(1) FROM users WHERE role = $1")).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO users .* RETURNING id`).
		WithArgs(
			email,
			sqlmock.AnyArg(),
			"admin",
			float64(0),
			defaultUserConcurrency,
			"active",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(userID))
	mock.ExpectExec(regexp.QuoteMeta(deleteStaleBootstrapRoleSQL)).
		WithArgs(userID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(insertBootstrapRoleSQL)).
		WithArgs(userID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(verifyBootstrapRoleSQL)).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()
}
