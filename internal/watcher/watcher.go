package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/avnis/kb-system/internal/compiler"
	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/fsnotify/fsnotify"
)

type fileState int

const (
	stateNone fileState = iota
	stateDebouncing
	stateCompiling
)

type Watcher struct {
	cfg         *config.Config
	compiler    *compiler.Compiler
	logger      *slog.Logger
	poll        bool
	states      map[string]fileState
	statesMutex sync.Mutex
}

func New(cfg *config.Config, compiler *compiler.Compiler, logger *slog.Logger, poll bool) *Watcher {
	return &Watcher{
		cfg:      cfg,
		compiler: compiler,
		logger:   logger,
		poll:     poll,
		states:   make(map[string]fileState),
	}
}

func (w *Watcher) Start(ctx context.Context) error {
	w.checkUncompiledCount()

	eventChan := make(chan string, 100)
	processChan := make(chan string, 100)

	// Start worker goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case path := <-processChan:
				func() {
					defer func() {
						w.statesMutex.Lock()
						delete(w.states, path)
						w.statesMutex.Unlock()
					}()

					content, err := os.ReadFile(path)
					if err != nil {
						return
					}
					sf, _, err := frontmatter.ParseSource(content)
					if err != nil || (sf.Status != "uncompiled" && sf.Status != "") {
						return
					}

					w.logger.Info("Compiling file from watch queue", "file", filepath.Base(path))
					err = w.compiler.CompileSingle(path, false, false)
					if err != nil {
						w.logger.Error("Watcher compilation failed", "file", filepath.Base(path), "error", err)
					} else {
						w.logger.Info("Watcher compilation succeeded", "file", filepath.Base(path))
					}
				}()
				w.checkUncompiledCount()
			}
		}
	}()

	// Start debounce goroutine
	go func() {
		pending := make(map[string]time.Time)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case path := <-eventChan:
				pending[path] = time.Now().Add(2 * time.Second)
			case <-ticker.C:
				now := time.Now()
				for path, deadline := range pending {
					if now.After(deadline) {
						delete(pending, path)
						w.moveToCompiling(path, processChan)
					}
				}
			}
		}
	}()

	if w.poll {
		w.logger.Info("Watcher running in Polling mode", "interval", "2s")
		return w.runPollingLoop(ctx, eventChan)
	}

	// Try starting fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.logger.Warn("Failed to start fsnotify watcher, falling back to Polling mode", "error", err)
		w.poll = true
		w.logger.Info("Watcher running in Polling mode", "interval", "2s")
		return w.runPollingLoop(ctx, eventChan)
	}
	defer watcher.Close()

	rawDir := vault.RawDir(w.cfg)
	err = watcher.Add(rawDir)
	if err != nil {
		w.logger.Warn("Failed to add raw directory to fsnotify watcher, falling back to Polling mode", "dir", rawDir, "error", err)
		w.poll = true
		w.logger.Info("Watcher running in Polling mode", "interval", "2s")
		return w.runPollingLoop(ctx, eventChan)
	}

	w.logger.Info("Watching raw directory using fsnotify", "dir", rawDir)

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) && filepath.Ext(event.Name) == ".md" {
				w.logger.Debug("fsnotify event detected", "op", event.Op.String(), "path", event.Name)
				w.handleDetection(event.Name, eventChan, true)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			w.logger.Error("fsnotify error", "error", err)
		}
	}
}

func (w *Watcher) runPollingLoop(ctx context.Context, eventChan chan<- string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	rawDir := vault.RawDir(w.cfg)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			entries, err := os.ReadDir(rawDir)
			if err != nil {
				w.logger.Error("Failed to read raw directory in polling loop", "error", err)
				continue
			}

			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
					continue
				}

				path := filepath.Join(rawDir, entry.Name())

				w.statesMutex.Lock()
				state := w.states[path]
				w.statesMutex.Unlock()

				if state == stateNone {
					content, err := os.ReadFile(path)
					if err == nil {
						sf, _, err := frontmatter.ParseSource(content)
						if err == nil && (sf.Status == "uncompiled" || sf.Status == "") {
							w.logger.Debug("Polling detected uncompiled file", "file", entry.Name())
							w.handleDetection(path, eventChan, false)
						}
					}
				}
			}
		}
	}
}

func (w *Watcher) handleDetection(path string, eventChan chan<- string, isFSNotify bool) {
	w.statesMutex.Lock()
	state := w.states[path]

	if state == stateNone {
		w.states[path] = stateDebouncing
		w.statesMutex.Unlock()
		eventChan <- path
		return
	}

	if state == stateDebouncing && isFSNotify {
		w.statesMutex.Unlock()
		eventChan <- path
		return
	}

	w.statesMutex.Unlock()
}

func (w *Watcher) moveToCompiling(path string, processChan chan<- string) {
	w.statesMutex.Lock()
	defer w.statesMutex.Unlock()

	if w.states[path] != stateDebouncing {
		return
	}
	w.states[path] = stateCompiling
	processChan <- path
}

func (w *Watcher) checkUncompiledCount() {
	rawDir := vault.RawDir(w.cfg)
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		w.logger.Error("failed to read raw directory to check uncompiled count", "error", err)
		return
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		filePath := filepath.Join(rawDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		sf, _, err := frontmatter.ParseSource(content)
		if err == nil && (sf.Status == "uncompiled" || sf.Status == "") {
			count++
		}
	}

	if count >= w.cfg.MultiDocThreshold {
		w.logger.Warn("Count of uncompiled documents has reached or exceeded MULTI_DOC_THRESHOLD. Consider running with multi-doc synthesis mode.", "count", count, "threshold", w.cfg.MultiDocThreshold)
	}
}
