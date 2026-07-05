package service

import (
	"github.com/bytedance/sonic"
	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

func sonicMarshal(v interface{}) (string, error) {
	return sonic.MarshalString(v)
}

// logStruct marshals v via sonic and writes it as a single Info log field,
// per this project's struct-logging convention.
func logStruct(label string, v interface{}) {
	s, _ := sonic.MarshalString(v)
	logger.L().Info(label, zap.String("data", s))
}
