package job

import (
	"context"
	"errors"

	"github.com/wadejbeckett/relical/core/internal/inventory"
)

// FileExcludes turns the subscription's file_excludes, which are relative
// to each backed-up path, into restic --exclude patterns anchored to that
// path (the path glob-escaped, so a literal * or [ in it matches itself).
func FileExcludes(sub inventory.Subscription) []string {
	excludes := []string{}
	for _, p := range sub.Files {
		ep := GlobEscape(p)
		for _, x := range sub.FileExcludes {
			excludes = append(excludes, ep+"/"+x)
		}
	}
	return excludes
}

// BackupFiles backs up the subscription's files paths into one snapshot
// tagged files. It stays on each path's file system and skips directories
// marked with a CACHEDIR.TAG.
func (e Env) BackupFiles(ctx context.Context, sub inventory.Subscription) Result {
	res := e.result(sub.Name, FilesTag, "backup")
	if !sub.Has(FilesTag) {
		res.OK = true
		res.Note = "files is not among the subscription's features; nothing to back up"
		return res
	}
	if len(sub.Files) == 0 {
		return failed(res, errors.New("the subscription has no files paths"))
	}
	r, err := e.openRepo(ctx, sub)
	if err != nil {
		return failed(res, err)
	}
	sum, err := r.Backup(ctx, sub.Files, []string{FilesTag}, FileExcludes(sub), "--exclude-caches")
	return saved(res, sum, err)
}
