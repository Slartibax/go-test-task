package config

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
		want    Config
	}{
		{
			name: "comments blanks quotes and export",
			body: `
# comment
APP_NAME=ignored

export DB_HOST=database
DB_PORT="5432"
DB_DATABASE='go_test_task'
DB_USERNAME=go_test_task
DB_PASSWORD="p@ss=word"
`,
			want: Config{
				HTTPAddr:   ":8080",
				DBHost:     "database",
				DBPort:     "5432",
				DBDatabase: "go_test_task",
				DBUsername: "go_test_task",
				DBPassword: "p@ss=word",
				DBMaxConns: defaultMaxConns,
				DBMinConns: defaultMinConns,
			},
		},
		{
			name: "explicit http addr and pool",
			body: "HTTP_ADDR=:9090\nDB_HOST=db\nDB_PORT=5432\nDB_DATABASE=app\nDB_USERNAME=app\nDB_MAX_CONNS=20\nDB_MIN_CONNS=4\n",
			want: Config{
				HTTPAddr:   ":9090",
				DBHost:     "db",
				DBPort:     "5432",
				DBDatabase: "app",
				DBUsername: "app",
				DBMaxConns: 20,
				DBMinConns: 4,
			},
		},
		{
			name:    "missing database",
			body:    "DB_HOST=database\n",
			wantErr: "DB_HOST, DB_PORT, DB_DATABASE and DB_USERNAME are required",
		},
		{
			name:    "line without separator",
			body:    "DB_HOST\n",
			wantErr: `unexpected character "\n" in variable name`,
		},
		{
			name:    "bad pool size",
			body:    "DB_HOST=db\nDB_PORT=5432\nDB_DATABASE=app\nDB_USERNAME=app\nDB_MAX_CONNS=nope\n",
			wantErr: "DB_MAX_CONNS must be a non-negative integer",
		},
		{
			name:    "min conns above max",
			body:    "DB_HOST=db\nDB_PORT=5432\nDB_DATABASE=app\nDB_USERNAME=app\nDB_MAX_CONNS=4\nDB_MIN_CONNS=10\n",
			wantErr: "DB_MIN_CONNS must not exceed DB_MAX_CONNS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), ".env")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))

			got, err := loadWithEnvLookup(path, lookupWithoutProcessEnv)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	_, err := Load(filepath.Join(t.TempDir(), "missing.env"))
	require.Error(t, err)
}

func TestPostgresDSN(t *testing.T) {
	t.Parallel()

	cfg := Config{
		DBHost:     "database",
		DBPort:     "5432",
		DBDatabase: "go_test_task",
		DBUsername: "go_test_task",
		DBPassword: "p@ss word",
	}
	parsed, err := url.Parse(cfg.PostgresDSN())
	require.NoError(t, err)
	password, ok := parsed.User.Password()
	require.True(t, ok)
	require.Equal(t, cfg.DBUsername, parsed.User.Username())
	require.Equal(t, cfg.DBPassword, password)
	require.Equal(t, "postgres", parsed.Scheme)
	require.Equal(t, "database:5432", parsed.Host)
	require.Equal(t, "/go_test_task", parsed.Path)
	require.Equal(t, "disable", parsed.Query().Get("sslmode"))
}

func TestLoadEnvExample(t *testing.T) {
	t.Parallel()

	cfg, err := loadWithEnvLookup(filepath.Join("..", "..", ".env.example"), lookupWithoutProcessEnv)
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "database", cfg.DBHost)
	require.Equal(t, "go_test_task", cfg.DBDatabase)
	require.Equal(t, int32(40), cfg.DBMaxConns)
	require.Equal(t, int32(10), cfg.DBMinConns)
	require.NotEmpty(t, cfg.PostgresDSN())
}

func TestLoadPrefersProcessEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "HTTP_ADDR=:8080\nDB_HOST=database\nDB_PORT=5432\nDB_DATABASE=app\nDB_USERNAME=app\nDB_PASSWORD=file\nDB_MAX_CONNS=40\nDB_MIN_CONNS=10\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	t.Setenv("HTTP_ADDR", ":9091")
	t.Setenv("DB_HOST", "from-env")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("DB_DATABASE", "override")
	t.Setenv("DB_USERNAME", "env-user")
	t.Setenv("DB_PASSWORD", "env-pass")
	t.Setenv("DB_MAX_CONNS", "6")
	t.Setenv("DB_MIN_CONNS", "2")

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, Config{
		HTTPAddr:   ":9091",
		DBHost:     "from-env",
		DBPort:     "5433",
		DBDatabase: "override",
		DBUsername: "env-user",
		DBPassword: "env-pass",
		DBMaxConns: 6,
		DBMinConns: 2,
	}, cfg)
}

func lookupWithoutProcessEnv(string) (string, bool) {
	return "", false
}
