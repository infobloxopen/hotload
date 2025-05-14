package logger

import (
	"fmt"
	"time"
)

// V1LevelLogger adapts the deprecated version 1 Logger to LevelLogger.
// V1LevelLogger is fixed at INFO-level, it never logs any DEBUG-level output.
type V1LevelLogger struct {
	parent   *V1LevelLogger
	v1logger Logger
	kvMap    KeyValueMap
}

func NewV1LevelLogger(v1logger Logger) *V1LevelLogger {
	new := &V1LevelLogger{
		v1logger: v1logger,
		kvMap:    make(KeyValueMap),
	}
	return new
}

func (lll V1LevelLogger) WithKV(keyValuePairs ...any) LevelLogger {
	new := NewV1LevelLogger(lll.v1logger)
	new.parent = &lll
	new.kvMap.MergeKVPairs(keyValuePairs...)
	return new
}

func (lll V1LevelLogger) DebugKV(msg string, keyValuePairs ...any) {
	// V1LevelLogger discards debug-level
}

func (lll V1LevelLogger) InfoKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, "INFO", msg, keyValuePairs...)
}

func (lll V1LevelLogger) WarnKV(msg string, keyValuePairs ...any) {
	lll.logKV(nil, "WARN", msg, keyValuePairs...)
}

func (lll V1LevelLogger) ErrorKV(err error, msg string, keyValuePairs ...any) {
	lll.logKV(err, "ERROR", msg, keyValuePairs...)
}

func (lll V1LevelLogger) logKV(err error, level, msg string, keyValuePairs ...any) {
	nowStr := time.Now().Format(time.RFC3339Nano)
	newMap := lll.kvMap

	// If it has parent, or there is err, or there are KV args,
	// then need to allocate copy of KVMap because we need to modify KVMap
	if (lll.parent != nil) || (err != nil) || (len(keyValuePairs) > 0) {
		newMap = KVMapTreeCopyMergeAncestors(lll)
		newMap.MergeKVPairs(keyValuePairs...)
		if err != nil {
			newMap["err"] = err.Error()
		}
	}

	lll.v1logger(fmt.Sprintf("%s %s: %s%v\n", nowStr, level, msg, newMap))
}

// GetParent implements hlogger.KVMapTreeNode interface for V1LevelLogger
func (lll V1LevelLogger) GetParent() KVMapTreeNode {
	if lll.parent == nil {
		return nil // if parent is nil, must explicitly return nil, not parent, otherwise panic
	}
	return lll.parent
}

// GetKVMap implements hlogger.KVMapTreeNode interface for V1LevelLogger
func (lll V1LevelLogger) GetKVMap() KeyValueMap {
	return lll.kvMap
}
