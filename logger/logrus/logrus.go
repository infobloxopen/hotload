package logrus

import (
	hlogger "github.com/infobloxopen/hotload/logger"
	"github.com/sirupsen/logrus"
)

// LogrusLevelLogger adapts logrus pkg as LevelLogger.
type LogrusLevelLogger struct {
	parent   *LogrusLevelLogger
	loggerus *logrus.Logger
	kvArgs   hlogger.KeyValueArgs
}

func NewLogrusLevelLogger(loggerus *logrus.Logger) *LogrusLevelLogger {
	new := &LogrusLevelLogger{
		loggerus: loggerus,
		kvArgs:   make(hlogger.KeyValueArgs, 0, 4),
	}
	return new
}

func (lll LogrusLevelLogger) WithKV(keyValuePairs ...any) hlogger.LevelLogger {
	new := NewLogrusLevelLogger(lll.loggerus)
	new.parent = &lll
	if len(keyValuePairs) > 0 {
		new.kvArgs.AppendKVPairs(keyValuePairs...)
	}
	return new
}

func (lll LogrusLevelLogger) DebugKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, logrus.DebugLevel, msg, keyValuePairs...)
}

func (lll LogrusLevelLogger) InfoKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, logrus.InfoLevel, msg, keyValuePairs...)
}

func (lll LogrusLevelLogger) WarnKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, logrus.WarnLevel, msg, keyValuePairs...)
}

func (lll LogrusLevelLogger) ErrorKV(err error, msg string, keyValuePairs ...any) {
	lll.logKV(err, logrus.ErrorLevel, msg, keyValuePairs...)
}

func (lll LogrusLevelLogger) logKV(err error, lvl logrus.Level, msg string, keyValuePairs ...any) {
	if !lll.loggerus.IsLevelEnabled(lvl) {
		return
	}

	fields := make(logrus.Fields)
	hlogger.KVArgsTreeIterateAncestors(lll, func(kAny, vAny any) {
		kStr, _ := hlogger.AnyToString(kAny)
		fields[kStr] = vAny
	})

	// Use integer modulo math to ensure even number of args are iterated
	evenLen := len(keyValuePairs) / 2 * 2
	for i := 0; i < evenLen; i = i + 2 {
		kAny := keyValuePairs[i]
		vAny := keyValuePairs[i+1]
		kStr, _ := hlogger.AnyToString(kAny)
		fields[kStr] = vAny
	}

	entry := lll.loggerus.WithFields(fields)
	if err != nil {
		entry = entry.WithError(err)
	}
	entry.Log(lvl, msg)
}

// GetParent implements hlogger.KVArgsTreeNode interface for LogrusLevelLogger
func (lll LogrusLevelLogger) GetParent() hlogger.KVArgsTreeNode {
	if lll.parent == nil {
		return nil // if parent is nil, must explicitly return nil, not parent, otherwise panic
	}
	return lll.parent
}

// GetKVArgs implements hlogger.KVArgsTreeNode interface for LogrusLevelLogger
func (lll LogrusLevelLogger) GetKVArgs() hlogger.KeyValueArgs {
	return lll.kvArgs
}
