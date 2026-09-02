// Package reload implements hot-reloading of the marketplace-api in-memory
// index from dist/.
//
// Two triggers are supported:
//
//  1. Explicit — the caller (typically main.go on SIGHUP) calls Reload.
//  2. Polling — MaybeReload compares the on-disk fingerprint
//     (mtime, size) of dist/index.json against the last successful
//     reload and triggers Reload when either changes.
//
// Reload is safe to call concurrently from multiple goroutines. It is
// also safe to call on a State whose initial Index is nil — a successful
// Reload publishes the first Index; a failed Reload leaves whatever was
// there before (or nil) in place.
//
// On failure (parse error, validation error, transient I/O error) Reload
// returns a wrapped error and the previously-published Index is retained.
// Callers should log at WARN level and continue serving — stale-but-
// serving is a valid operational state. The only reload path that should
// crash is the startup-time initial load.
package reload

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/VMware-AI/agent-marketplace-packages/internal/server"
)

// Reload reads dist/index.json from disk, validates the resulting index
// against the dist/ directory (tarballs present + sha256 matches), and
// atomically publishes the new *apitypes.Index via State.SetIndex.
//
// On any error the previously-published Index is retained. The returned
// error is wrapped with operation context for logging.
//
// Reload does not consult State.Index() — it does not need to, because
// SetIndex atomically replaces whatever is there.
func Reload(s *server.State, dist *repo.Dir, logger *slog.Logger) error {
	idx, err := dist.LoadIndex()
	if err != nil {
		return fmt.Errorf("load index.json: %w", err)
	}
	if err := dist.Validate(idx); err != nil {
		return fmt.Errorf("validate dist/: %w", err)
	}
	s.SetIndex(idx)
	if logger != nil {
		logger.Info("index reloaded",
			"agents", len(idx.Agents),
			"dist_dir", dist.Path,
		)
	}
	return nil
}

// fingerprint captures the (mtime, size) pair used to detect when
// dist/index.json has changed on disk. Tracking both guards against the
// edge case where mtime goes backwards (manual touch, backup restore)
// but size legitimately changes — and against same-mtime reindexes that
// rewrite a same-sized file (e.g. updating only generated_at).
type Fingerprint struct {
	mtime time.Time
	size  int64
}

// MaybeReload stats dist/index.json and, if the fingerprint differs from
// the last successful reload, calls Reload. The fingerprint pointer is
// updated in place only on successful reload so a failed attempt does
// not "advance" the watch state and cause a tight retry loop.
//
// A Stat error is treated as a transient condition (file briefly deleted
// during a reindex, bind-mount hiccup, etc.) and returns nil — the next
// tick will retry. This matches the "stale-but-serving is acceptable"
// posture: the in-memory Index keeps serving until either the file
// comes back or the operator reloads explicitly.
func MaybeReload(s *server.State, dist *repo.Dir, fp *Fingerprint, logger *slog.Logger) error {
	path := filepath.Join(dist.Path, "index.json")
	fi, err := os.Stat(path)
	if err != nil {
		if logger != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("reload: stat index.json failed, keeping old index",
				"err", err, "path", path)
		}
		return nil
	}
	cur := Fingerprint{mtime: fi.ModTime(), size: fi.Size()}
	if fp != nil && cur == *fp {
		return nil
	}
	if err := Reload(s, dist, logger); err != nil {
		if logger != nil {
			logger.Warn("reload failed, keeping old index", "err", err)
		}
		return err
	}
	if fp != nil {
		*fp = cur
	}
	return nil
}

// InitialFingerprint captures the (mtime, size) of dist/index.json at
// startup, BEFORE the initial LoadIndex. This way the very first tick of
// the poll loop won't spuriously re-trigger when nothing has changed —
// and if a reindex lands between this call and LoadIndex, the initial
// load will simply pick up the new file (which is the desired behavior).
//
// Returns (Fingerprint, true) on success, or (zero, false) if the file
// is missing or unreadable — in which case MaybeReload will keep
// retrying on each tick.
func InitialFingerprint(dist *repo.Dir) (Fingerprint, bool) {
	fi, err := os.Stat(filepath.Join(dist.Path, "index.json"))
	if err != nil {
		return Fingerprint{}, false
	}
	return Fingerprint{mtime: fi.ModTime(), size: fi.Size()}, true
}
