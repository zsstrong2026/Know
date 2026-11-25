package database

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source"
	"github.com/stretchr/testify/require"
)

// Check both migration registries without requiring a live PostgreSQL server.
// Independently merged migrations must not reuse a version in either dialect.
func TestMigrationSourcesHaveUniqueVersions(t *testing.T) {
	// Relative file:// URLs keep this portable: an absolute Windows path would
	// be parsed as a host with a port ("invalid port :\\知识库... after host").
	chdirAndRestore(t, sqliteRepoRoot(t))
	for _, dialect := range []string{"sqlite", "versioned"} {
		t.Run(dialect, func(t *testing.T) {
			migrations, err := source.Open("file://migrations/" + dialect)
			require.NoError(t, err)
			require.NoError(t, migrations.Close())
		})
	}
}
