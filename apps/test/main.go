package main

import (
	"bufio"
	"context"
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

	"github.com/cyber-nic/ctx/apps/client/mapper"
	ctxtypes "github.com/cyber-nic/ctx/libs/types"
	ctxutils "github.com/cyber-nic/ctx/libs/utils"
	"github.com/gorilla/websocket"

	"github.com/rs/zerolog/log"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const (
	ctxIgnoreFile = ".ctxignore"
)

func main() {
	var addr = flag.String("addr", "localhost:8000", "http service address")
	var debug = flag.Bool("debug", false, "enable debug mode")
	flag.Parse()

	ctxutils.ConfigLogging(debug)

	// Create channel for signal handling
	sigChan := make(chan os.Signal, 1)
	// Register for interrupt (Ctrl+C) and termination signals
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create channel for user input
	inputChan := make(chan string)

	// Create context for graceful shutdown
	ctx, ctxCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Create Web Socket Connection
	wg.Add(1)
	ws := getWebSocketConn(*addr)
	defer ws.Close()

	appCtx, err := getAppContext()
	if err != nil {
		log.Fatal().Err(err).Msg("error getting app context")
	}

	// immediately fire off application context so as to cache early
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info().Msg("send app context")
		if err := sendInitialAppContext(ws, appCtx); err != nil {
			log.Err(err).Msg("failed to send initial app context")
			ctxCancel()
		}

		time.Sleep(15 * time.Second)
		log.Info().Msg("re-send app context")
		if err := sendInitialAppContext(ws, appCtx); err != nil {
			log.Err(err).Msg("failed to send initial app context")
			ctxCancel()
		}
	}()

	// Start goroutine to read user input
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			text, err := reader.ReadString('\n')
			if err != nil {
				log.Err(err).Msg("error reading input")
				close(inputChan)
				return
			}

			select {
			case <-ctx.Done():
				return
			case inputChan <- func() string {
				prompt := strings.TrimSpace(text)

				// send the app context with the user prompt
				msg := ctxtypes.CtxRequest{
					Step:       ctxtypes.CtxStepFileSelection,
					Context:    appCtx,
					UserPrompt: prompt,
				}

				msgData, err := json.Marshal(msg)
				if err != nil {
					log.Fatal().Err(err).Msg("Error marshalling JSON")
				}

				// Send the payload to the server
				if err := ws.WriteMessage(websocket.TextMessage, msgData); err != nil {
					log.Err(err).Msg("write")
				}

				return prompt
			}():

			}
		}
	}()

	// Track if shutting down
	terminate := false

	// Main loop
	textPrompt()
	for {
		select {
		case sig := <-sigChan:
			if terminate {
				// Second signal: immediate exit
				fmt.Printf("\nshutdown forced (%v)\n", sig)
				os.Exit(1)
			}

			// First signal: initiate graceful shutdown
			// fmt.Printf("\nshutdown initiated (%v)\n", sig)
			terminate = true
			ctxCancel()

			// Send 'close' frame to server
			closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			if err := ws.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
				log.Err(err).Msg("write close")
			}
			wg.Done()

			// Start a goroutine to wait for cleanup
			go func() {
				wg.Wait()
				os.Exit(0)
			}()

		case input, ok := <-inputChan:
			if !ok {
				return
			}

			fmt.Printf("You entered: %s\n", input)

			textPrompt()
		}
	}
}

func textPrompt() {
	fmt.Printf("Enter text: ")
}

func getAppContext() (ctxtypes.ApplicationContext, error) {
	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return ctxtypes.ApplicationContext{}, fmt.Errorf("error getting current working directory: %w", err)
	}

	// Load the ignore list
	// tr@ck - combine .ctxignore with .gitignore
	ignoreList := loadIgnoreList(filepath.Join(cwd, ctxIgnoreFile))

	rootNode, err := getContextFileTree(cwd, ignoreList)
	if err != nil {
		log.Fatal().Err(err).Msg("error getting folder structure")
	}

	return ctxtypes.ApplicationContext{FileSystem: rootNode}, nil

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

func getWebSocketConn(addr string) *websocket.Conn {

	// Setup WebSocket connection
	wsconn := url.URL{Scheme: "ws", Host: addr, Path: "/data"}
	log.Trace().Msgf("connecting to %s", wsconn.String())

	// Connect
	ws, _, err := websocket.DefaultDialer.Dial(wsconn.String(), nil)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to dial ws")
	}

	return ws
}

func sendInitialAppContext(ws *websocket.Conn, appCtx ctxtypes.ApplicationContext) error {
	msg := ctxtypes.CtxRequest{Step: ctxtypes.CtxStepLoadContext, Context: appCtx}

	msgData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("error marshalling JSON: %w", err)
	}

	if err := ws.WriteMessage(websocket.TextMessage, msgData); err != nil {
		return fmt.Errorf("failed to send initial app context: %w", err)
	}

	return nil
}
