module github.com/superplanehq/suss

go 1.26

toolchain go1.26.6

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260811182544-a038080d80e5 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	golang.org/x/vuln v1.7.0 // indirect
	mvdan.cc/gofumpt v0.11.0 // indirect
)

tool (
	golang.org/x/vuln/cmd/govulncheck
	mvdan.cc/gofumpt
)
