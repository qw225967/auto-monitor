package logger

import (
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	instance *zap.Logger
	once     sync.Once
)

// GetLoggerInstance 获取日志单例实例
func GetLoggerInstance() *zap.Logger {
	once.Do(func() {
		instance = initLogger("")
	})
	return instance
}

// InitLogger 初始化日志（支持指定文件路径）
// 注意：这应该在程序启动早期调用，且只能调用一次（或者会覆盖之前的实例）
func InitLogger(logPath string) {
	instance = initLogger(logPath)
	// 如果 once 已经被执行过（即 GetLoggerInstance 被调用过），这里直接覆盖 instance
	// 如果没有，我们手动标记 once 为已执行，防止 GetLoggerInstance 再次覆盖
	once.Do(func() {})
}

// 初始化 logger
func initLogger(logPath string) *zap.Logger {
	// 配置输出格式
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder, // 小写编码器
		EncodeTime:     zapcore.ISO8601TimeEncoder,    // ISO8601 UTC 时间格式
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder, // 短路径编码器
	}

	// 设置日志级别（Debug 便于排查 Bitget 提币确认等）
	atom := zap.NewAtomicLevelAt(zap.InfoLevel)

	// 配置输出目标
	var syncer zapcore.WriteSyncer
	if logPath != "" {
		// 如果指定了日志文件，输出到文件和控制台
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			// 如果打开文件失败，降级为仅输出到控制台，并打印错误
			// 这里不能用 zap 打印，因为 zap 还没初始化完成
			fmt.Printf("Failed to open log file %s: %v. Logging to stdout only.\n", logPath, err)
			syncer = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout))
		} else {
			syncer = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(file))
		}
	} else {
		// 默认只输出到控制台
		syncer = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout))
	}

	// 配置日志输出
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig), // JSON 编码器
		syncer,                                // 输出目标
		atom,                                  // 日志级别
	)

	// 开启开发模式，堆栈跟踪
	caller := zap.AddCaller()
	development := zap.Development()

	// 构造日志
	logger := zap.New(core, caller, development)

	return logger
}
