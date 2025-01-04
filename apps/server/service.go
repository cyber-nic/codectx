package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	ctxtypes "github.com/cyber-nic/ctx/libs/types"
	"github.com/gorilla/websocket"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog/log"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/googleai"
)

type CodeContextService interface {
	Handler(ctx context.Context) func(w http.ResponseWriter, r *http.Request)
}

type codeContextService struct {
	model llms.CallOption
	llm   *googleai.GoogleAI
}

func NewCodeContextService(llm *googleai.GoogleAI, model string) CodeContextService {
	return &codeContextService{
		llm:   llm,
		model: llms.WithModel(modelName),
	}
}

func (wss *codeContextService) Handler(ctx context.Context) func(w http.ResponseWriter, r *http.Request) {
	var upgrader = websocket.Upgrader{} // use default options

	// model.ResponseMIMEType = "application/json"

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

		l := log.With().Str("client_ip", r.RemoteAddr).Logger()

		for {
			// block until a message is received
			mt, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err,
					websocket.CloseNormalClosure,
					websocket.CloseGoingAway,
					websocket.CloseAbnormalClosure) {
					l.Err(err).Msg("unexpected close error")
				} else {
					l.Info().Msg("websocket closed normally")
				}
				break
			}

			log.Info().Int("type", mt).Msg("received message")

			// Handle close messages
			if mt == websocket.CloseMessage {
				l.Info().Msg("received close message")
				continue
			}

			// Only process text messages
			if mt != websocket.TextMessage {
				continue
			}

			// Unmarshal the message into CtxRequest
			var req ctxtypes.CtxRequest
			if err := json.Unmarshal(message, &req); err != nil {
				l.Err(err).Msg("Error marshalling JSON")
			}

			// add client id to log
			l = l.With().Str("client_id", req.ClientID).Str("step", string(req.Step)).Logger()

			// Marshall the application context
			jsonCtx, err := json.Marshal(req.Context)
			// jsonData, err := json.MarshalIndent(req.Context, "", "")
			if err != nil {
				l.Err(err).Msg("Failed to marshal JSON")
				continue
			}

			// Add the length of the context to the log
			l = l.With().Int("len", len(jsonCtx)).Logger()

			// Instructions for the AI
			instructions := []string{}

			switch req.Step {
			// PRELOAD CONTEXT
			case ctxtypes.CtxStepLoadContext:
				schema := GenerateSchema[ctxtypes.StepPreloadResponseSchema]()
				instructions = []string{
					"Acknowledge application context and respond step=preload and status=ok",
					fmt.Sprintf("Respond using this JSON schema: %v", schema),
				}

				// Write the code context to disk
				go func() {
					f, err := os.OpenFile(debugCodeContextFile, os.O_CREATE|os.O_WRONLY, 0644)
					if err != nil {
						l.Fatal().Err(err).Msgf("Failed to open '%s' file", debugCodeContextFile)
					}
					defer f.Close()

					if _, err := f.WriteString(string(jsonCtx)); err != nil {
						l.Err(err).Msg("Failed to write to file")
					}
				}()

			// SELECT FILES
			case ctxtypes.CtxStepFileSelection:
				schema := GenerateSchema[ctxtypes.StepFileSelectFiles]()

				instructions = []string{
					fmt.Sprintf("You are a senior software engineer and system architect. Consider the previously provided application context along with this user prompt describing changes needed to the codebase: ``%s``.", req.UserPrompt),
					"First identity the list of files that will need to be altered, created or removed in order to implement the requirements or instructions articulated in the prompt. Return these in the `files` array. The `operation` field must be set to 0 for updates, 1 for create, and -1 for remove.",
					"Next identity additional files for which the content would be useful to have in order to perform the requested changes. Return this list of files in the `additional_context_files` array.",
					fmt.Sprintf("Respond using this JSON schema: %v", schema),
				}

			// WORK
			case ctxtypes.CtxStepCodeWork:
				schema := GenerateSchema[ctxtypes.PatchData]()

				instructions = []string{
					fmt.Sprintf("You are a senior software engineer and system architect. Consider the previously provided application context along with this user prompt describing changes needed to the codebase: ``%s``.", req.UserPrompt),
					"You always follow best practices and ensure that your code is clean, maintainable, and well-documented. Your code should be production-ready and ready to be reviewed by your peers. Changes are razor-focused and should not include any unrelated changes.",
					fmt.Sprintf("Respond using a properly formatted git patch, honoring the following schema: %v", schema),
					fmt.Sprintf("Given the application context and the user prompt, return the changes needed to implement the requirements or instructions articulated in the prompt for the file: \n\n%s", req.WorkPrompt),
				}

			// UNEXPECTED
			default:
				l.Warn().Str("step", string(req.Step)).Msg("unexpected step")
			}
			l.Debug().Msg("request")

			promptParts, err := formatGenaiParts(string(jsonCtx), instructions)
			if err != nil {
				l.Err(err).Msg("unexpected error")
				continue
			}

			content := []llms.MessageContent{
				{
					Role:  llms.ChatMessageTypeHuman,
					Parts: promptParts,
				},
			}

			start := time.Now()
			aiResp, err := wss.llm.GenerateContent(ctx, content, wss.model, llms.WithTemperature(0.8), llms.WithJSONMode())

			if err != nil {
				l.Error().Err(err).Msg("ai failed to generate content") // Changed from Fatal to Error
				wsErr := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "ai generation failed")
				c.WriteMessage(websocket.CloseMessage, wsErr)
				continue
			}

			// Log the elapsed time
			elapsed := time.Since(start)
			l = l.With().Int64("elapsed_ms", elapsed.Milliseconds()).Logger()

			data, err := extractResponseContent(aiResp)
			if err != nil {
				l.Err(err).Msg("failed to extract ai response content")

				// preload doesn't expect a response
				if req.Step != ctxtypes.CtxStepLoadContext {
					wsErr := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "failed to extract response")
					c.WriteMessage(websocket.CloseMessage, wsErr)
				}
				continue
			}

			// ndelorme - unmarshal into step corresponding response model
			switch req.Step {
			case ctxtypes.CtxStepLoadContext:
				// unmarshal data into StepPreloadResponseSchema
				respData := ctxtypes.StepPreloadResponseSchema{}

				if err := json.Unmarshal([]byte(data), &respData); err != nil {
					l.Err(err).Msg("failed to unmarshal preload ack response")
					continue
				}
				l.Debug().Str("status", respData.Status).Msg("response")

				// log preload ack to stdout
				continue
			case ctxtypes.CtxStepFileSelection:
				// unmarshal data into StepPreloadResponseSchema
				fileData := ctxtypes.StepFileSelectFiles{}

				if err := json.Unmarshal([]byte(data), &fileData); err != nil {
					l.Err(err).Msg("failed to unmarshal preload ack response")
					continue
				}
				l.Debug().Str("status", "ok").Msg("response")

				respData := ctxtypes.StepFileSelectResponseSchema{
					Timestamp: time.Now().Format(time.RFC3339),
					Step:      string(req.Step),
					Status:    "ok",
					Data:      fileData,
				}

				// marshal response
				d, err := json.Marshal(respData)
				if err != nil {
					l.Err(err).Msg("failed to marshal response")
					continue
				}

				// preload doesn't expect a response
				if err = c.WriteMessage(mt, []byte(d)); err != nil {
					l.Err(err).Msg("failed to write message to ws")
					continue
				}

			case ctxtypes.CtxStepCodeWork:
				// unmarshal data into PatchData
				patchData := ctxtypes.PatchData{}

				fmt.Println(data)

				if err := json.Unmarshal([]byte(data), &patchData); err != nil {
					l.Err(err).Msg("failed to unmarshal git patch response")
					continue
				}
				l.Debug().Str("status", "ok").Msg("response")

				respData := ctxtypes.StepFileWorkResponseSchema{
					Timestamp: time.Now().Format(time.RFC3339),
					Step:      string(req.Step),
					Status:    "ok",
					Data:      patchData,
				}

				// marshal response
				d, err := json.Marshal(respData)
				if err != nil {
					l.Err(err).Msg("failed to marshal response")
					continue
				}

				// preload doesn't expect a response
				if err = c.WriteMessage(mt, []byte(d)); err != nil {
					l.Err(err).Msg("failed to write message to ws")
					continue
				}

			}

		}
	}
}

func extractResponseContent(resp *llms.ContentResponse) (string, error) {
	builder := strings.Builder{}

	for _, choice := range resp.Choices {
		builder.Write([]byte(fmt.Sprintf("%s\n", choice.Content)))
	}
	return builder.String(), nil
}

func formatGenaiParts(codeCtx string, instructions []string) ([]llms.ContentPart, error) {

	if len(instructions) == 0 {
		return nil, errors.New("no instructions provided")
	}

	// Create llms.ContentPart to hold context and instructions
	parts := make([]llms.ContentPart, 0, len(instructions)+1)

	// Add code context
	parts = append(parts, llms.TextPart(codeCtx))

	// Add instructions
	for _, instr := range instructions {
		parts = append(parts, llms.TextPart(instr))
	}

	return parts, nil
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

	j, _ := json.MarshalIndent(schema, "", "  ")
	return string(j)
}
