package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"nodevas/internal/db"
)

// database reports where a workspace's operational data lives, and moves it
// from the SQLite file to the PostgreSQL server named by NODEVAS_DATABASE_URL.
func database(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("db", flag.ExitOnError)
	projectFlag := fs.String("project", ".", "workspace directory")
	replace := fs.Bool("replace", false,
		"with copy-to-postgres: delete what the PostgreSQL database already holds before copying")
	_ = fs.Parse(args[1:])

	root, err := filepath.Abs(*projectFlag)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	switch action {
	case "status":
		handle, err := db.Open(root)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		defer handle.Close()
		fmt.Printf("backend: %s\nlocation: %s\n", handle.Backend(), handle.Path())
		if handle.Backend() == db.Postgres {
			fmt.Printf("search index: %s\n", handle.Local().Path())
		}
	case "copy-to-postgres":
		rawURL, err := db.URLFromEnv(os.LookupEnv)
		if err != nil {
			log.Fatal(err)
		}
		if rawURL == "" {
			log.Fatalf("set %s or %s to the PostgreSQL server to copy into", db.URLEnv, db.URLFileEnv)
		}
		src, err := db.OpenAt(db.PathFor(root))
		if err != nil {
			log.Fatalf("workspace database: %v", err)
		}
		defer src.Close()
		// No fallback here: a copy that quietly ran against nothing is worse
		// than one that says the server is down.
		dst, err := db.OpenPostgres(rawURL, 0)
		if err != nil {
			log.Fatalf("postgres: %v", err)
		}
		defer dst.Close()
		counts, err := db.CopyToPostgres(ctx, src, dst, *replace)
		if err != nil {
			log.Fatalf("copy: %v", err)
		}
		tables := make([]string, 0, len(counts))
		for table := range counts {
			tables = append(tables, table)
		}
		sort.Strings(tables)
		fmt.Printf("copied %s -> %s\n", src.Path(), dst.Path())
		for _, table := range tables {
			fmt.Printf("  %-13s %d\n", table, counts[table])
		}
	default:
		usage()
		os.Exit(2)
	}
}
