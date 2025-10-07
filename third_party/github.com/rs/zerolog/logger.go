package zerolog

import "io"

type Level int8

const (
DebugLevel Level = iota
InfoLevel
WarnLevel
ErrorLevel
FatalLevel
)

var TimeFieldFormat = ""

type Logger struct{}

func New(w io.Writer) Logger {
_ = w
return Logger{}
}

func (l Logger) With() Context { return Context{} }

func (l Logger) Level(level Level) Logger {
_ = level
return l
}

func (l Logger) Info() Event  { return Event{} }
func (l Logger) Error() Event { return Event{} }
func (l Logger) Debug() Event { return Event{} }
func (l Logger) Warn() Event  { return Event{} }
func (l Logger) Fatal() Event { return Event{} }
func (l Logger) Timestamp() Logger {
return l
}

type Event struct{}

func (e Event) Str(key, val string) Event {
_ = key
_ = val
return e
}

func (e Event) Int64(key string, v int64) Event {
_ = key
_ = v
return e
}

func (e Event) Err(err error) Event {
_ = err
return e
}

func (e Event) Msg(msg string) {
_ = msg
}

func (e Event) Msgf(format string, args ...any) {
_ = format
_ = args
}

func (e Event) Send() {}

type Context struct{}

func (c Context) Timestamp() Context { return c }

func (c Context) Logger() Logger { return Logger{} }
