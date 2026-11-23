package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"rivosnap/internal/config"
	"rivosnap/internal/db"
	"rivosnap/internal/testguard"
)

func TestEmbeddedMigrationsChecksums(t *testing.T) {
	list, err := db.LoadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("LoadEmbeddedMigrations: %v", err)
	}
	if len(list) < 3 {
		t.Fatalf("expected at least 3 embedded migrations, got %d", len(list))
	}
	expected := map[string]string{
		"202611190001_initial":              "db8d930f1f8ce52b2bf71ef6c060019ce4b9f07bbb84e74d6037cf848e01ed66",
		"202611190002_authorization_window": "4ea50f39fe7ec571be43c919d38b63c80192b1b314e1dbb41b63148adca1c966",
		"202611200001_video_support":        "47c5467f66d0317d2f13dc80639ff48819e95fe654f887829c6f0177f164f8bf",
		"202611210001_timestamptz":          "e8d2fd066de4b34d3aec6bcc9d54cbe14cacd9f60d08490011283f189398ca0b",
	}
	for _, m := range list {
		if want, ok := expected[m.Name]; ok && m.Checksum != want {
			t.Fatalf("checksum mismatch for %s: got %s, want %s", m.Name, m.Checksum, want)
		}
	}
}

func TestMigrationLifecycleIntegration(t *testing.T) {
	testguard.Require(t)
	ctx := context.Background()
	env := config.Env{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		TokenEncryptionKey: strings.Repeat("1", 64),
	}
	database, err := db.Connect(ctx, env)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer database.Close()

	// 1. Re-running Go migrations on a Go-migrated database:
	// test-integration.sh runs `rivosnap migrate` first, so _prisma_migrations already holds every embedded migration.
	// Insert a LineGroup with distinct membershipAt and enabledSince to verify 202611190002_authorization_window is NOT re-run.
	t1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	_, _ = database.Pool.Exec(ctx, `DELETE FROM "LineGroup" WHERE "lineGroupId" = 'mig-test-group'`)
	if _, err := database.Pool.Exec(
		ctx,
		`INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "membershipStatus", "membershipAt", "enabledSince", "lastEventAt", "createdAt", "updatedAt")
		 VALUES ('mig-id-1', 'mig-test-group', true, 'JOINED', $1, $2, $2, NOW(), NOW())`,
		t1, t2,
	); err != nil {
		t.Fatalf("insert test LineGroup: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on Prisma-initialized DB failed: %v", err)
	}
	var gotEnabledSince time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT "enabledSince" FROM "LineGroup" WHERE "lineGroupId" = 'mig-test-group'`).Scan(&gotEnabledSince); err != nil {
		t.Fatalf("select enabledSince: %v", err)
	}
	if !gotEnabledSince.Equal(t2) {
		t.Fatalf("expected enabledSince %v to be preserved (migration not re-executed), got %v", t2, gotEnabledSince)
	}
	_, _ = database.Pool.Exec(ctx, `DELETE FROM "LineGroup" WHERE "lineGroupId" = 'mig-test-group'`)

	// 2. Incremental new migration path:
	list, err := db.LoadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	withExtra := append(append([]db.MigrationFile{}, list...), db.MigrationFile{
		Name:     "202611219999_test_marker",
		SQL:      `CREATE TABLE "MigrationTestMarker" ("id" INT PRIMARY KEY);`,
		Checksum: strings.Repeat("a", 64),
	})
	if err := db.RunMigrations(ctx, database.Pool, withExtra); err != nil {
		t.Fatalf("RunMigrations with extra migration: %v", err)
	}
	var markerRecorded int
	if err := database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "_prisma_migrations" WHERE "migration_name" = '202611219999_test_marker' AND "finished_at" IS NOT NULL`).Scan(&markerRecorded); err != nil || markerRecorded != 1 {
		t.Fatalf("expected new migration recorded once, got %d (err=%v)", markerRecorded, err)
	}
	// Clean up test marker
	_, _ = database.Pool.Exec(ctx, `DROP TABLE IF EXISTS "MigrationTestMarker"`)
	_, _ = database.Pool.Exec(ctx, `DELETE FROM "_prisma_migrations" WHERE "migration_name" = '202611219999_test_marker'`)

	// 3. Fresh empty database path:
	// Drop public schema and recreate from scratch using only Go embedded migrations
	if _, err := database.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset public schema: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on fresh DB failed: %v", err)
	}
	var count int
	if err := database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "_prisma_migrations" WHERE "finished_at" IS NOT NULL`).Scan(&count); err != nil || count != len(list) {
		t.Fatalf("expected %d recorded migrations on fresh DB, got %d (err=%v)", len(list), count, err)
	}
}

func TestSessionTimezoneIsUTC(t *testing.T) {
	testguard.Require(t)
	ctx := context.Background()
	database, err := db.Connect(ctx, config.Env{DatabaseURL: os.Getenv("DATABASE_URL")})
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer database.Close()
	var tz string
	if err := database.Pool.QueryRow(ctx, `SHOW timezone`).Scan(&tz); err != nil {
		t.Fatalf("SHOW timezone: %v", err)
	}
	if tz != "UTC" {
		t.Fatalf("expected session timezone UTC, got %q", tz)
	}
}

func TestAllTimestampColumnsAreTimestamptz(t *testing.T) {
	testguard.Require(t)
	ctx := context.Background()
	database, err := db.Connect(ctx, config.Env{DatabaseURL: os.Getenv("DATABASE_URL")})
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	rows, err := database.Pool.Query(ctx, `SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type = 'timestamp without time zone' AND table_name <> '_prisma_migrations'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	var naive []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		naive = append(naive, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(naive) > 0 {
		t.Fatalf("columns still timestamp without time zone: %s", strings.Join(naive, ", "))
	}
}
