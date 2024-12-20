package ctxutils

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ConfigLogging configures the logging level and format
func ConfigLogging(debug *bool) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Logger = log.With().Caller().Logger()

	if debug != nil && *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// add CTX_LOG env variable to set log level
	if logLevel, ok := os.LookupEnv("CTX_LOG"); ok {
		switch logLevel {
		case "debug":
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		case "trace":
			zerolog.SetGlobalLevel(zerolog.TraceLevel)
		case "error":
			zerolog.SetGlobalLevel(zerolog.ErrorLevel)
		default:
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
			log.Warn().Msgf("Invalid log level: %s", logLevel)
		}
		return
	}

	// default log level
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}
