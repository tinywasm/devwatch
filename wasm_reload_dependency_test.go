package devwatch

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestWasmReloadRaceCondition_DependencyDetection tests that editing a dependency
// file (like greet.go) triggers WASM recompilation
func TestWasmReloadRaceCondition_DependencyDetection(t *testing.T) {
	tempDir := t.TempDir()

	// Create project structure matching the example
	err := os.MkdirAll(tempDir+"/cmd/webclient", 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(tempDir+"/pkg/greet", 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create go.mod
	goModContent := `module example

go 1.25.2

require github.com/cdvelop/tinystring v0.8.3
`
	if err := os.WriteFile(tempDir+"/go.mod", []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create greet.go (dependency)
	greetFile := tempDir + "/pkg/greet/greet.go"
	greetContent := `package greet

import . "github.com/cdvelop/tinystring"

func Greet(target string) string {
	return Fmt("Hello, %s 👋", target, "from Go!!")
}
`
	if err := os.WriteFile(greetFile, []byte(greetContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main.go that imports greet
	mainGoFile := tempDir + "/cmd/webclient/main.go"
	mainGoContent := `package main

import (
	"example/src/pkg/greet"
	"syscall/js"
)

func main() {
	dom := js.Global().Get("document").Call("createElement", "div")
	dom.Set("innerHTML", greet.Greet("WebAssembly!"))
	body := js.Global().Get("document").Get("body")
	body.Call("appendChild", dom)
	select {}
}
`
	if err := os.WriteFile(mainGoFile, []byte(mainGoContent), 0644); err != nil {
		t.Fatal(err)
	}

	var compilationCount int32
	var reloadCount int64

	wasmHandler := &CountingCompilingHandler{
		compilationCount:     &compilationCount,
		compilationDuration:  100 * time.Millisecond,
		SupportedExtensions_: []string{".go"},
		MainInputFile:        "src/cmd/webclient/main.go",
	}

	reloadCalled := make(chan struct{}, 10)

	config := &WatchConfig{
		AppRootDir:         tempDir,
		FilesEventHandlers: []FilesEventHandlers{wasmHandler},
		BrowserReload: func() error {
			atomic.AddInt64(&reloadCount, 1)
			reloadCalled <- struct{}{}
			return nil
		},
		Logger:   func(message ...any) { t.Log(message...) },
		ExitChan: make(chan bool, 1),
	}

	w := New(config)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	w.watcher = watcher
	defer watcher.Close()

	go w.watchEvents()
	time.Sleep(100 * time.Millisecond)

	t.Log("=== Edit 1: Modify main.go (should compile) ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("Edit 1: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("Edit 1: Timeout waiting for reload")
	}

	time.Sleep(500 * time.Millisecond)

	t.Log("=== Edit 2: Modify greet.go (dependency - should compile) ===")
	watcher.Events <- fsnotify.Event{
		Name: greetFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("Edit 2: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("Edit 2: Timeout waiting for reload")
	}

	time.Sleep(100 * time.Millisecond)

	w.ExitChan <- true
	time.Sleep(100 * time.Millisecond)

	compilations := atomic.LoadInt32(&compilationCount)
	reloads := atomic.LoadInt64(&reloadCount)

	t.Log("\n=== Results ===")
	t.Logf("Main.go edits: 1")
	t.Logf("Greet.go edits: 1")
	t.Logf("Total compilations: %d", compilations)
	t.Logf("Total reloads: %d", reloads)

	// We expect 2 compilations: one for main.go, one for greet.go
	if compilations < 2 {
		t.Errorf("❌ BUG CONFIRMED: Only %d compilations for 2 edits!", compilations)
		t.Errorf("   greet.go edit did NOT trigger compilation!")
		t.Errorf("   This means browser reloaded with STALE WASM code!")
		t.Errorf("   godepfind.ThisFileIsMine() is NOT detecting the dependency!")
	} else {
		t.Logf("✅ CORRECT: Both main.go and greet.go edits triggered compilation")
	}

	if reloads < 2 {
		t.Errorf("❌ Reload count: %d (expected 2)", reloads)
	}
}

// TestWasmReloadRaceCondition_MultipleDependencies tests multiple dependency files
func TestWasmReloadRaceCondition_MultipleDependencies(t *testing.T) {
	tempDir := t.TempDir()

	// Create full project structure
	err := os.MkdirAll(tempDir+"/src/cmd/webclient", 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(tempDir+"/src/pkg/greet", 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(tempDir+"/src/pkg/helper", 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create go.mod
	goModContent := `module example

go 1.25.2
`
	if err := os.WriteFile(tempDir+"/go.mod", []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create helper.go
	helperFile := tempDir + "/src/pkg/helper/helper.go"
	helperContent := `package helper

func Format(s string) string {
	return ">>> " + s + " <<<"
}
`
	if err := os.WriteFile(helperFile, []byte(helperContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create greet.go that imports helper
	greetFile := tempDir + "/src/pkg/greet/greet.go"
	greetContent := `package greet

import "example/src/pkg/helper"

func Greet(target string) string {
	return helper.Format("Hello, " + target)
}
`
	if err := os.WriteFile(greetFile, []byte(greetContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create main.go that imports greet
	mainGoFile := tempDir + "/src/cmd/webclient/main.go"
	mainGoContent := `package main

import (
	"example/src/pkg/greet"
	"syscall/js"
)

func main() {
	dom := js.Global().Get("document").Call("createElement", "div")
	dom.Set("innerHTML", greet.Greet("WebAssembly!"))
	body := js.Global().Get("document").Get("body")
	body.Call("appendChild", dom)
	select {}
}
`
	if err := os.WriteFile(mainGoFile, []byte(mainGoContent), 0644); err != nil {
		t.Fatal(err)
	}

	var compilationCount int32
	var reloadCount int64

	wasmHandler := &CountingCompilingHandler{
		compilationCount:     &compilationCount,
		compilationDuration:  100 * time.Millisecond,
		SupportedExtensions_: []string{".go"},
		MainInputFile:        "src/cmd/webclient/main.go",
	}

	reloadCalled := make(chan struct{}, 10)

	config := &WatchConfig{
		AppRootDir:         tempDir,
		FilesEventHandlers: []FilesEventHandlers{wasmHandler},
		BrowserReload: func() error {
			atomic.AddInt64(&reloadCount, 1)
			reloadCalled <- struct{}{}
			return nil
		},
		Logger:   func(message ...any) { t.Log(message...) },
		ExitChan: make(chan bool, 1),
	}

	w := New(config)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	w.watcher = watcher
	defer watcher.Close()

	go w.watchEvents()
	time.Sleep(100 * time.Millisecond)

	// Edit helper.go (transitive dependency)
	t.Log("=== Editing helper.go (transitive dependency) ===")
	watcher.Events <- fsnotify.Event{
		Name: helperFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("helper.go: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("helper.go: Timeout")
	}

	time.Sleep(200 * time.Millisecond)

	// Edit greet.go (direct dependency)
	t.Log("=== Editing greet.go (direct dependency) ===")
	watcher.Events <- fsnotify.Event{
		Name: greetFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("greet.go: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("greet.go: Timeout")
	}

	time.Sleep(200 * time.Millisecond)

	// Edit main.go
	t.Log("=== Editing main.go (main file) ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("main.go: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("main.go: Timeout")
	}

	time.Sleep(100 * time.Millisecond)

	w.ExitChan <- true
	time.Sleep(100 * time.Millisecond)

	compilations := atomic.LoadInt32(&compilationCount)
	reloads := atomic.LoadInt64(&reloadCount)

	t.Log("\n=== Results ===")
	t.Logf("Total edits: 3 (helper.go, greet.go, main.go)")
	t.Logf("Compilations: %d", compilations)
	t.Logf("Reloads: %d", reloads)

	if compilations < 3 {
		t.Errorf("❌ DEPENDENCY DETECTION FAILED: Only %d compilations for 3 edits", compilations)
		if compilations == 1 {
			t.Errorf("   Only main.go triggered compilation - dependencies NOT detected!")
		}
	} else {
		t.Logf("✅ All dependencies correctly detected and compiled")
	}
}
