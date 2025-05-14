package modtime

import (
	"io/fs"
	"os"
	"time"

	"github.com/infobloxopen/hotload/logger"
)

var (
	// DefaultStatFS is the default Stat FileSystem to monitor paths,
	// which is the host Unix filesystem rooted at "/"
	DefaultStatFS = os.DirFS("/").(fs.StatFS)

	// DefaultCheckInterval is the default interval for checking paths' modtimes
	DefaultCheckInterval = time.Minute * 5
)

type mtmOptions struct {
	log       logger.LevelLogger
	statFS    fs.StatFS
	checkIntv time.Duration
}

type Option func(*mtmOptions)

func newDefaultOptions() *mtmOptions {
	opts := &mtmOptions{
		log:       logger.GetDefaultLevelLogger(),
		statFS:    DefaultStatFS,
		checkIntv: DefaultCheckInterval,
	}
	return opts
}

// WithLogger is the option to set the Logger
// Deprecated: Use WithLevelLogger instead (internally all hotload logging now uses LevelLogger)
func WithLogger(log logger.Logger) Option {
	return func(opts *mtmOptions) {
		if log == nil {
			log = logger.GetLogger()
		}
		opts.log = logger.NewV1LevelLogger(log)
	}
}

// WithLevelLogger is the option to set the LevelLogger
func WithLevelLogger(log logger.LevelLogger) Option {
	return func(opts *mtmOptions) {
		if log == nil {
			log = logger.GetDefaultLevelLogger()
		}
		opts.log = log
	}
}

// WithStatFS is the option to set the Stat FileSystem
func WithStatFS(statFS fs.StatFS) Option {
	return func(opts *mtmOptions) {
		if statFS == nil {
			opts.statFS = DefaultStatFS
		} else {
			opts.statFS = statFS
		}
	}
}

// WithCheckInterval is the option to set the check interval
// (how often to check modtimes)
func WithCheckInterval(checkIntv time.Duration) Option {
	return func(opts *mtmOptions) {
		if checkIntv <= 0 {
			opts.checkIntv = DefaultCheckInterval
		} else {
			opts.checkIntv = checkIntv
		}
	}
}
