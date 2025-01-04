package ctxtypes

// FileSystemNode represents a node in a file system tree
type FileSystemNode struct {
	Directory bool                       `json:"dir,omitempty"`
	Children  map[string]*FileSystemNode `json:"children,omitempty"`
	Skip      bool                       `json:"skip,omitempty"`
	Keywords  []string                   `json:"keywords,omitempty"`
}

type ApplicationContext struct {
	FileSystem        map[string]FileSystemNode `json:"fs,omitempty"`
	FileSystemDetails []string                  `json:"fs_details,omitempty"`
	FileContents      map[string]string         `json:"file_contents,omitempty"`
}

type CtxStep string

const (
	CtxStepLoadContext   CtxStep = "load"
	CtxStepFileSelection CtxStep = "select"
	CtxStepCodeWork      CtxStep = "work"
)

// CtxRequest represents a message sent from client to server
type CtxRequest struct {
	ClientID   string             `json:"clientID"`
	Context    ApplicationContext `json:"context,omitempty"`
	Step       CtxStep            `json:"step"`
	UserPrompt string             `json:"userPrompt,omitempty"`
	WorkPrompt string             `json:"workPrompt,omitempty"`
}

// CtxResponse represents a message sent from server to client
type CtxResponse struct {
	DisplayMessage string   `json:"display_message,omitempty"`
	Instructions   []string `json:"instructions,omitempty"`
}

type StepPreloadResponseSchema struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type FileOperation int

const (
	FileOperationRemove FileOperation = -1
	FileOperationUpdate FileOperation = 0
	FileOperationCreate FileOperation = 1
)

type StepFileSelectItem struct {
	Operation FileOperation
	Path      string
	Reason    string
}

type StepFileSelectFiles struct {
	Files      []StepFileSelectItem `json:"files"`
	Additional []StepFileSelectItem `json:"additional_context_files"`
}

type StepFileSelectResponseSchema struct {
	Timestamp string              `json:"timestamp"`
	Step      string              `json:"step"`
	Status    string              `json:"status"`
	Data      StepFileSelectFiles `json:"data"`
}

type PatchData struct {
	Patch string `json:"patch"`
}

type StepFileWorkResponseSchema struct {
	Timestamp string    `json:"timestamp"`
	Step      string    `json:"step"`
	Status    string    `json:"status"`
	Data      PatchData `json:"data"`
}
