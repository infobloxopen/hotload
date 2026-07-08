module github.com/infobloxopen/hotload/observability

go 1.25.0

require (
	github.com/infobloxopen/hotload/v3 v3.0.0
	github.com/prometheus/client_golang v1.20.0
	github.com/prometheus/common v0.55.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	golang.org/x/sys v0.45.0 // indirect
	google.golang.org/protobuf v1.35.1 // indirect
)

replace github.com/infobloxopen/hotload/v3 => ../
