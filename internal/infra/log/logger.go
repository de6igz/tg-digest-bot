package log

import (
"os"
"time"

"github.com/rs/zerolog"
)

// NewLogger создаёт настроенный zerolog.
func NewLogger(appEnv string) zerolog.Logger {
level := zerolog.InfoLevel
if appEnv == "dev" {
level = zerolog.DebugLevel
}
logger := zerolog.New(os.Stdout).With().Timestamp().Logger().Level(level)
zerolog.TimeFieldFormat = time.RFC3339
return logger
}
