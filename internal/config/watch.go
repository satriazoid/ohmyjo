package config

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch starts a filesystem watcher on the config file so edits made in a text
// editor apply without restarting. It returns a stop function.
//
// Editors do not write in place: they create a temporary file and rename it, or
// replace the target outright, which drops the watch on the old inode. The
// watcher therefore watches the containing directory and filters by name.
func (l *Loader) Watch() (func(), error) {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return nil, err
	}

	stop := make(chan struct{})
	var once sync.Once
	go func() {
		// Rename bursts (write tmp, rename) arrive as several events; coalesce
		// so the UI receives one update per save, not three.
		var timer *time.Timer
		debounce := make(chan struct{}, 1)
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if !isConfigEvent(ev.Name, l.path) {
					continue
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(150*time.Millisecond, func() {
					select {
					case debounce <- struct{}{}:
					default:
					}
				})
			case <-debounce:
				if diags := l.Reload(); len(diags) > 0 {
					for _, d := range diags {
						log.Printf("config: %s", d)
					}
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("config watch: %v", err)
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
			_ = w.Close()
		})
	}, nil
}

// isConfigEvent reports whether a watched path name refers to our config file.
// Both the file itself and the atomic-save temp name qualify.
func isConfigEvent(name, path string) bool {
	base := filepath.Base(path)
	n := filepath.Base(name)
	return n == base || n == base+".tmp"
}
