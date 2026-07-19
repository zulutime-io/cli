package version

// Version is set at build time via -ldflags.
var Version = "dev"

// UserAgent returns the HTTP User-Agent for API requests.
func UserAgent() string {
	return "ztime-cli/" + Version
}
