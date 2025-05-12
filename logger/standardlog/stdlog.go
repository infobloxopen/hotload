package standardlog

import (
	"log"

	hlogger "github.com/infobloxopen/hotload/logger"
)

// StdLogLevelLogger adapts Golang's standard log pkg as LevelLogger.
// Intended for internal hotload testing purposes, probably not suitable for production.
type StdLogLevelLogger struct {
	parent   *StdLogLevelLogger
	stdloggr *log.Logger
	level    Level
	kvMap    hlogger.KeyValueMap
}

func NewStdLogLevelLogger(stdloggr *log.Logger, lvl Level) *StdLogLevelLogger {
	new := &StdLogLevelLogger{
		stdloggr: stdloggr,
		level:    lvl,
		kvMap:    make(hlogger.KeyValueMap),
	}
	return new
}

func (lll StdLogLevelLogger) WithKV(keyValuePairs ...any) hlogger.LevelLogger {
	new := NewStdLogLevelLogger(lll.stdloggr, lll.level)
	new.parent = &lll
	new.kvMap.MergeKVPairs(keyValuePairs...)
	return new
}

func (lll StdLogLevelLogger) DebugKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, LevelDebug, msg, keyValuePairs...)
}

func (lll StdLogLevelLogger) InfoKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, LevelInfo, msg, keyValuePairs...)
}

func (lll StdLogLevelLogger) WarnKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, LevelWarn, msg, keyValuePairs...)
}

func (lll StdLogLevelLogger) ErrorKV(err error, msg string, keyValuePairs ...any) {
	lll.logKV(err, LevelError, msg, keyValuePairs...)
}

func (lll StdLogLevelLogger) logKV(err error, lvl Level, msg string, keyValuePairs ...any) {
	if lvl < lll.level {
		return
	}

	newMap := lll.kvMap

	// If it has parent, or there is err, or there are KV args,
	// then need to allocate copy of KVMap because we need to modify KVMap
	if (lll.parent != nil) || (err != nil) || (len(keyValuePairs) > 0) {
		newMap = hlogger.KVMapTreeCopyMergeAncestors(lll)
		newMap.MergeKVPairs(keyValuePairs...)
		if err != nil {
			newMap["err"] = err.Error()
		}
	}

	lll.stdloggr.Printf("%s %s%v\n", lvl.String(), msg, newMap)
}

// GetParent implements hlogger.KVMapTreeNode interface for StdLogLevelLogger
func (lll StdLogLevelLogger) GetParent() hlogger.KVMapTreeNode {
	if lll.parent == nil {
		return nil // if parent is nil, must explicitly return nil, not parent, otherwise panic
	}
	return lll.parent
}

// GetKVMap implements hlogger.KVMapTreeNode interface for StdLogLevelLogger
func (lll StdLogLevelLogger) GetKVMap() hlogger.KeyValueMap {
	return lll.kvMap
}

// Level mimics slog's Level
type Level int

const (
	levelInherit Level = -99 // TODO: internal: inherit level from parent
	LevelDebug   Level = -4
	LevelInfo    Level = 0
	LevelWarn    Level = 4
	LevelError   Level = 8
)

var levelNameMap = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

// String implements fmt.Stringer interface for Level
func (lvl Level) String() string {
	name, ok := levelNameMap[lvl]
	if !ok {
		name = "UNKNOWN-LOGLEVEL"
	}
	return name
}
