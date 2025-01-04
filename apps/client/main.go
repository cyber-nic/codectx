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
	"sync/atomic"
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
	ctxIgnoreFile = ".ctxignore"
)

// application entrypoint
func main() {
	// Setup signal handling to gracefully shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Signal handling
	go func() {
		<-sigChan // Wait for SIGINT or SIGTERM
		log.Trace().Msg("SIG(INT|TERM)")

		// Start a new goroutine to listen for a second SIGINT or SIGTERM
		go func() {
			<-sigChan // Wait for second SIGINT or SIGTERM
			log.Fatal().Msg("Immediate shutdown initiated.")
		}()

		time.AfterFunc(10*time.Second, func() {
			log.Fatal().Msg("Graceful shutdown timed out.")
		})

		// All ongoing operations completed
		log.Info().Msg("Graceful shutdown")
		os.Exit(0)
	}()

	var addr = flag.String("addr", "localhost:8000", "http service address")
	var debug = flag.Bool("debug", false, "enable debug mode")
	flag.Parse()

	ctxutils.ConfigLogging(debug)

	// Get the MAC address of the host machine to identify unauthenticated users. Skip if logged in
	macAddr, err := getMacAddr()
	if err != nil {
		log.Fatal().Err(err).Msg("Error getting MAC address")
	}
	log.Trace().Str("client_id", macAddr).Msg("client")

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

	appCtx := ctxtypes.ApplicationContext{
		FileSystemDetails: []string{
			"'Skip' signifies that the file or directory exists, but content is ignored",
		},
		FileSystem: rootNode,
	}

	// Create channels for coordination
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	// Setup WebSocket connection
	wsconn := url.URL{Scheme: "ws", Host: *addr, Path: "/data"}
	log.Printf("connecting to %s", wsconn.String())

	ws, _, err := websocket.DefaultDialer.Dial(wsconn.String(), nil)
	if err != nil {
		log.Fatal().Err(err).Msg("dial")
	}
	defer ws.Close()

	// STEP 1: PRELOAD
	{
		// immediately send a message containing the application context so as to cache it on the server / ai
		msg := ctxtypes.CtxRequest{
			ClientID: macAddr,
			Step:     ctxtypes.CtxStepLoadContext,
			Context:  appCtx,
		}

		msgData, err := json.Marshal(msg)
		if err != nil {
			log.Fatal().Err(err).Msg("Error marshalling JSON")
		}

		if err := ws.WriteMessage(websocket.TextMessage, msgData); err != nil {
			log.Err(err).Msg("write")
		}
	}

	// STEP 2: SELECT
	var waitForIt atomic.Bool
	waitForIt.Store(true)
	userPrompt := ""

	// Goroutine for reading input
	reader := bufio.NewReader(os.Stdin)
	for waitForIt.Load() {
		fmt.Printf("Instruction: ")
		userPrompt, err := reader.ReadString('\n')

		if err != nil {
			waitForIt.Store(false)
			log.Error().Err(err).Msg("Error reading input")
			return
		}

		userPrompt = strings.TrimSpace(userPrompt)
		if userPrompt == "" {
			continue
		}

		waitForIt.Store(false)
		log.Info().Str("value", userPrompt).Msg("input")

		// send the app context with the user prompt
		msg := ctxtypes.CtxRequest{
			ClientID:   macAddr,
			Step:       ctxtypes.CtxStepFileSelection,
			Context:    appCtx,
			UserPrompt: userPrompt,
		}

		msgData, err := json.Marshal(msg)
		if err != nil {
			log.Fatal().Err(err).Msg("Error marshalling JSON")
		}

		// Send the payload to the server
		if err := ws.WriteMessage(websocket.TextMessage, msgData); err != nil {
			log.Err(err).Msg("write")
			return
		}
	}

	// Unmarshal to StepFileSelectResponseSchema
	var selectResp ctxtypes.StepFileSelectResponseSchema

	waitForIt.Store(true)

	// fetch files to update
	for waitForIt.Load() {
		_, message, err := ws.ReadMessage()
		waitForIt.Store(false)

		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Info().Msg("Connection closed by server")
			} else {
				log.Err(err).Msg("Error reading message")
			}
			return
		}

		if err := json.Unmarshal(message, &selectResp); err != nil {
			log.Err(err).Msg("Error unmarshalling JSON")
			return
		}

		ctxutils.PrintStructOut(selectResp)
	}

	// STEP 4: WORK

	// create list of file contents requested by the server
	filesContents := map[string]string{}

	{
		// include create and update files
		for _, file := range selectResp.Data.Files {
			if file.Operation != ctxtypes.FileOperationUpdate {
				continue
			}

			// read the file contents
			content, err := os.ReadFile(file.Path)
			if err != nil {
				log.Err(err).Msg("Error reading file")
				continue
			}
			filesContents[file.Path] = string(content)

		}
		// include additional context files
		for _, file := range selectResp.Data.Additional {
			// read the file contents
			content, err := os.ReadFile(file.Path)
			if err != nil {
				log.Err(err).Msg("Error reading file")
				continue
			}
			filesContents[file.Path] = string(content)
		}
	}

	// append the file contents
	appCtx.FileContents = filesContents

	// request individual file changes
	for _, file := range selectResp.Data.Files {

		// create a new version of the file
		fileContentWithLineNumbers := fmt.Sprintf("# %s\n\n", file.Path)

		// add line numbers to the file content
		if file.Operation == ctxtypes.FileOperationUpdate {
			// read the file line by line and create a new version where each line is prefixed with the line number
			fileContents, err := os.ReadFile(file.Path)
			if err != nil {
				log.Err(err).Msg("Error reading file")
				continue
			}

			scanner := bufio.NewScanner(strings.NewReader(string(fileContents)))
			lineNumber := 1
			for scanner.Scan() {
				fileContentWithLineNumbers += fmt.Sprintf("%d | %s\n", lineNumber, scanner.Text())
				lineNumber++
			}
		}

		// fmt.Println(fileContentWithLineNumbers)

		// request, wait and print changes
		msg := ctxtypes.CtxRequest{
			ClientID:   macAddr,
			Step:       ctxtypes.CtxStepCodeWork,
			Context:    appCtx,
			UserPrompt: userPrompt,
			WorkPrompt: fileContentWithLineNumbers,
		}

		msgData, err := json.Marshal(msg)
		if err != nil {
			log.Fatal().Err(err).Msg("Error marshalling JSON")
		}

		if err := ws.WriteMessage(websocket.TextMessage, msgData); err != nil {
			log.Err(err).Msg("write")
		}

		// Unmarshal to StepFileSelectResponseSchema
		var workResp ctxtypes.StepFileWorkResponseSchema

		var waitForIt atomic.Bool
		waitForIt.Store(true)

		// fetch files to update
		for waitForIt.Load() {
			_, message, err := ws.ReadMessage()
			waitForIt.Store(false)

			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Info().Msg("Connection closed by server")
				} else {
					log.Err(err).Msg("Error reading message")
				}
				return
			}

			if err := json.Unmarshal(message, &workResp); err != nil {
				log.Err(err).Msg("Error unmarshalling JSON")
				return
			}

			fmt.Printf("# %s\n", file.Path)
			ctxutils.PrintStructOut(workResp.Data)
		}

	}

	// Close channels
	close(interrupt)

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
		// name := parts[len(parts)-1]

		// Check if the path matches the ignore list
		if matchesIgnoreList(path, ignoreList) {
			n := ctxtypes.FileSystemNode{Skip: true}
			if info.IsDir() {
				n.Directory = true
			}
			// Mark the node as ignored
			node.Children[relPath] = &n
			if info.IsDir() {
				return filepath.SkipDir // Skip ignored directories
			}
			return nil
		}

		// Add the node to the tree
		if info.IsDir() {
			// If the current item is a directory, create a node with an empty children map
			node.Children[relPath] = &ctxtypes.FileSystemNode{
				Directory: true,
				Children:  make(map[string]*ctxtypes.FileSystemNode),
			}
		} else {
			// Parse the file for keywords
			if keywords, err := parseFile(relPath); err != nil {
				node.Children[relPath] = &ctxtypes.FileSystemNode{}
			} else {
				// If the current item is a file, create a node without children
				node.Children[relPath] = &ctxtypes.FileSystemNode{Keywords: keywords}
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
