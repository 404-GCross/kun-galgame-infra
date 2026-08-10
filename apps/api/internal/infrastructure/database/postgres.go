package database

import (
	"log"
	"os"
	"time"

	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type PostgresDB struct {
	db *gorm.DB
}

func NewPostgresDB(cfg config.DatabaseConfig) (*PostgresDB, error) {
	gormConfig := &gorm.Config{}

	gormConfig.Logger = gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		},
	)

	db, err := gorm.Open(postgres.Open(cfg.DSN()), gormConfig)
	if err != nil {
		return nil, err
	}
	if err := ApplyPool(db, cfg.Pool); err != nil {
		return nil, err
	}

	logger.Info("Database connected successfully")

	return &PostgresDB{db: db}, nil
}

func OpenJob(dsn string) (*gorm.DB, error) {
	return OpenJobWith(dsn, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
}

func OpenJobWith(dsn string, cfg *gorm.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, err
	}
	if err := ApplyPool(db, config.JobPoolConfig()); err != nil {
		return nil, err
	}
	return db, nil
}

func ApplyPool(db *gorm.DB, pool config.PoolConfig) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if pool.MaxOpen > 0 {
		sqlDB.SetMaxOpenConns(pool.MaxOpen)
	}
	if pool.MaxIdle > 0 {
		sqlDB.SetMaxIdleConns(pool.MaxIdle)
	}
	if pool.MaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(pool.MaxLifetime)
	}
	if pool.MaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(pool.MaxIdleTime)
	}
	return nil
}

func (p *PostgresDB) DB() *gorm.DB {
	return p.db
}

func (p *PostgresDB) Close() error {
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (p *PostgresDB) AutoMigrate(models ...any) error {
	return p.db.AutoMigrate(models...)
}
