package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	ctxtypes "github.com/cyber-nic/ctx/libs/types"
	ctxutils "github.com/cyber-nic/ctx/libs/utils"

	"github.com/cyber-nic/ctx/apps/client/mapper"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const (
	ctxIgnoreFile        = ".ctxignore"
	debugCodeContextFile = "code.ctx"
)

// application entrypoint
func main() {
	var addr = flag.String("addr", "localhost:8000", "http service address")
	var debug = flag.Bool("debug", false, "enable debug mode")
	flag.Parse()

	ctxutils.ConfigLogging(debug)

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal().Err(err).Msg("Error getting current working directory")
		return
	}

	// Load the ignore list
	// tr@ck - combine .ctxignore with .gitignore
	ignoreList := loadIgnoreList(filepath.Join(cwd, ctxIgnoreFile))

	rootNode, err := getContextFileTree(cwd, ignoreList)
	if err != nil {
		log.Fatal().Err(err).Msg("Error getting folder structure")
	}

	appCtx := ctxtypes.ApplicationContext{FileSystem: rootNode}

	jsonData, err := json.Marshal(appCtx)
	if err != nil {
		log.Fatal().Err(err).Msg("Error marshalling JSON")
	}

	log.Trace().Int("len", len(jsonData)).Msg("Application context")

	// Create channels for coordination
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	stop := make(chan bool)
	prompt := make(chan string, 1)

	// Create WaitGroup to track goroutines
	var wg sync.WaitGroup

	// Setup WebSocket connection
	wsconn := url.URL{Scheme: "ws", Host: *addr, Path: "/data"}
	log.Printf("connecting to %s", wsconn.String())

	c, _, err := websocket.DefaultDialer.Dial(wsconn.String(), nil)
	if err != nil {
		log.Fatal().Err(err).Msg("dial")
	}
	defer c.Close()

	// immediately send a message containing the application context so as to cache it on the server / ai
	go func() {
		msg := ctxtypes.CtxRequest{Step: ctxtypes.CtxStepPreload, Context: appCtx}

		msgData, err := json.Marshal(msg)
		if err != nil {
			log.Fatal().Err(err).Msg("Error marshalling JSON")
		}

		if err := c.WriteMessage(websocket.TextMessage, msgData); err != nil {
			log.Err(err).Msg("write")
			close(done)
			return
		}
	}()

	// Message handler goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			case p := <-prompt:

				// send the app context with the user prompt
				selectFilesPrompt := fmt.Sprintf("Consider the previously provided application context. Return the list of files required to implement the requirements or instructions explained in the following user prompt: ```%s```. Return JSON.", p)
				msg := ctxtypes.CtxRequest{Context: appCtx, Instructions: []string{selectFilesPrompt}}

				msgData, err := json.Marshal(msg)
				if err != nil {
					log.Fatal().Err(err).Msg("Error marshalling JSON")
				}

				// Send the payload to the server
				if err := c.WriteMessage(websocket.TextMessage, msgData); err != nil {
					log.Err(err).Msg("write")
					close(done)
					return
				}
			case sig := <-interrupt:
				log.Info().Msgf("Received signal: %v", sig)

				// Send close frame to server
				closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")

				// Write the close frame to the server
				if err := c.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
					log.Err(err).Msg("write close")
					close(done)
					return
				}

				// Wait for server to close the connection or timeout
				go func() {
					// Read messages until we get an error (which should be a close frame)
					for {
						_, _, err := c.ReadMessage()
						if err != nil {
							if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
								log.Info().Msg("Received close frame from server")
							} else {
								log.Warn().Err(err).Msg("Unexpected error while waiting for close frame")
							}
							close(done)
							return
						}
					}
				}()

				// Wait for either done signal or timeout
				select {
				case <-done:
					return
				case <-time.After(time.Second):
					log.Warn().Msg("Timeout waiting for server to close connection")
					close(done)
					return
				}
			}
		}
	}()

	// Input handler goroutine with non-blocking read
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Create a channel for input
		inputCh := make(chan string)

		// Goroutine for reading input
		go func() {
			reader := bufio.NewReader(os.Stdin)
			for {
				fmt.Printf("Enter text: ")
				input, err := reader.ReadString('\n')
				if err != nil {
					log.Error().Err(err).Msg("Error reading input")
					close(done)
					return
				}
				input = strings.TrimSpace(input)
				if input != "" {
					inputCh <- input
				}
			}
		}()

		// Goroutine for processing input
		for {
			select {
			case <-done:
				// Send an interrupt to stdin to unblock ReadString
				// This is platform specific and may not work on all systems
				p, err := os.FindProcess(os.Getpid())
				if err == nil {
					p.Signal(os.Interrupt)
				}
				return
			case input := <-inputCh:
				select {
				case <-done:
					return
				case prompt <- input:
					spinnerDone := make(chan struct{})
					go func() {
						showSpinner(stop)
						close(spinnerDone)
					}()

					_, message, err := c.ReadMessage()
					if err != nil {
						if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
							log.Info().Msg("Connection closed by server")
						} else {
							log.Err(err).Msg("Error reading message")
						}
						close(done)
						return
					}

					stop <- true
					<-spinnerDone

					fmt.Print(string(message))
					time.Sleep(100 * time.Millisecond)
				}
			}
		}
	}()

	// Wait for done signal
	<-done

	// Wait for goroutines to finish
	wg.Wait()

	// Close channels
	close(stop)
	close(prompt)

	log.Info().Msg("Graceful termination")
}

func parseFile(filePath string) ([]string, error) {
	filePath = strings.Replace(filePath, "./", "", 1)

	language := getLanguage(filePath)

	if language == nil {
		return nil, fmt.Errorf("unsupported file: %s", filePath)
	}

	code, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %s", filePath)
	}

	parser := sitter.NewParser()
	defer parser.Close()

	parser.SetLanguage(language)

	// Parse the file with optional old tree for incremental parsing
	tree := parser.Parse(code, nil)
	log.Trace().Str("path", filePath).Msg("Parsed")

	root := tree.RootNode()

	// tr@ck -- this isn't working, but is necessary imo
	// // Check for errors
	// if hasErr, _ := hasErrors(root); hasErr {
	// 	return "", fmt.Errorf("parsing errors detected")
	// }

	// Build the code map
	codeMap, err := mapper.GetCodeMap(root, filePath, code)
	if err != nil {
		return nil, fmt.Errorf("failed to build code map: %w", err)
	}

	return codeMap, nil
}

func matchesIgnoreList(path string, ignoreList []string) bool {
	for _, pattern := range ignoreList {
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
		if strings.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}

func getContextFileTree(dirPath string, ignoreList []string) (map[string]ctxtypes.FileSystemNode, error) {
	// Initialize the root node as a directory with an empty map for its children
	root := &ctxtypes.FileSystemNode{Directory: true, Children: make(map[string]*ctxtypes.FileSystemNode)}

	// Walk through the directory tree
	err := filepath.Walk(dirPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err // Propagate errors encountered during traversal
		}

		// Get the relative path from the root directory
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err // Return an error if the relative path cannot be determined
		}
		if relPath == "." {
			return nil // Skip the root directory itself
		}

		// Split the relative path into parts to navigate the tree
		parts := strings.Split(relPath, string(os.PathSeparator))
		node := root
		for _, part := range parts[:len(parts)-1] {
			if child, exists := node.Children[part]; exists {
				node = child // Navigate to the existing child node
			} else {
				// Create a new directory node if it doesn't exist
				newNode := &ctxtypes.FileSystemNode{Directory: true, Children: make(map[string]*ctxtypes.FileSystemNode)}
				node.Children[part] = newNode
				node = newNode
			}
		}

		// Extract the name of the current file or directory
		name := parts[len(parts)-1]

		// Check if the path matches the ignore list
		if matchesIgnoreList(path, ignoreList) {
			n := ctxtypes.FileSystemNode{Ignore: true}
			if info.IsDir() {
				n.Directory = true
			}
			// Mark the node as ignored
			node.Children[name] = &n
			if info.IsDir() {
				return filepath.SkipDir // Skip ignored directories
			}
			return nil
		}

		// Add the node to the tree
		if info.IsDir() {
			// If the current item is a directory, create a node with an empty children map
			node.Children[name] = &ctxtypes.FileSystemNode{
				Directory: true,
				Children:  make(map[string]*ctxtypes.FileSystemNode),
			}
		} else {
			// Parse the file for keywords
			keywords, err := parseFile(relPath)
			if err != nil {
				log.Trace().Err(err).Msgf("Failed to parse file: %s", relPath)
				return nil
			}

			// If the current item is a file, create a node without children
			node.Children[name] = &ctxtypes.FileSystemNode{
				Keywords: keywords,
			}
		}

		// Log the addition to the tree
		log.Debug().Str("path", path).Msg("Added to tree")

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory (%s): %w", dirPath, err)
	}

	// Wrap the root node in a map with the root directory path as the key
	rootNode := map[string]ctxtypes.FileSystemNode{dirPath: *root}

	return rootNode, nil
}
