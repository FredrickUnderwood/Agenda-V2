package logger

import "go.uber.org/zap"

var log *zap.Logger

type Config struct {
	Level string
}

func Init(cfg Config) {
	zcfg := zap.NewProductionConfig()
	if cfg.Level != "" {
		var level zap.AtomicLevel
		if err := level.UnmarshalText([]byte(cfg.Level)); err == nil {
			zcfg.Level = level
		}
	}
	l, err := zcfg.Build()
	if err != nil {
		panic(err)
	}
	log = l
}

func L() *zap.Logger {
	if log == nil {
		log = zap.NewNop()
	}
	return log
}

func Shutdown() {
	if log != nil {
		_ = log.Sync()
	}
}
