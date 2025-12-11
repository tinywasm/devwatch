# DevWatch File Detection Strategy

## Overview

DevWatch is a file monitoring library that watches for file changes and triggers appropriate actions based on file types. This document outlines the simplified API design and file detection strategy.

## Problem Statement

The current DevWatch API has multiple event handlers (`FileEventAssets`, `FileEventGO`, `FileEventWASM`) which creates complexity. We need to simplify this to just two main categories: **Frontend** and **Backend** files, with only frontend changes triggering browser reloads.

## Design Decisions

### 1. File Classification Strategy

Files are classified into three categories:

#### Frontend Files (Non-Go)
- **Extensions**: `.css`, `.js`, `.html`, `.svg`, `.png`, `.jpg`, `.ico`, etc.
- **Action**: Trigger browser reload via `FileEventAssets`

#### WASM Files (Go Frontend)
- **Go Files**: `.go` files that belong to a main package containing "wasm" in its name
- **Action**: WASM compilation + browser reload via `FileEventWASM`

#### Backend Files (Go Backend)
- **Go Files**: `.go` files that belong to main packages NOT containing "wasm" in their name
- **Action**: Server restart via `FileEventGO` (no browser reload for backend-only changes)

### 2. Frontend Extensions Management

Frontend file extensions are managed dynamically:
- **Default extensions**: `.html`, `.css`, `.js`, `.svg` (initialized in constructor)
- **Dynamic configuration**: Public method to add more extensions at runtime
- **Centralized logic**: Used in InitialRegistration and file processing

### 3. Go File Detection Method

For `.go` files, we use a dependency finder interface to determine which main package owns the file:

```go
// Dependency finder interface for loose coupling
type DepFinder interface {
    GoFileComesFromMain(fileName string) ([]string, error)
}

// Go file classification implementation
func (h *DevWatch) ClassifyGoFile(fileName string) (frontend, backend bool) {
    if h.DepFinder == nil {
        // Fallback: treat as both types if no dependency finder
        fmt.Fprintln(h.Writer, "Warning: No dependency finder available, treating as both frontend and backend")
        return true, true
    }
    
    mains, err := h.DepFinder.GoFileComesFromMain(fileName)
    if err != nil {
        // On error: treat as both frontend and backend (conservative approach)
        fmt.Fprintln(h.Writer, "Warning: Dependency analysis failed for", fileName, ":", err)
        return true, true
    }
    
    for _, mainPkg := range mains {
        if strings.Contains(strings.ToLower(mainPkg), "wasm") {
            frontend = true
        } else {
            backend = true
        }
    }
    return frontend, backend
}
```

**Benefits of this approach:**
- ✅ **Efficient**: No need to parse file contents
- ✅ **Accurate**: Based on actual dependency analysis
- ✅ **Maintainable**: Uses existing `godepfind` functionality
- ✅ **Flexible**: Supports multiple main packages per file
- ✅ **Conservative**: Treats unknown files as both types (no missed reloads)

### 3. Dependency Interface Design

The dependency finder is abstracted through an interface:
- **Interface**: `DepFinder` with `GoFileComesFromMain(fileName string) ([]string, error)`
- **Decoupling**: DevWatch doesn't depend directly on godepfind
- **Testing**: Easier to mock and test
- **Error handling**: Silent logging to Writer, continues execution

### 4. Error Handling Strategy

The `godepfind` instance is injected as a dependency:

- **Rationale**: Maximum flexibility and control for the user
- **Benefit**: Allows sharing the same dependency finder instance across multiple tools
- **Implementation**: User creates and configures dependency finder before passing to DevWatch
- **Interface**: Uses `DepFinder` interface for loose coupling

### 4. Error Handling Strategy

When dependency finder fails to determine file type:
- **Decision**: Silent logging to Writer + treat as both Frontend + Backend
- **Rationale**: Non-disruptive development workflow
- **Result**: DevBrowser will reload even if classification fails, no execution interruption

### 5. DevBrowser Reload Logic

- **Frontend Assets only** (via FileEventAssets): DevBrowser reload
- **WASM only** (via FileEventWASM): DevBrowser reload 
- **Backend only** (via FileEventGO): No browser reload
- **WASM + Backend**: DevBrowser reload (covers both scenarios)
- **Error/Unknown**: DevBrowser reload (conservative approach)

### 6. Folder Events

Folder events are maintained unchanged for architecture detection purposes.

### 7. API Architecture Decision

- **Decision**: Maintain current handler-based architecture with three distinct handlers
- **Rationale**: Clear separation of concerns, simple configuration, better performance
- **Handlers**: 
  - `FileEventAssets`: Frontend assets (CSS, JS, HTML, etc.)
  - `FileEventWASM`: Go files compiled to WebAssembly
  - `FileEventGO`: Go files for backend services
- **Migration**: Update interface methods but keep handler structure

### 8. Cache Management Decision

**Decision**: Hybrid approach for go.mod monitoring
- **DevWatch**: Monitors `go.mod` as Backend file type
- **godepfind**: Provides `InvalidateCache()` method
- **BackendEvent handler**: Calls cache invalidation when go.mod changes
- **Rationale**: Maintains separation of concerns while ensuring cache consistency

### 9. Event Subscription Architecture - DECISION FINAL

**Decision**: **MANTENER ARQUITECTURA ACTUAL** 

**Rationale**:
- ✅ **Simplicidad**: Fácil de entender y configurar
- ✅ **Clarity**: Tres handlers específicos con responsabilidades claras
- ✅ **Performance**: Acceso directo sin overhead de mapas
- ✅ **Type Safety**: Errores detectados en compile-time
- ✅ **Maintainability**: Código más fácil de mantener y debuggear

**Final Architecture**:
```go
type WatchConfig struct {
    AppRootDir      string      // Root directory
    DepFinder       DepFinder   // Interface for dependency analysis
    FileEventAssets FileEvent   // Frontend assets → browser reload
    FileEventWASM   FileEvent   // Go WASM files → browser reload  
    FileEventGO     FileEvent   // Go backend files → server restart
    FolderEvents    FolderEvent // Folder changes → architecture detection
    BrowserReload   func() error
    Writer          io.Writer
    ExitChan        chan bool
    UnobservedFiles func() []string
}
```

**Benefits of this decision**:
- Clear mental model: Assets → WASM → Backend
- Easy configuration and testing
- Predictable behavior
- No complexity of subscription management

## Simplified API Design

```go
type DepFinder interface {
    GoFileComesFromMain(fileName string) ([]string, error)
}

type WatchConfig struct {
    AppRootDir      string      // Root directory to watch
    DepFinder       DepFinder   // Interface for dependency analysis (loose coupling)
    FileEventAssets FileEvent   // Handles frontend assets → browser reload
    FileEventWASM   FileEvent   // Handles Go WASM files → browser reload
    FileEventGO     FileEvent   // Handles Go backend files → server restart
    FolderEvents    FolderEvent // Handles folder changes → architecture detection
    BrowserReload   func() error // DevBrowser reload function
    Writer          io.Writer   // Logging output
    ExitChan        chan bool   // Exit signal
    UnobservedFiles func() []string // Files to ignore
}

type FileEvent interface {
    NewFileEvent(fileName, extension, filePath, event string) error
}

type FolderEvent interface {
    NewFolderEvent(folderName, path, event string) error
}
```

## Implementation Architecture

### File Processing Flow

```
File Change Detected
         ↓
    Get File Extension
         ↓
┌─────────────────────┐
│  Frontend Assets?   │ → (.css,.js,.html,.svg) → FileEventAssets → DevBrowser Reload
│  (.css,.js,.html)   │
└─────────────────────┘
         ↓
┌─────────────────────┐
│    Go Files?        │
│     (.go)           │
└─────────────────────┘
         ↓
   Use DepFinder to find
   which main(s) own file
         ↓
┌─────────────────────┐
│  Check main names   │
│  for "wasm"         │
└─────────────────────┘
         ↓
┌─────────────────────┐    ┌─────────────────────┐
│   Contains "wasm"   │    │  No "wasm" found    │
│   → WASM Frontend   │    │  → Backend          │
└─────────────────────┘    └─────────────────────┘
         ↓                           ↓
   FileEventWASM                FileEventGO
         ↓                           ↓
   DevBrowser Reload              No DevBrowser Reload
```

### Integration with Dependency Finder

DevWatch integrates with a dependency finder through interface:

1. **Initialize**: Create a dependency finder instance (e.g., `godepfind.New(appRootDir)`)
2. **Inject**: Pass as `DepFinder` interface to DevWatch
3. **Query**: Use `GoFileComesFromMain(fileName)` to get main packages
4. **Classify**: Check if any main package name contains "wasm"
5. **Route**: Send to `FileEventWASM` or `FileEventGO` accordingly

## Performance Considerations

- **Caching**: godepfind provides intelligent caching (~15ms → ~0.0002ms per query)
- **Lazy Loading**: Dependency analysis only runs when needed
- **Selective Updates**: Cache invalidation only for affected packages

## Cache Management Analysis

### go.mod File Monitoring - Analysis

**Question**: Should DevWatch monitor `go.mod` files for automatic cache invalidation?

**Analysis Results**:

#### ✅ **Arguments FOR** DevWatch handling go.mod:
1. **Complete File Monitoring**: DevWatch already monitors all files in the project
2. **User Experience**: Automatic cache invalidation without manual intervention  
3. **Development Workflow**: go.mod changes are common during development
4. **Performance**: godepfind cache becomes stale when dependencies change

#### ❌ **Arguments AGAINST** (Outside DevWatch's Responsibility):
1. **Single Responsibility**: DevWatch focuses on file change detection, not dependency management
2. **Separation of Concerns**: Cache management belongs to godepfind, not file watcher
3. **Complexity**: Adds go.mod parsing logic to DevWatch
4. **Tight Coupling**: Creates dependency between DevWatch and Go module system

#### 🎯 **Recommended Solution**: **Hybrid Approach**

**DevWatch Responsibility**: 
- Monitor `go.mod` files as regular files
- Treat `go.mod` changes as "Backend" file type (since they affect Go compilation)
- Pass go.mod events to BackendEvent handler

**godepfind Responsibility**:
- Provide `InvalidateCache()` method for manual cache clearing
- Handle cache invalidation internally based on file change events
- Maintain cache consistency within its own domain

**Implementation**:
```go
// In BackendEvent handler
func (h *BackendHandler) NewFileEvent(fileName, ext, path, event string) error {
    // Handle go.mod changes
    if fileName == "go.mod" {
        // Clear godepfind cache
        h.depFinder.InvalidateCache()
        log.Println("go.mod changed - cleared dependency cache")
    }
    
    // Handle other backend files...
    return h.processBackendFile(fileName, path, event)
}
```

**Benefits**:
- ✅ DevWatch remains focused on file detection
- ✅ godepfind manages its own cache lifecycle
- ✅ Automatic cache invalidation when needed
- ✅ Clean separation of responsibilities
- ✅ No tight coupling between components

## Example Usage

```go
import (
    "github.com/tinywasm/devwatch"
    "github.com/tinywasm/depfind"
)

// Frontend assets handler
type AssetsHandler struct{}
func (h *AssetsHandler) NewFileEvent(fileName, ext, path, event string) error {
    fmt.Printf("Frontend asset changed: %s\n", fileName)
    // Process CSS, JS, HTML, etc.
    return nil
}

// WASM handler
type WASMHandler struct{}
func (h *WASMHandler) NewFileEvent(fileName, ext, path, event string) error {
    fmt.Printf("WASM Go file changed: %s\n", fileName)
    // Compile to WebAssembly
    return nil
}

// Backend handler  
type BackendHandler struct{}
func (h *BackendHandler) NewFileEvent(fileName, ext, path, event string) error {
    fmt.Printf("Backend Go file changed: %s\n", fileName)
    // Restart server
    return nil
}

// Configuration
depFinder := godepfind.New("/path/to/project")  // Implements DepFinder interface
config := &devwatch.WatchConfig{
    AppRootDir:      "/path/to/project",
    DepFinder:       depFinder,
    FileEventAssets: &AssetsHandler{},
    FileEventWASM:   &WASMHandler{},
    FileEventGO:     &BackendHandler{},
    BrowserReload: func() error {
        fmt.Println("Reloading browser...")
        return nil
    },
    Writer:   os.Stdout,
    ExitChan: make(chan bool),
}

// Start watching
watcher := devwatch.New(config)
watcher.Start()
```

## Migration Strategy

1. **Phase 1**: Implement new simplified API (breaking change)
2. **Phase 2**: Add godepfind integration for Go file classification  
3. **Phase 3**: Update event handling logic to use new classification
4. **Phase 4**: Update documentation and examples

**Breaking Changes:**
- Interface methods renamed: `NewFileEvent` → `NewFileEvent`, `NewFolderEvent` → `NewFolderEvent` 
- New required field: `DepFinder` interface (can be `godepfind` instance)
- `FileEventAssets`, `FileEventWASM`, `FileEventGO` kept for clarity (no consolidation)

## Testing Strategy

- **Unit Tests**: File classification logic with various scenarios
- **Integration Tests**: godepfind integration with real Go projects
- **Performance Tests**: Ensure caching provides expected speedup
- **End-to-End Tests**: DevBrowser reload triggers for frontend changes only
