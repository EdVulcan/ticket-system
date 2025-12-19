package logger

import (
	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

func InitLogger(level string, filename string, maxSize, maxAge, maxBackups int) {
	writeSyncer := getLogWriter(filename, maxSize, maxAge, maxBackups)
	encoder := getEncoder()

	var l zapcore.Level
	switch level {
	case "debug":
		l = zapcore.DebugLevel
	case "info":
		l = zapcore.InfoLevel
	case "warn":
		l = zapcore.WarnLevel
	case "error":
		l = zapcore.ErrorLevel
	default:
		l = zapcore.InfoLevel
	}

	core := zapcore.NewCore(encoder, writeSyncer, l)

	// Add caller skip to keep caller info correct when wrapping
	Log = zap.New(core, zap.AddCaller())
}

func getEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	return zapcore.NewConsoleEncoder(encoderConfig)
}

func getLogWriter(filename string, maxSize, maxAge, maxBackups int) zapcore.WriteSyncer {
	lumberJackLogger := &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		MaxAge:     maxAge,
		Compress:   false,
	}
	return zapcore.AddSync(lumberJackLogger)
}
