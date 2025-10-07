package log

import "github.com/rs/zerolog"

var Logger = zerolog.New(nil)

func Info() zerolog.Event  { return Logger.Info() }
func Error() zerolog.Event { return Logger.Error() }
func Fatal() zerolog.Event { return Logger.Fatal() }
func Warn() zerolog.Event  { return Logger.Warn() }
func Debug() zerolog.Event { return Logger.Debug() }
