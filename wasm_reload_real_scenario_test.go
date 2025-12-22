package devwatch

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// LoggingHandler wraps another handler and logs compilation events
type LoggingHandler struct {
	inner     FilesEventHandlers
	onCompile func(count int32)
	onFinish  func(count int32)
	count     int32
}

func (h *LoggingHandler) NewFileEvent(fileName, extension, filePath, event string) error {
	count := atomic.AddInt32(&h.count, 1)
	if h.onCompile != nil {
		h.onCompile(count)
	}
	err := h.inner.NewFileEvent(fileName, extension, filePath, event)
	if h.onFinish != nil {
		h.onFinish(count)
	}
	return err
}

func (h *LoggingHandler) SupportedExtensions() []string {
	return h.inner.SupportedExtensions()
}

func (h *LoggingHandler) MainInputFileRelativePath() string {
	return h.inner.MainInputFileRelativePath()
}

func (h *LoggingHandler) UnobservedFiles() []string {
	return h.inner.UnobservedFiles()
}

// TestWasmReloadRaceCondition_RealWorldScenario simulates the exact scenario
// described by the user:
// 1. First edit → compiles → reloads ✓
// 2. Second edit → sometimes compiles, sometimes just reloads ✗
// 3. Third edit → same inconsistent behavior ✗
func TestWasmReloadRaceCondition_RealWorldScenario(t *testing.T) {
	tempDir := t.TempDir()

	err := os.MkdirAll(tempDir+"/src/cmd/webclient", 0755)
	if err != nil {
		t.Fatal(err)
	}

	goModContent := "module example\n\ngo 1.25.2\n"
	if err := os.WriteFile(tempDir+"/go.mod", []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	mainGoFile := tempDir + "/src/cmd/webclient/main.go"
	mainGoContent := `package main
import "syscall/js"
func main() {
	dom := js.Global().Get("document").Call("createElement", "div")
	dom.Set("innerHTML", "Hello, WebAssembly! VERSION")
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
	compilationTimes := make([]time.Time, 0, 10)
	reloadTimes := make([]time.Time, 0, 10)
	var timesMutex sync.Mutex

	// Realistic WASM compilation time: 300ms
	baseHandler := &CountingCompilingHandler{
		compilationCount:     &compilationCount,
		compilationDuration:  300 * time.Millisecond,
		SupportedExtensions_: []string{".go"},
		MainInputFile:        "src/cmd/webclient/main.go",
	}

	// Wrapper to track times
	wasmHandler := &LoggingHandler{
		inner: baseHandler,
		onCompile: func(count int32) {
			timesMutex.Lock()
			now := time.Now()
			compilationTimes = append(compilationTimes, now)
			timesMutex.Unlock()

			if count == 1 {
				t.Logf("[t=0.000s] Compilation %d STARTED", count)
			} else {
				t.Logf("[t=%.3fs] Compilation %d STARTED", time.Since(compilationTimes[0]).Seconds(), count)
			}
		},
		onFinish: func(count int32) {
			if count == 1 {
				t.Logf("[t=0.300s] Compilation %d FINISHED", count)
			} else {
				timesMutex.Lock()
				startTime := compilationTimes[0]
				timesMutex.Unlock()
				t.Logf("[t=%.3fs] Compilation %d FINISHED", time.Since(startTime).Seconds(), count)
			}
		},
	}

	reloadCalled := make(chan struct{}, 10)

	config := &WatchConfig{
		AppRootDir:         tempDir,
		FilesEventHandlers: []FilesEventHandlers{wasmHandler},
		BrowserReload: func() error {
			count := atomic.AddInt64(&reloadCount, 1)
			now := time.Now()

			// Protect access to shared slices
			timesMutex.Lock()
			reloadTimes = append(reloadTimes, now) // Protected just in case, though usually single-threaded
			hasCompilations := len(compilationTimes) > 0
			var firstCompTime time.Time
			if hasCompilations {
				firstCompTime = compilationTimes[0]
			}
			timesMutex.Unlock()

			if hasCompilations {
				t.Logf("[t=%.3fs] Browser reload %d", time.Since(firstCompTime).Seconds(), count)
			}
			reloadCalled <- struct{}{}
			return nil
		},
		Logger:   func(message ...any) { /* Silent */ },
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
	time.Sleep(50 * time.Millisecond)

	// Simulate realistic user editing pattern
	t.Log("\n=== EDIT 1: Initial change ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	// Wait for compilation and reload
	select {
	case <-reloadCalled:
		t.Log("Edit 1: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("Edit 1: Timeout waiting for reload")
	}

	// User makes another edit after seeing the result (realistic timing: 2-3 seconds)
	time.Sleep(2 * time.Second)
	t.Log("\n=== EDIT 2: After 2 seconds ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("Edit 2: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("Edit 2: Timeout waiting for reload")
	}

	// Another edit after seeing result
	time.Sleep(2 * time.Second)
	t.Log("\n=== EDIT 3: After 2 more seconds ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	select {
	case <-reloadCalled:
		t.Log("Edit 3: ✓ Reloaded")
	case <-time.After(2 * time.Second):
		t.Fatal("Edit 3: Timeout waiting for reload")
	}

	// Quick successive edits (user rapidly makes changes)
	time.Sleep(500 * time.Millisecond)
	t.Log("\n=== EDIT 4: Quick edit ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	time.Sleep(200 * time.Millisecond) // Within debounce of previous edit END
	t.Log("\n=== EDIT 5: Very quick follow-up (200ms later) ===")
	watcher.Events <- fsnotify.Event{
		Name: mainGoFile,
		Op:   fsnotify.Write,
	}

	// Wait for pending operations
	time.Sleep(2 * time.Second)

	w.ExitChan <- true
	time.Sleep(100 * time.Millisecond)

	compilations := atomic.LoadInt32(&compilationCount)
	reloads := atomic.LoadInt64(&reloadCount)

	t.Log("\n=== FINAL RESULTS ===")
	t.Logf("Total edits: 5")
	t.Logf("Compilations: %d", compilations)
	t.Logf("Reloads: %d", reloads)

	// Verify each edit triggered a compilation
	if compilations != 5 {
		t.Errorf("❌ INCONSISTENT BEHAVIOR: Expected 5 compilations, got %d", compilations)
		t.Errorf("   This matches user's report: sometimes compiles, sometimes doesn't")

		// Analyze which edits were skipped
		for i := 0; i < 5; i++ {
			editNum := i + 1
			if int(compilations) < editNum {
				t.Errorf("   Edit %d was SKIPPED (no compilation)", editNum)
			}
		}
	} else {
		t.Logf("✅ CONSISTENT: All 5 edits triggered compilation")
	}

	if reloads != 5 {
		t.Errorf("❌ Reload count mismatch: expected 5, got %d", reloads)
	}
}

// TestWasmReloadRaceCondition_EditorWritePattern simulates how editors
// actually write files (multiple write events in quick succession)
func TestWasmReloadRaceCondition_EditorWritePattern(t *testing.T) {
	tempDir := t.TempDir()

	err := os.MkdirAll(tempDir+"/src/cmd/webclient", 0755)
	if err != nil {
		t.Fatal(err)
	}

	goModContent := "module example\n\ngo 1.25.2\n"
	if err := os.WriteFile(tempDir+"/go.mod", []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	mainGoFile := tempDir + "/src/cmd/webclient/main.go"
	mainGoContent := `package main
func main() { println("test") }
`
	if err := os.WriteFile(mainGoFile, []byte(mainGoContent), 0644); err != nil {
		t.Fatal(err)
	}

	var compilationCount int32
	var reloadCount int64

	wasmHandler := &CountingCompilingHandler{
		compilationCount:     &compilationCount,
		compilationDuration:  200 * time.Millisecond,
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
	time.Sleep(50 * time.Millisecond)

	// Simulate editor save pattern: multiple WRITE events in quick succession
	// This is how VSCode, Vim, etc. actually save files
	t.Log("=== Simulating editor save (multiple write events in 50ms) ===")
	for i := 0; i < 3; i++ {
		watcher.Events <- fsnotify.Event{
			Name: mainGoFile,
			Op:   fsnotify.Write,
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for compilation and reload
	select {
	case <-reloadCalled:
		t.Log("✓ Browser reloaded after editor save")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for reload")
	}

	time.Sleep(100 * time.Millisecond)

	// Another editor save after user makes more changes
	time.Sleep(1 * time.Second)
	t.Log("=== Second editor save ===")
	for i := 0; i < 3; i++ {
		watcher.Events <- fsnotify.Event{
			Name: mainGoFile,
			Op:   fsnotify.Write,
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-reloadCalled:
		t.Log("✓ Browser reloaded after second save")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for second reload")
	}

	time.Sleep(100 * time.Millisecond)

	w.ExitChan <- true
	time.Sleep(100 * time.Millisecond)

	compilations := atomic.LoadInt32(&compilationCount)
	reloads := atomic.LoadInt64(&reloadCount)

	t.Log("\n=== Results ===")
	t.Logf("Editor saves: 2 (each with 3 write events)")
	t.Logf("Compilations: %d (expected: 2, one per save)", compilations)
	t.Logf("Reloads: %d", reloads)

	// We expect 2 compilations (one per user save), even though there were 6 write events
	// The debounce should filter the rapid duplicate events from the same save
	if compilations < 2 {
		t.Errorf("❌ BUG: Only %d compilations for 2 saves (debounce too aggressive)", compilations)
	} else if compilations > 2 {
		t.Logf("⚠️  Warning: %d compilations for 2 saves (debounce not filtering duplicate events)", compilations)
	} else {
		t.Logf("✅ CORRECT: Exactly 2 compilations for 2 saves")
	}
}
