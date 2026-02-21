package config

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// OnReload is called after a successful hot-reload. Consumers can register
// callbacks to react to config changes (e.g. updating log levels).
type OnReload func(old, new *Config)

// Watcher monitors the config file for changes and reloads automatically.
type Watcher struct {
	fsWatcher *fsnotify.Watcher
	filePath  string
	callbacks []OnReload
	mu        sync.Mutex
	done      chan struct{}
}

// Watch starts watching the given config file for changes. When the file is
// modified, the config is re-loaded, validated, and stored in the global
// atomic pointer. Any registered callbacks are invoked with the old and new
// config values.
//
// If filePath is empty, Watch attempts to locate the file using the same
// search order as Load (home dir then cwd).
func Watch(filePath string) (*Watcher, error) {
	if filePath == "" {
		return nil, fmt.Errorf("config watcher: file path must not be empty")
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("config watcher: resolving path: %w", err)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("config watcher: creating fsnotify watcher: %w", err)
	}

	// Watch the directory containing the config file rather than the file
	// itself. Many editors perform atomic saves (write tmp + rename) which
	// causes the inode to change; watching the directory catches renames.
	dir := filepath.Dir(absPath)
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("config watcher: watching directory %s: %w", dir, err)
	}

	w := &Watcher{
		fsWatcher: fsw,
		filePath:  absPath,
		done:      make(chan struct{}),
	}

	go w.loop()

	return w, nil
}

// OnChange registers a callback that will be invoked after each successful
// config reload. It is safe to call from multiple goroutines.
func (w *Watcher) OnChange(fn OnReload) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks = append(w.callbacks, fn)
}

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	close(w.done)
	return w.fsWatcher.Close()
}

// loop is the main event loop that processes fsnotify events.
func (w *Watcher) loop() {
	// Debounce: editors may fire multiple events in rapid succession for a
	// single save operation. We wait a short interval after the last event
	// before performing the reload.
	const debounce = 100 * time.Millisecond
	var timer *time.Timer

	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Only react to writes/creates/renames of our specific file.
			if filepath.Clean(event.Name) != w.filePath {
				continue
			}

			isWrite := event.Op&fsnotify.Write != 0
			isCreate := event.Op&fsnotify.Create != 0
			isRename := event.Op&fsnotify.Rename != 0

			if !isWrite && !isCreate && !isRename {
				continue
			}

			// Reset the debounce timer.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				w.reload()
			})

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("[config watcher] error: %v", err)
		}
	}
}

// reload performs the actual config reload and notifies callbacks.
func (w *Watcher) reload() {
	old := Get()

	newCfg, err := Load(w.filePath)
	if err != nil {
		log.Printf("[config watcher] reload failed: %v (keeping previous config)", err)
		return
	}

	log.Printf("[config watcher] config reloaded from %s", w.filePath)

	w.mu.Lock()
	cbs := make([]OnReload, len(w.callbacks))
	copy(cbs, w.callbacks)
	w.mu.Unlock()

	for _, cb := range cbs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[config watcher] callback panicked: %v", r)
				}
			}()
			cb(old, newCfg)
		}()
	}
}
