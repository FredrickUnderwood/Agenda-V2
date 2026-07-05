package repository

import (
	"time"

	alog "github.com/FredrickUnderwood/agenda-go-sdk/log"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type DBOptions struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func OpenMySQL(opts DBOptions) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(opts.DSN), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		alog.L().Error("open mysql failed", zap.Error(err))
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		alog.L().Error("get mysql sql db failed", zap.Error(err))
		return nil, err
	}
	if opts.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if opts.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}
	return db, nil
}

func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&domain.Route{},
		&domain.Backend{},
		&domain.RouteHistory{},
	); err != nil {
		alog.L().Error("migrate mysql failed", zap.Error(err))
		return err
	}
	return nil
}
