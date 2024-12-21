package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	ctxtypes "github.com/cyber-nic/ctx/libs/types"
	ctxutils "github.com/cyber-nic/ctx/libs/utils"
	"github.com/invopop/jsonschema"

	"github.com/google/generative-ai-go/genai"
	"github.com/gorilla/websocket"
	"github.com/logrusorgru/aurora/v4"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
)

const (
	modelName            = "gemini-2.0-flash-exp"
	debugCodeContextFile = "code.ctx"
)

func extractResponseContent(resp *genai.GenerateContentResponse) (string, error) {
	builder := strings.Builder{}

	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				builder.Write([]byte(fmt.Sprintf("%s\n", part)))
			}
		}
	}
	return builder.String(), nil
}

func wsHandler(ctx context.Context, client *genai.Client) func(w http.ResponseWriter, r *http.Request) {
	var upgrader = websocket.Upgrader{} // use default options
	model := client.GenerativeModel(modelName)
	model.ResponseMIMEType = "application/json"

	type AIResponseSchema struct {
		Stage  string      `json:"stage"`
		Status string      `json:"status"`
		Data   interface{} `json:"data"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Err(err).Msg("ws upgrade")
			return
		}
		defer c.Close()

		// Set up a close handler
		c.SetCloseHandler(func(code int, text string) error {
			log.Info().Int("code", code).Str("text", text).Msg("received close frame")
			message := websocket.FormatCloseMessage(code, "")
			return c.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second))
		})

		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err,
					websocket.CloseNormalClosure,
					websocket.CloseGoingAway,
					websocket.CloseAbnormalClosure) {
					log.Err(err).Msg("unexpected close error")
				} else {
					log.Info().Msg("websocket closed normally")
				}
				break
			}

			// Handle close messages
			if mt == websocket.CloseMessage {
				log.Info().Msg("received close message")
				return
			}

			// Only process text messages
			if mt != websocket.TextMessage {
				continue
			}

			// Unmarshal the message into CtxRequest
			var req ctxtypes.CtxRequest
			if err := json.Unmarshal(message, &req); err != nil {
				log.Err(err).Msg("Error marshalling JSON")
			}

			// Marshall the application context
			jsonCtx, err := json.Marshal(req.Context)
			// jsonData, err := json.MarshalIndent(req.Context, "", "")
			if err != nil {
				log.Err(err).Msg("Failed to marshal JSON")
				return
			}

			// if preload step write application context to file
			if req.Step == ctxtypes.CtxStepPreload {
				req.Instructions = append(req.Instructions, "Acknowledge application context using this JSON schema and exact response: {'stage': 'preload', 'status': 'ok'}")

				// Write the code context to disk
				go func() {
					f, err := os.OpenFile(debugCodeContextFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if err != nil {
						log.Fatal().Err(err).Msgf("Failed to open '%s' file", debugCodeContextFile)
					}
					defer f.Close()

					if _, err := f.WriteString(string(jsonCtx)); err != nil {
						log.Err(err).Msg("Failed to write to file")
					}
				}()
			}

			if len(req.Instructions) == 0 {
				log.Info().Msg("No instructions provided")
				return
			}

			log.Debug().Str("source", "ws").Int("len", len(jsonCtx)).Str("step", string(req.Step)).Msg("request")

			// Create genai.Part to hold context and instructions
			parts := make([]genai.Part, 0, len(req.Instructions)+1)

			// Add code context
			parts = append(parts, genai.Text(string(jsonCtx)))

			// Add instructions
			for _, instr := range req.Instructions {
				parts = append(parts, genai.Text(instr))
			}

			start := time.Now()
			resp, err := model.GenerateContent(ctx, parts...)

			if err != nil {
				log.Error().Err(err).Msg("ai failed to generate content") // Changed from Fatal to Error
				wsErr := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "AI generation failed")
				c.WriteMessage(websocket.CloseMessage, wsErr)
				return
			}
			elapsed := time.Since(start)
			l := log.With().Str("model", modelName).Str("elapsed", elapsed.String()).Logger()
			l.Trace().Msg(aurora.BrightBlue("ai").String())

			data, err := extractResponseContent(resp)
			if err != nil {
				l.Err(err).Msg("failed to extract ai response content")

				// preload doesn't expect a response
				if req.Step != ctxtypes.CtxStepPreload {
					wsErr := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to extract response")
					c.WriteMessage(websocket.CloseMessage, wsErr)
				}
				return
			}

			// unmarshal data into AIResponseSchema
			respData := AIResponseSchema{}

			if err := json.Unmarshal([]byte(data), &respData); err != nil {
				l.Err(err).Msg("failed to unmarshal preload ack response")
				return
			}
			l.Info().Str("stage", respData.Stage).Str("status", respData.Status).Msg("data")

			// log preload ack to stdout
			if req.Step == ctxtypes.CtxStepPreload {
				return
			}

			// preload doesn't expect a response
			if err = c.WriteMessage(mt, []byte(data)); err != nil {
				log.Err(err).Msg("failed to write message to ws")
				return
			}
		}
	}
}

func main() {
	var addr = flag.String("addr", "localhost:8000", "http service address")
	var debug = flag.Bool("debug", false, "enable debug mode")
	flag.Parse()

	ctxutils.ConfigLogging(debug)

	// context
	ctx := context.Background()

	var aiKey string

	homedir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to locate user's home directory")
	}

	// Recommended: check file
	if key, err := os.ReadFile(homedir + "/.secrets/GCP_AI_API_KEY"); err == nil {
		aiKey = string(key)
	}

	ai, err := genai.NewClient(ctx, option.WithAPIKey(strings.TrimSpace(aiKey)))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create AI client")
	}
	defer ai.Close()

	http.HandleFunc("/data", wsHandler(ctx, ai))

	log.Info().Str("proto", "ws").Str("addr", *addr).Msg("listening")
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}
}

func GenerateSchema[T any]() interface{} {
	// Structured Outputs uses a subset of JSON schema
	// These flags are necessary to comply with the subset
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T
	schema := reflector.Reflect(v)
	return schema
}
