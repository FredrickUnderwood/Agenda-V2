package repository

import (
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

// OpenMySQL opens the application database connection. Schema is created and
// kept up to date by Migrate (GORM AutoMigrate) at startup, so a fresh install
// needs no out-of-band SQL — clone, point at an empty database, and run.
func OpenMySQL(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
		Logger:         gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

// Migrate creates or updates every control-plane table from the domain models.
// GORM AutoMigrate is additive: it creates missing tables, columns, and indexes
// but never drops a column or index, so it is safe to run on every startup.
// The models carry the full schema (sizes, defaults, composite indexes) in their
// gorm struct tags — those tags are the single source of truth for the schema.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&domain.Machine{},
		&domain.Application{},
		&domain.ApplicationEnvironment{},
		&domain.ApplicationEnvTarget{},
		&domain.ApplicationGatewayRoute{},
		&domain.ApplicationGatewayRouteBackend{},
		&domain.ApplicationInstanceHealth{},
		&domain.ApplicationRelease{},
		&domain.DeployLog{},
		&domain.PipelineStep{},
		&domain.Setting{},
		&domain.User{},
		&domain.Notification{},
		&domain.AlertRule{},
	)
}
