// cmd/migrate-hash-client-secrets converts plaintext oauth_clients.secret
// values to the hashed at-rest form ("sha256:<hex>") so a DB-read breach can no
// longer hand out usable s2s credentials.
//
// Idempotent + re-runnable: rows already prefixed "sha256:" are skipped. Safe to
// run live — downstream clients keep their existing plaintext secret in config
// and it still verifies (model.OAuthClient.VerifySecret hashes the presented
// value before comparing). Run once after deploying the secret-hashing change.
//
// Usage:
//
//	go run ./cmd/migrate-hash-client-secrets            # hash plaintext rows
//	go run ./cmd/migrate-hash-client-secrets -dry-run   # report only, no writes
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/site/model"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "只统计将哈希多少行，不写入")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("connect db", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	gdb := db.DB()

	var rows []model.OAuthClient
	if err := gdb.Where("secret NOT LIKE ?", "sha256:%").Find(&rows).Error; err != nil {
		slog.Error("scan plaintext secrets", "error", err)
		os.Exit(1)
	}
	fmt.Printf("待哈希 client secret：%d 行（dry-run=%v）\n", len(rows), *dryRun)
	if *dryRun || len(rows) == 0 {
		if len(rows) == 0 {
			fmt.Println("✅ 无明文 secret（已全部哈希）")
		}
		return
	}

	hashed := 0
	for i := range rows {
		c := &rows[i]
		if err := gdb.Model(&model.OAuthClient{}).Where("id = ?", c.ID).
			Update("secret", model.HashOAuthClientSecret(c.Secret)).Error; err != nil {
			slog.Error("hash secret", "client", c.ID, "error", err)
			continue
		}
		hashed++
	}
	fmt.Printf("✅ 完成：哈希 %d / %d 行 client secret\n", hashed, len(rows))
}
