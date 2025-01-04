package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	ctxutils "github.com/cyber-nic/ctx/libs/utils"

	"github.com/rs/zerolog/log"
	"github.com/tmc/langchaingo/llms/googleai"
)

const (
	// this is the model name that we are using and should NEVER be changed
	modelName            = "gemini-2.0-flash-exp"
	debugCodeContextFile = "code.ctx"
)

func main() {
	var addr = flag.String("addr", "localhost:8000", "http service address")
	var debug = flag.Bool("debug", false, "enable debug mode")
	flag.Parse()

	ctxutils.ConfigLogging(debug)

	// context
	ctx := context.Background()

	// API key
	var key []byte
	{
		// get home dir
		homedir, err := os.UserHomeDir()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to locate user's home directory")
		}

		// read API key
		if key, err = os.ReadFile(fmt.Sprintf("%s/.secrets/GCP_AI_API_KEY", homedir)); err != nil {
			log.Fatal().Err(err).Msg("failed to read API key")
		}
	}

	llm, err := googleai.New(ctx, googleai.WithAPIKey(string(key)))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create AI client")
	}

	// create a new CodeContextService
	wss := NewCodeContextService(llm, modelName)

	// Start server
	http.HandleFunc("/data", wss.Handler(ctx))

	log.Info().Str("proto", "ws").Str("addr", *addr).Msg("listening")
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}
}
