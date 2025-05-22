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
	log       logger.Logger
	errlog    logger.Logger
	statFS    fs.StatFS
	checkIntv time.Duration
}

type Option func(*mtmOptions)

func newDefaultOptions() *mtmOptions {
	opts := &mtmOptions{
		log:       logger.GetLogger(),
		statFS:    DefaultStatFS,
		checkIntv: DefaultCheckInterval,
	}
	return opts
}

// WithLogger is the option to set the Logger
func WithLogger(log logger.Logger) Option {
	return func(opts *mtmOptions) {
		if log == nil {
			opts.log = logger.GetLogger()
		} else {
			opts.log = log
		}
	}
}

// WithErrLogger is the option to set the Logger
func WithErrLogger(errlog logger.Logger) Option {
	return func(opts *mtmOptions) {
		if errlog == nil {
			opts.errlog = logger.GetErrLogger()
		} else {
			opts.errlog = errlog
		}
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
