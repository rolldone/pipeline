package executor

import (
	"fmt"
	"pipeline/internal/pipeline/types"
	"strings"
	"time"
)

// buildRsyncArgSlices builds rsync argument slices for given entries without executing rsync.
// This is a pure helper used for unit testing arg construction and validation.
func (e *Executor) buildRsyncArgSlices(step *types.Step, jobMode string, host string, entries []struct{ Source, Destination string }) ([][]string, error) {
	var all [][]string

	if step == nil {
		return nil, fmt.Errorf("step is nil")
	}

	base := []string{"-a"}

	compress := true
	if step.Compress != nil {
		compress = *step.Compress
	}
	if compress && jobMode != "local" {
		base = append(base, "-z")
	}

	if step.DryRun {
		base = append(base, "--dry-run")
	}
	if step.PruneEmptyDirs {
		base = append(base, "--prune-empty-dirs")
	}

	for _, inc := range step.Includes {
		base = append(base, "--include="+inc)
	}
	for _, exc := range step.Excludes {
		base = append(base, "--exclude="+exc)
	}

	deletePolicy := step.DeletePolicy
	if deletePolicy == "" {
		deletePolicy = "soft"
	}
	if deletePolicy != "soft" && deletePolicy != "force" {
		return nil, fmt.Errorf("invalid delete_policy '%s' in step %s", deletePolicy, step.Name)
	}
	if deletePolicy == "force" {
		if !step.ConfirmForce {
			return nil, fmt.Errorf("delete_policy=force requires confirm_force=true for step %s", step.Name)
		}
		base = append(base, "--delete")
		if step.DeleteExcluded {
			base = append(base, "--delete-excluded")
		}
	}

	for _, opt := range step.Options {
		low := strings.ToLower(opt)
		if deletePolicy == "soft" && (low == "--delete" || low == "--delete-excluded") {
			return nil, fmt.Errorf("unsafe option '%s' in step %s while delete_policy=soft", opt, step.Name)
		}
		base = append(base, opt)
	}

	// add a harmless timestamp option to ensure determinism in tests when needed
	_ = time.Now()

	if jobMode != "local" {
		base = append(base, "-e", "ssh -F .sync_temp/.ssh/config")
	}

	for _, ent := range entries {
		runArgs := append([]string{}, base...)
		var src, dst string
		direction := step.Direction
		if direction == "" {
			direction = "upload"
		}
		if direction == "upload" {
			src = ent.Source
			dst = fmt.Sprintf("%s:%s", host, ent.Destination)
		} else {
			src = fmt.Sprintf("%s:%s", host, ent.Source)
			dst = ent.Destination
		}
		runArgs = append(runArgs, src, dst)
		all = append(all, runArgs)
	}

	return all, nil
}
