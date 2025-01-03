package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func showSpinner(done chan bool) {
	spinnerChars := []rune{'|', '/', '-', '\\'}
	for {
		select {
		case <-done:
			// Stop the spinner when done
			fmt.Printf("\r") // Clear the spinner line
			return
		default:
			// Display spinner animation
			for _, r := range spinnerChars {
				fmt.Printf("\rProcessing... %c", r)
				time.Sleep(100 * time.Millisecond)
				select {
				case <-done:
					fmt.Printf("\r") // Clear the spinner line
					return
				default:
					// Continue spinning
				}
			}
		}
	}
}

func loadIgnoreList(ignoreFilePath string) []string {
	ignoreList := make(map[string]struct{})

	file, err := os.Open(ignoreFilePath)
	if err != nil {
		log.Warn().Msgf("Failed to load ignore file: %s", ignoreFilePath)
		return []string{}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			ignoreList[line] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Warn().Err(err).Msgf("Error reading ignore file: %s", ignoreFilePath)
	}

	keys := make([]string, 0, len(ignoreList))
	// convert hash to array
	for key := range ignoreList {
		keys = append(keys, key)
	}

	return keys
}

func getLanguage(path string) *sitter.Language {
	// return docker if filepath begins with Dockerfile"
	if strings.HasPrefix(path, "Dockerfile") {
		return sitter.NewLanguage(tree_sitter_go.Language())
	}

	ext := filepath.Ext(path)

	switch ext {
	case ".go":
		return sitter.NewLanguage(tree_sitter_go.Language())
	case ".jsx":
		return sitter.NewLanguage(tree_sitter_javascript.Language())
	case ".js":
		return sitter.NewLanguage(tree_sitter_javascript.Language())
	case ".py":
		return sitter.NewLanguage(tree_sitter_python.Language())
	case ".tsx":
		return sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	case ".ts":
		return sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	default:
		return nil
	}
}
