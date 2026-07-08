module github.com/infobloxopen/hotload/test/integration

go 1.25.0

require (
	github.com/infobloxopen/hotload/v3 v3.0.0-rc.1
	github.com/lib/pq v1.10.9
)

require (
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

// This module exists only to test the sibling modules in this repository;
// it is never tagged or imported, so the replace directive is permanent.
replace github.com/infobloxopen/hotload/v3 => ../../
