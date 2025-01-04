package main

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// getMacAddr gets the MAC hardware
// address of the host machine
func getMacAddr() (string, error) {
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, i := range interfaces {
			if i.Flags&net.FlagUp != 0 && bytes.Compare(i.HardwareAddr, nil) != 0 {
				// Don't use random as we have a real address
				return i.HardwareAddr.String(), nil
			}
		}
	}
	return "", errors.New("could not get MAC address")
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
