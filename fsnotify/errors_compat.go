package fsnotify

// isError reports whether any error in err's chain matches target.
// This is a compatibility shim for errors.Is which was added in Go 1.13.
// We need this for Go 1.20 compatibility with older error packages.
func isError(err, target error) bool {
	if target == nil {
		return err == target
	}

	// Direct comparison
	if err == target {
		return true
	}

	// Walk the error chain
	for {
		if err == nil {
			return false
		}
		if err == target {
			return true
		}
		// Check if error implements Is method
		if x, ok := err.(interface{ Is(error) bool }); ok && x.Is(target) {
			return true
		}
		// Unwrap and continue
		if x, ok := err.(interface{ Unwrap() error }); ok {
			err = x.Unwrap()
			continue
		}
		return false
	}
}
