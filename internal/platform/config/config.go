// Package config defines Platform configuration schema, validation and activation.
package config

// Config is a placeholder configuration surface for the repository scaffold
// slice. The real schema, reload-class tagging and activation versions land
// in the Configuration and secret broker slice (MEG-015 §08).
type Config struct {
	Environment string
}

// Load returns a stub configuration. It does not yet read from disk, the
// environment or a config store — that arrives with the Configuration slice.
func Load() (Config, error) {
	return Config{Environment: "local"}, nil
}
