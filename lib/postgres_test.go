package lib

import (
	"fmt"
	"github.com/deso-protocol/core/migrate"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/go-pg/pg/v10"
	migrations "github.com/robinjoseph08/go-pg-migrations/v3"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestParsePostgresURI(t *testing.T) {
	require := require.New(t)

	// No password.
	pgURI := "postgresql://testUser@localhost:5432/testDatabase"
	pgOptions := ParsePostgresURI(pgURI)
	require.Equal(pgOptions.Addr, "localhost:5432")
	require.Equal(pgOptions.User, "testUser")
	require.Equal(pgOptions.Database, "testDatabase")
	require.Equal(pgOptions.Password, "")

	// With password.
	pgURI = "postgresql://testUser:testPassword@postgres:5432/testDatabase"
	pgOptions = ParsePostgresURI(pgURI)
	require.Equal(pgOptions.Addr, "postgres:5432")
	require.Equal(pgOptions.User, "testUser")
	require.Equal(pgOptions.Database, "testDatabase")
	require.Equal(pgOptions.Password, "testPassword")
}

func TestEmbedPg(t *testing.T) {
	db, embpg := StartTestEmbeddedPostgresDB(t, "", 5433)
	fmt.Println("Started embedded postgres")
	defer StopTestEmbeddedPostgresDB(t, db, embpg)
}

// Use this utility function to start a test DB at the beginning of your test.
// Don't forget to queue a call to StopTestEmbeddedPostgresDB after you do this.
func StartTestEmbeddedPostgresDB(t *testing.T, dataPath string, port uint32) (
	*Postgres, *embeddedpostgres.EmbeddedPostgres) {
	require := require.New(t)

	viper.SetConfigFile("../.env")
	viper.ReadInConfig()
	viper.Set("ENV", "TEST")
	viper.AutomaticEnv()

	var embeddedPostgres *embeddedpostgres.EmbeddedPostgres
	if viper.GetUint32("EMBEDDED_PG_PORT") > 0 {
		port = viper.GetUint32("EMBEDDED_PG_PORT")
	}

	// If we are in a local environment, start up embedded postgres.
	if !viper.GetBool("BUILDKITE_ENV") {
		embeddedPostgres = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Port(port).
			// Setting a DataPath will make it use the same DB every time.
			DataPath(dataPath).
			// Setting a BinariesPath makes the tests run faster because otherwise it will
			// re-download the binaries every time.
			BinariesPath("/tmp/pg_bin").
			Version(embeddedpostgres.V14).
			Logger(nil))
		require.NoError(embeddedPostgres.Start())
	} else {
		embeddedPostgres = nil
	}

	// Open a PostgreSQL database.
	dsn := viper.GetString("TEST_PG_URI")
	if dsn == "" {
		dsn = "postgresql://postgres:postgres@localhost:" + fmt.Sprint(port) + "/postgres?sslmode=disable"
	}
	db := pg.Connect(ParsePostgresURI(dsn))
	postgresDb := NewPostgres(db)

	migrate.LoadMigrations()
	err := migrations.Run(db, "migrate", []string{"", "migrate"})
	require.NoError(err)

	return postgresDb, embeddedPostgres
}

func StopTestEmbeddedPostgresDB(
	t *testing.T, db *Postgres,
	epg *embeddedpostgres.EmbeddedPostgres) {

	require := require.New(t)
	if !viper.GetBool("BUILDKITE_ENV") {
		require.NoError(epg.Stop())
	}
}
