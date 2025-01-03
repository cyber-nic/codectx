package ctxtypes

// FileSystemNode represents a node in a file system tree
type FileSystemNode struct {
	Directory bool                       `json:"dir,omitempty"`
	Children  map[string]*FileSystemNode `json:"children,omitempty"`
	Ignore    bool                       `json:"ignore,omitempty"`
	Keywords  []string                   `json:"keywords,omitempty"`
}

type ApplicationContext struct {
	FileSystem map[string]FileSystemNode `json:"fs,omitempty"`
}

type CtxStep string

const (
	CtxStepLoadContext   CtxStep = "load"
	CtxStepFileSelection CtxStep = "select"
	CtxStepCodeWork      CtxStep = "work"
)

// CtxRequest represents a message sent from client to server
type CtxRequest struct {
	Context    ApplicationContext `json:"context,omitempty"`
	Step       CtxStep            `json:"step"`
	UserPrompt string             `json:"userPrompt,omitempty"`
}

// CtxResponse represents a message sent from server to client
type CtxResponse struct {
	DisplayMessage string   `json:"display_message,omitempty"`
	Instructions   []string `json:"instructions,omitempty"`
}
