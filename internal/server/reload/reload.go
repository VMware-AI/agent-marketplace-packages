// Package reload implements hot-reloading of the marketplace-api in-memory
// index from dist/.
//
// Two triggers are supported:
//
//  1. Explicit — the caller (typically main.go on SIGHUP) calls Reload.
//  2. Polling — MaybeReload compares the on-disk fingerprint
//     (mtime, size) of dist/index.json + dist/skills-index.json against
//     the last successful reload and triggers the corresponding Reload
//     when either changes.
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
	"sync"
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
//
// Reload also reloads the skills index (dist/skills-index.json) but does
// not fail the call if skills loading errors out — that would be a
// regression for an existing agents-only deployment that happens to have
// a broken skills file (which the operator would want to fix separately).
// Skills reload errors are logged at WARN.
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
	// Skills index reload is best-effort: failures here don't fail the
	// overall Reload call (the agent index is the primary contract).
	reloadSkills(s, dist, logger)
	return nil
}

// reloadSkills reloads dist/skills-index.json + validates dist/skills/,
// then atomically publishes via State.SetSkillsIndex. Errors are logged
// at WARN (not returned) so a broken skills index doesn't take down the
// agents side. Previously-published skills index is retained on error.
//
// Called both from Reload (full reload) and from MaybeReload when only
// the skills fingerprint changed.
func reloadSkills(s *server.State, dist *repo.Dir, logger *slog.Logger) {
	idx, err := dist.LoadSkillsIndex()
	if err != nil {
		if logger != nil {
			logger.Warn("reload skills index: load failed, keeping old", "err", err)
		}
		return
	}
	if err := dist.ValidateSkills(idx); err != nil {
		if logger != nil {
			logger.Warn("reload skills index: validate failed, keeping old", "err", err)
		}
		return
	}
	s.SetSkillsIndex(idx)
	if logger != nil {
		logger.Info("skills index reloaded",
			"skills", len(idx.Skills),
			"dist_dir", dist.Path,
		)
	}
}

// Fingerprint captures the (mtime, size) pair used to detect when a file
// has changed on disk. Tracking both guards against the edge case where
// mtime goes backwards (manual touch, backup restore) but size
// legitimately changes — and against same-mtime reindexes that rewrite a
// same-sized file (e.g. updating only generated_at).
type Fingerprint struct {
	mtime time.Time
	size  int64
}

// Equal reports whether two Fingerprints are identical (used by MaybeReload
// to short-circuit the no-change case).
func (f Fingerprint) Equal(other Fingerprint) bool {
	return f == other
}

// Fingerprints is the parallel of Fingerprint for the two-index layout:
// one slot for dist/index.json (agents) and one for dist/skills-index.json
// (skills). Either or both may change on a given tick — MaybeReload fans
// out to the corresponding Reload paths.
type Fingerprints struct {
	Agents Fingerprint
	Skills Fingerprint
}

// MaybeReload stats both index files and, if either fingerprint differs
// from the last successful reload, calls the matching Reload. The
// fingerprint pointer is updated in place only on successful reload so a
// failed attempt does not "advance" the watch state and cause a tight
// retry loop.
//
// A Stat error on EITHER file is treated as a transient condition
// (file briefly deleted during a reindex, bind-mount hiccup, etc.) and
// returns nil — the next tick will retry. This matches the "stale-but-
// serving is acceptable" posture: the in-memory Index keeps serving until
// either the file comes back or the operator reloads explicitly.
//
// To keep the original contract (a single Dist, a single State, no shared
// lock between agents and skills reloads), we serialize on a package-level
// mutex. Without this, two concurrent goroutines could each call Reload
// and race on the same state pointers; in practice we expect this is the
// only writer, but a defensive lock keeps -race clean if any future
// caller fans out.
var reloadMu sync.Mutex

func MaybeReload(s *server.State, dist *repo.Dir, fp *Fingerprints, logger *slog.Logger) error {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	agentsChanged, agentsFp, agentsErr := statFingerprint(filepath.Join(dist.Path, "index.json"))
	if agentsErr != nil && !errors.Is(agentsErr, os.ErrNotExist) {
		if logger != nil {
			logger.Warn("reload: stat index.json failed, keeping old index",
				"err", agentsErr)
		}
		// Don't proceed with a half-stat'd state — next tick will retry.
		agentsChanged = false
	}
	skillsChanged, skillsFp, skillsErr := statFingerprint(filepath.Join(dist.Path, "skills-index.json"))
	if skillsErr != nil && !errors.Is(skillsErr, os.ErrNotExist) {
		if logger != nil {
			logger.Warn("reload: stat skills-index.json failed, keeping old skills index",
				"err", skillsErr)
		}
		skillsChanged = false
	}

	// Determine what changed relative to the last-known fingerprints.
	// If fp is nil we treat both as "changed" (cold start with no
	// recorded prior state).
	agentsShouldReload := fp == nil ||
		(agentsChanged && (fp.Agents.mtime != agentsFp.mtime || fp.Agents.size != agentsFp.size))
	skillsShouldReload := fp == nil ||
		(skillsChanged && (fp.Skills.mtime != skillsFp.mtime || fp.Skills.size != skillsFp.size))

	if !agentsShouldReload && !skillsShouldReload {
		return nil
	}

	if agentsShouldReload {
		// Re-load agents only. Pass nil logger on error paths so we don't
		// double-log when Reload itself already logged via Reload().
		if err := Reload(s, dist, logger); err != nil {
			if logger != nil {
				logger.Warn("reload failed, keeping old agents index", "err", err)
			}
			// Don't advance fp — keep watching for the real change.
			return err
		}
	} else if skillsShouldReload {
		// Only the skills side changed — reload just skills. Reload()
		// also reloads skills, so call it directly: it'll reload agents
		// too but that's a cheap read and we don't want to fork the
		// loading logic.
		if err := Reload(s, dist, logger); err != nil {
			if logger != nil {
				logger.Warn("reload failed, keeping old agents index", "err", err)
			}
			return err
		}
	}

	if fp != nil {
		if agentsChanged {
			fp.Agents = agentsFp
		}
		if skillsChanged {
			fp.Skills = skillsFp
		}
	}
	return nil
}

// statFingerprint returns (file-exists, fingerprint, error). ENOENT is
// reported as (false, zero, nil) so callers can treat a missing file as
// "no change" rather than as a fatal error.
func statFingerprint(path string) (bool, Fingerprint, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, Fingerprint{}, nil
		}
		return false, Fingerprint{}, err
	}
	return true, Fingerprint{mtime: fi.ModTime(), size: fi.Size()}, nil
}

// InitialFingerprints captures the (mtime, size) of both index files at
// startup, BEFORE the initial Reload. This way the very first tick of
// the poll loop won't spuriously re-trigger when nothing has changed —
// and if a reindex lands between this call and Reload, the initial load
// will simply pick up the new file (which is the desired behavior).
//
// Either fingerprint may be the zero value if the corresponding file is
// missing — that's fine, MaybeReload handles ENOENT as "no change".
func InitialFingerprints(dist *repo.Dir) Fingerprints {
	fp := Fingerprints{}
	if fi, err := os.Stat(filepath.Join(dist.Path, "index.json")); err == nil {
		fp.Agents = Fingerprint{mtime: fi.ModTime(), size: fi.Size()}
	}
	if fi, err := os.Stat(filepath.Join(dist.Path, "skills-index.json")); err == nil {
		fp.Skills = Fingerprint{mtime: fi.ModTime(), size: fi.Size()}
	}
	return fp
}
