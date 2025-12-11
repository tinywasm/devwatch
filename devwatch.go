package devwatch

import (
	"sync"
	"time"

	"github.com/tinywasm/depfind"
	"github.com/fsnotify/fsnotify"
)

// FilesEventHandlers unifies asset and Go file event handling.
// It allows handlers to specify which file extensions they support and how to process them.
type FilesEventHandlers interface {
	MainInputFileRelativePath() string // eg: go => "app/server/main.go" | js =>"app/pwa/public/main.js"
	// NewFileEvent handles file events (create, remove, write, rename).
	NewFileEvent(fileName, extension, filePath, event string) error
	SupportedExtensions() []string // eg: [".go"], [".js",".css"], etc.
	UnobservedFiles() []string     // eg: main.exe, main.js
}

// event: create, remove, write, rename
type FolderEvent interface {
	NewFolderEvent(folderName, path, event string) error
}

type WatchConfig struct {
	AppRootDir         string               // eg: "home/user/myNewApp"
	FilesEventHandlers []FilesEventHandlers // All file event handlers are managed here
	FolderEvents       FolderEvent          // when directories are created/removed for architecture detection

	BrowserReload func() error // when change frontend files reload browser

	Logger          func(message ...any) // For logging output
	ExitChan        chan bool            // global channel to signal the exit
	UnobservedFiles func() []string      // files that are not observed by the watcher eg: ".git", ".gitignore", ".vscode",  "examples",
}

type DevWatch struct {
	*WatchConfig
	watcher         *fsnotify.Watcher
	depFinder       *depfind.GoDepFind // Dependency finder for Go projects
	no_add_to_watch map[string]bool
	noAddMu         sync.RWMutex
	// reload timer to debounce browser reloads across multiple events
	reloadTimer *time.Timer
	reloadMutex sync.Mutex
	// logMu           sync.Mutex // No longer needed with Print func
}

func New(c *WatchConfig) *DevWatch {
	dw := &DevWatch{
		WatchConfig: c,
		depFinder:   depfind.New(c.AppRootDir),
	}
	return dw
}
