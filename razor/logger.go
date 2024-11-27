package razor

import (
	"log"
)

type LoggingLevel int

const (
	LoggingLevelOff LoggingLevel = iota
	LogLevelError
	LogLevelWarn
	LogLevelInfo
	LogLevelVerbose
	LogLevelDebug
	LogLevelTrace
)

func (l LoggingLevel) String() string {
	switch l {
	case LoggingLevelOff:
		return "OFF"
	case LogLevelError:
		return "ERR"
	case LogLevelWarn:
		return "WRN"
	case LogLevelInfo:
		return "IFO"
	case LogLevelVerbose:
		return "VRB"
	case LogLevelDebug:
		return "DBG"
	case LogLevelTrace:
		return "TRC"
	}

	return "UNK"
}

type Logger struct {
	level                LoggingLevel
	panicOnAssertFailure bool
}

func NewLogger(level LoggingLevel, panicOnAssertFailure bool) *Logger {
	return &Logger{}
}

func (l *Logger) log(level LoggingLevel, component string, msg string) {
	if level <= l.level {
		log.Println(level, component, "--", msg)
	}
}

func (l *Logger) NilErrCheck(component string, msg string, err error) bool {
	if err != nil {
		if l.panicOnAssertFailure {
			panic("Error is not nil " + component + " " + msg + " " + err.Error())
		} else {
			l.log(LogLevelError, component, msg)
		}
		return true
	}
	return false
}

func (l *Logger) Assert(component string, msg string, condition bool) {
	if !condition {
		if l.panicOnAssertFailure {
			panic("Assertion failure in " + component + " " + msg)
		} else {
			l.log(LogLevelError, component, "Assertion failure: "+msg)
		}
	}
}

func (l *Logger) Error(component string, msg string) {
	l.log(LogLevelError, component, msg)
}

func (l *Logger) Warn(component string, msg string) {
	l.log(LogLevelWarn, component, msg)
}

func (l *Logger) Info(component string, msg string) {
	l.log(LogLevelInfo, component, msg)
}

func (l *Logger) Verbose(component string, msg string) {
	l.log(LogLevelVerbose, component, msg)
}

func (l *Logger) Debug(component string, msg string) {
	l.log(LogLevelDebug, component, msg)
}

func (l *Logger) Trace(component string, msg string) {
	l.log(LogLevelTrace, component, msg)
}
