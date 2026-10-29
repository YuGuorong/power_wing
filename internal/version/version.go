// Package version holds the application version string.
//
// The default is "dev"; release builds override it with:
//
//	go build -ldflags "-X github.com/yuguorong/power_wing/internal/version.Version=1.2.3"
package version

// Version is the PowerWing application version, reported by the web UI
// and the MCP server info response.
var Version = "dev"
