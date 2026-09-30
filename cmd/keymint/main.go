package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rest-api/internal/auth"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://root@localhost:5432/rest-api?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pool:", err)
		os.Exit(1)
	}
	defer pool.Close()
	repo := auth.NewPostgresRepository(pool)

	acc, err := repo.FindAccount(ctx, 1)
	if err != nil {
		accID, cerr := repo.CreateAccount(ctx, "diag", auth.RoleAdmin, "free")
		if cerr != nil {
			fmt.Fprintln(os.Stderr, "create account:", cerr)
			os.Exit(1)
		}
		acc = &auth.Account{ID: accID}
	}
	raw, err := auth.GenerateKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if _, err := repo.CreateKey(ctx, acc.ID, "diag", auth.HashKey(raw), nil); err != nil {
		fmt.Fprintln(os.Stderr, "create key:", err)
		os.Exit(1)
	}
	fmt.Println(raw)
}