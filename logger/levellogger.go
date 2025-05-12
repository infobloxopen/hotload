package logger

// LevelLogger defines the abstract leveled-logging interface.
// Replaces the deprecated Logger abstract interface.
type LevelLogger interface {
	WithKV(keyValuePairs ...any) LevelLogger
	DebugKV(msg string, keyValuePairs ...any)
	InfoKV(msg string, keyValuePairs ...any)
	WarnKV(msg string, keyValuePairs ...any)
	ErrorKV(err error, msg string, keyValuePairs ...any)
}

var (
	nullLevelLogger    LevelLogger = NewNullLevelLogger()
	defaultLevelLogger LevelLogger = nullLevelLogger
)

// SetDefaultLevelLogger sets the default level logger used by the convenience LevelLogger functions
func SetDefaultLevelLogger(lvlg LevelLogger) { defaultLevelLogger = lvlg }

// GetDefaultLevelLogger gets the default level logger used by the convenience LevelLogger functions
func GetDefaultLevelLogger() LevelLogger { return defaultLevelLogger }

// WithKV is convenience fn that is the equivalent of GetDefaultLevelLogger().WithKV()
func WithKV(keyValuePairs ...any) LevelLogger { return defaultLevelLogger.WithKV(keyValuePairs...) }

// DebugKV is convenience fn that is the equivalent of GetDefaultLevelLogger().DebugKV()
func DebugKV(msg string, keyValuePairs ...any) { defaultLevelLogger.DebugKV(msg, keyValuePairs...) }

// InfoKV is convenience fn that is the equivalent of GetDefaultLevelLogger().InfoKV()
func InfoKV(msg string, keyValuePairs ...any) { defaultLevelLogger.InfoKV(msg, keyValuePairs...) }

// WarnKV is convenience fn that is the equivalent of GetDefaultLevelLogger().WarnKV()
func WarnKV(msg string, keyValuePairs ...any) { defaultLevelLogger.WarnKV(msg, keyValuePairs...) }

// ErrorKV is convenience fn that is the equivalent of GetDefaultLevelLogger().ErrorKV()
func ErrorKV(err error, msg string, keyValuePairs ...any) {
	defaultLevelLogger.ErrorKV(err, msg, keyValuePairs...)
}

// NullLevelLogger implements a LevelLogger that is silent and never logs anything.
type NullLevelLogger struct{}

func NewNullLevelLogger() *NullLevelLogger { return &NullLevelLogger{} }

func (lll NullLevelLogger) WithKV(keyValuePairs ...any) LevelLogger { return lll }

func (lll NullLevelLogger) DebugKV(msg string, keyValuePairs ...any) {}

func (lll NullLevelLogger) InfoKV(msg string, keyValuePairs ...any) {}

func (lll NullLevelLogger) WarnKV(msg string, keyValuePairs ...any) {}

func (lll NullLevelLogger) ErrorKV(err error, msg string, keyValuePairs ...any) {}
