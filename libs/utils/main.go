package ctxutils

import (
	"encoding/json"
	"fmt"
	"io"
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
		log.Debug().Msg("debug logging enabled")
	}

	// add CTX_LOG env variable to set log level
	if logLevel, ok := os.LookupEnv("CTX_LOG"); ok {
		switch logLevel {
		case "debug":
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
			log.Debug().Msg("debug logging enabled")
		case "trace":
			zerolog.SetGlobalLevel(zerolog.TraceLevel)
			log.Trace().Msg("trace logging enabled")
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

// PrintStruct prints a struct as JSON.
func PrintStruct(w io.Writer, t interface{}) {
	j, _ := json.MarshalIndent(t, "", "  ")
	fmt.Fprintln(w, string(j))
}

func PrintStructOut(t interface{}) {
	PrintStruct(os.Stdout, t)
}
