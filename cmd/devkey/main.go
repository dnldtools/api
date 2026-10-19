package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"rest-api/internal/auth"
	"rest-api/internal/database"
	"rest-api/internal/plans"
	"rest-api/pkg/env"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "devkey: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		name      = flag.String("name", "dev", "account name")
		role      = flag.String("role", string(auth.RoleUser), "account role (user|admin)")
		plan      = flag.String("plan", string(plans.PlanPro), "plan (trial|free|pro)")
		keyName   = flag.String("key-name", "dev-cli", "API key name")
		customKey = flag.String("custom", "", "custom API key string (skip random generation; min 8 chars, a-z A-Z 0-9 _ - .)")
		accountID = flag.Int64("account-id", 0, "existing account id to attach the key to (0 = create new account)")
		expires   = flag.Duration("expires", 0, "optional key expiry duration (e.g. 720h); 0 = no expiry")
		dbURL     = flag.String("database-url", "", "PostgreSQL URL (defaults to DATABASE_URL)")
	)
	flag.Parse()

	url := *dbURL
	if url == "" {
		url = env.Get("DATABASE_URL", "")
	}
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required (set DATABASE_URL or pass -database-url)")
	}

	roleValue := auth.Role(*role)
	switch roleValue {
	case auth.RoleUser, auth.RoleAdmin:
	default:
		return fmt.Errorf("unknown role %q (want user or admin)", *role)
	}

	planValue := plans.Plan(*plan)
	if _, ok := plans.Defaults()[planValue]; !ok {
		return fmt.Errorf("unknown plan %q (want trial, free, or pro)", *plan)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := database.Open(ctx, database.Config{
		URL:            url,
		MaxConns:       4,
		MinConns:       0,
		ConnectTimeout: 10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := database.Migrate(ctx, db.Pool()); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	repo := auth.NewPostgresRepository(db.Pool())

	accID := *accountID
	if accID > 0 {
		acc, err := repo.FindAccount(ctx, accID)
		if err != nil {
			return fmt.Errorf("find account %d: %w", accID, err)
		}
		accID = acc.ID
	} else {
		accID, err = repo.CreateAccount(ctx, *name, roleValue, string(planValue))
		if err != nil {
			return fmt.Errorf("create account: %w", err)
		}
	}

	var rawKey string
	if *customKey != "" {
		if err := auth.ValidateCustomKey(*customKey); err != nil {
			return err
		}
		rawKey = *customKey
	} else {
		rawKey, err = auth.GenerateKey()
		if err != nil {
			return fmt.Errorf("generate key: %w", err)
		}
	}

	var expiresAt *time.Time
	if *expires > 0 {
		t := time.Now().UTC().Add(*expires)
		expiresAt = &t
	}

	keyID, err := repo.CreateKey(ctx, accID, *keyName, auth.HashKey(rawKey), expiresAt)
	if err != nil {
		return fmt.Errorf("create key: %w", err)
	}

	fmt.Printf("account_id: %d\n", accID)
	fmt.Printf("api_key_id: %d\n", keyID)
	fmt.Printf("api_key:    %s\n", rawKey)
	fmt.Println()
	fmt.Println("Store this API key securely — it will not be shown again.")
	fmt.Printf("Use it as:  X-API-Key: %s\n", rawKey)

	return nil
}