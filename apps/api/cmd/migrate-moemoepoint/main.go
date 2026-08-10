package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/auth/model"
	"api/pkg/config"
	"api/pkg/logger"
)

const (
	migrationNote   = "从 鲲 Galgame 论坛 和 鲲 Galgame 补丁 继承"
	migrationSource = "oauth"
)

const candidateSQL = `
	FROM users u
	LEFT JOIN (
		SELECT user_id, SUM(delta) AS logged
		FROM moemoepoint_log
		GROUP BY user_id
	) l ON l.user_id = u.id
	WHERE u.moemoepoint - COALESCE(l.logged, 0) <> 0
	  AND NOT EXISTS (
		SELECT 1 FROM moemoepoint_log m
		WHERE m.idempotency_key = 'oauth:migration:v1:' || u.id
	  )`

func main() {
	dryRun := flag.Bool("dry-run", false, "只统计将写入多少条，不实际写入")
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

	var preview struct {
		Users      int64
		TotalDelta int64
	}
	if err := gdb.Raw(
		`SELECT COUNT(*) AS users, COALESCE(SUM(u.moemoepoint - COALESCE(l.logged, 0)), 0) AS total_delta ` + candidateSQL,
	).Scan(&preview).Error; err != nil {
		slog.Error("preview query", "error", err)
		os.Exit(1)
	}
	fmt.Printf("待回填继承流水：%d 个用户，合计 delta=%d（dry-run=%v）\n",
		preview.Users, preview.TotalDelta, *dryRun)
	if *dryRun {
		return
	}
	if preview.Users == 0 {
		fmt.Println("✅ 无待回填用户（已全部迁移或余额均已被流水解释）")
		return
	}

	res := gdb.Exec(`
		INSERT INTO moemoepoint_log
			(user_id, delta, reason, source_app, ref, actor_user_id, idempotency_key, note, created_at)
		SELECT u.id,
		       u.moemoepoint - COALESCE(l.logged, 0),
		       ?, ?, '', 0,
		       'oauth:migration:v1:' || u.id,
		       ?, now()
		`+candidateSQL+`
		ON CONFLICT (idempotency_key) DO NOTHING`,
		model.MoemoepointReasonMigration, migrationSource, migrationNote,
	)
	if res.Error != nil {
		slog.Error("backfill insert", "error", res.Error)
		os.Exit(1)
	}
	fmt.Printf("✅ 完成：回填 %d 条继承流水（reason=%s，note=%q）\n",
		res.RowsAffected, model.MoemoepointReasonMigration, migrationNote)
}
