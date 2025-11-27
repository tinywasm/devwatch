package devwatch

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// fileEventKey stores both time and content hash for smarter debouncing
type fileEventKey struct {
	lastTime time.Time
	lastHash [32]byte
}

func (h *DevWatch) watchEvents() {
	// Track last event with content hash for smart debouncing
	// This allows rapid edits while filtering duplicate OS events
	lastEventInfo := make(map[string]fileEventKey)
	const debounceWindow = 50 * time.Millisecond // Reduced for faster response

	// create a stopped reload timer and a single goroutine that will handle its firing.
	h.reloadMutex.Lock()
	if h.reloadTimer == nil {
		h.reloadTimer = time.NewTimer(0)
		h.reloadTimer.Stop()
		// goroutine to wait on timer events and invoke reload
		go func(t *time.Timer) {
			for {
				<-t.C
				h.triggerBrowserReload()
			}
		}(h.reloadTimer)
	}
	h.reloadMutex.Unlock()

	for {
		select {

		case event, ok := <-h.watcher.Events:
			if !ok {
				h.Logger("Error h.watcher.Events")
				return
			}

			// create, write, rename, remove
			eventType := strings.ToLower(event.Op.String())
			isDeleteEvent := eventType == "remove" || eventType == "delete"

			// For non-delete events, check if file exists and is not contained
			var info os.FileInfo
			if !isDeleteEvent {
				var statErr error
				info, statErr = os.Stat(event.Name)
				if statErr != nil || h.Contain(event.Name) {
					continue // Skip if file doesn't exist or is already contained
				}
			}

			// Get fileName once and reuse for all operations
			fileName, err := GetFileName(event.Name)
			if err != nil {
				continue // Skip if we can't get the filename
			}

			// Handle directory changes for architecture detection (only for non-delete events)
			if !isDeleteEvent && info.IsDir() {
				h.handleDirectoryEvent(fileName, event.Name, eventType)
				continue
			}

			// SMART DEBOUNCE: Filter duplicate OS events but allow rapid user edits
			// Strategy: Compare both time AND file content hash
			now := time.Now()
			shouldProcess := true

			if lastInfo, exists := lastEventInfo[event.Name]; exists {
				timeSinceLastEvent := now.Sub(lastInfo.lastTime)

				// If event is very recent (< 50ms), check if content changed
				if timeSinceLastEvent <= debounceWindow {
					// Calculate current file hash
					currentHash := h.calculateFileHash(event.Name)

					// Only skip if BOTH time is recent AND content is identical
					// This filters duplicate OS events but allows rapid real edits
					if currentHash == lastInfo.lastHash {
						// Same content, same file, within debounce window = duplicate event
						shouldProcess = false
					}
					// If hash is different, it's a real edit - process it!
				}
			}

			if !shouldProcess {
				continue // Skip duplicate event
			}

			// Handle file events (both delete and non-delete)
			// NOTE: This call blocks during compilation! Events arriving during
			// compilation will queue up in the watcher.Events channel.
			h.handleFileEvent(fileName, event.Name, eventType, isDeleteEvent)

			// Record event with content hash AFTER processing
			// This ensures the hash reflects the file state after compilation/processing
			// FIX: Previously this was done BEFORE handleFileEvent, causing rapid edits
			// to be incorrectly detected as duplicates because the hash was captured
			// before the file was actually modified by the compilation process.
			lastEventInfo[event.Name] = fileEventKey{
				lastTime: now,
				lastHash: h.calculateFileHash(event.Name),
			}

		case err, ok := <-h.watcher.Errors:
			if !ok {
				h.Logger("h.watcher.Errors:", err)
				return
			}

		case <-h.ExitChan:
			h.watcher.Close()
			h.stopReload()
			return
		}
	}
}

// handleDirectoryEvent processes directory creation/modification events
func (h *DevWatch) handleDirectoryEvent(fileName, eventName, eventType string) {
	if h.FolderEvents != nil {
		err := h.FolderEvents.NewFolderEvent(fileName, eventName, eventType)
		if err != nil {
			h.Logger("Watch folder event error:", err)
		}
	}

	// Add new directory to watcher
	if eventType == "create" {
		// Create a registry map for the new directory walk
		reg := make(map[string]struct{})

		// Add the main directory first
		if err := h.addDirectoryToWatcher(eventName, reg); err == nil {
			// Walk recursively to add any subdirectories that might have been created
			// This handles cases like os.MkdirAll() where multiple directories are created at once
			err := filepath.Walk(eventName, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // Continue walking even if there's an error
				}
				if info.IsDir() && path != eventName && !h.Contain(path) {
					h.addDirectoryToWatcher(path, reg)
				}
				return nil
			})
			if err != nil {
				h.Logger("Watch: Error walking new directory:", eventName, err)
			}
		}
	}
}

// handleFileEvent processes file creation/modification/deletion events
func (h *DevWatch) handleFileEvent(fileName, eventName, eventType string, isDeleteEvent bool) {
	extension := filepath.Ext(eventName)
	var processedSuccessfully bool
	isGoFileEvent := extension == ".go"
	var atLeastOneGoHandlerSucceeded bool

	// Execute ALL handlers, don't stop on errors
	for _, handler := range h.FilesEventHandlers {
		if !slices.Contains(handler.SupportedExtensions(), extension) {
			continue
		}

		// At least one handler supports this extension.
		var isMine = true
		var herr error

		if !isDeleteEvent && extension == ".go" {
			isMine, herr = h.depFinder.ThisFileIsMine(handler.MainInputFileRelativePath(), eventName, eventType)
			if herr != nil {
				// h.Logger("DEBUG Error from ThisFileIsMine, continuing: %v\n", herr)
				continue
			}
		}

		if isMine {
			err := handler.NewFileEvent(fileName, extension, eventName, eventType)
			if err != nil {
				//h.Logger("DEBUG Watch updating file error:", err)
				// Continue to next handler even if this one failed
			} else {
				// Track success for both Go and non-Go files
				processedSuccessfully = true
				if isGoFileEvent {
					atLeastOneGoHandlerSucceeded = true
				}
			}
		}
	}

	// Schedule reload if AT LEAST ONE handler succeeded
	// For Go files: reload if any handler succeeded
	// For non-Go files: reload if any handler succeeded
	if (isGoFileEvent && atLeastOneGoHandlerSucceeded) || (!isGoFileEvent && processedSuccessfully) {
		h.scheduleReload()
	}
}

// triggerBrowserReload safely triggers a browser reload in a goroutine
func (h *DevWatch) triggerBrowserReload() {
	if h.BrowserReload != nil {
		// Call synchronously so the caller (watchEvents) completes the
		// reload action before returning. This prevents background reload
		// goroutines from racing with test teardown and shared counters.
		_ = h.BrowserReload()
	}
}

// scheduleReload resets or starts a reload timer which will call triggerBrowserReload
// after a short debounce period. This mirrors the original implementation's
// behavior of resetting the timer on each new event so only the last one triggers reload.
func (h *DevWatch) scheduleReload() {
	const wait = 50 * time.Millisecond

	h.reloadMutex.Lock()
	defer h.reloadMutex.Unlock()

	if h.reloadTimer == nil {
		h.reloadTimer = time.NewTimer(wait)
		return
	}

	// Stop existing timer and reset
	if !h.reloadTimer.Stop() {
		select {
		case <-h.reloadTimer.C:
		default:
		}
	}
	h.reloadTimer.Reset(wait)
}

// stopReload stops and clears the reload timer; used during shutdown
func (h *DevWatch) stopReload() {
	h.reloadMutex.Lock()
	defer h.reloadMutex.Unlock()
	if h.reloadTimer != nil {
		// Only trigger reload if timer was actually programmed (not stopped)
		// Check if there's a pending reload by trying to stop the timer
		if !h.reloadTimer.Stop() {
			// Timer already fired or was never started, check channel
			select {
			case <-h.reloadTimer.C:
				// Timer fired but reload not yet called, trigger it now
				h.reloadMutex.Unlock() // Unlock before calling reload to avoid deadlock
				h.triggerBrowserReload()
				h.reloadMutex.Lock() // Re-lock before returning
			default:
				// Timer was stopped or never programmed, don't reload
			}
		}
		// If Stop() returned true, timer was active and is now stopped
		// Don't trigger reload in this case
	}
}

// calculateFileHash computes SHA256 hash of file content for smart debouncing
// Returns zero hash if file cannot be read (will be treated as different)
func (h *DevWatch) calculateFileHash(filePath string) [32]byte {
	var zeroHash [32]byte

	file, err := os.Open(filePath)
	if err != nil {
		return zeroHash // File doesn't exist or can't be read
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return zeroHash // Error reading file
	}

	var hash [32]byte
	copy(hash[:], hasher.Sum(nil))
	return hash
}
