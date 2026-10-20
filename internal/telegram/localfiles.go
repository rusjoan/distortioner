package telegram

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"time"
)

// mediaDirs are the subdirectories where a self-hosted Bot API server in
// --local mode keeps downloaded files, one set per bot. Everything else in
// a bot's directory (td.binlog, databases) is server state and is kept.
var mediaDirs = map[string]bool{
	"animations": true, "documents": true, "music": true, "photos": true,
	"stickers": true, "temp": true, "thumbnails": true, "video_notes": true,
	"videos": true, "voice": true,
}

// SweepLocalFiles deletes media files older than ttl from the working
// directory of a self-hosted Bot API server, which never deletes them on
// its own. Layout: dir/<bot token>/<media dir>/…
//
// All access goes through os.Root, so symlinks inside the volume cannot
// make it delete anything outside dir.
func SweepLocalFiles(dir string, ttl time.Duration, now time.Time) (deleted int, freed int64, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, 0, err
	}
	defer root.Close()
	fsys := root.FS()

	bots, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return 0, 0, err
	}
	cutoff := now.Add(-ttl)
	var errs []error
	for _, bot := range bots {
		if !bot.IsDir() {
			continue
		}
		dirs, err := fs.ReadDir(fsys, bot.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() || !mediaDirs[d.Name()] {
				continue
			}
			walkErr := fs.WalkDir(fsys, path.Join(bot.Name(), d.Name()), func(p string, e fs.DirEntry, err error) error {
				if err != nil {
					errs = append(errs, err)
					return nil
				}
				if !e.Type().IsRegular() {
					return nil
				}
				info, err := e.Info()
				if err != nil || !info.ModTime().Before(cutoff) {
					return nil //nolint:nilerr // vanished in the meantime or still fresh
				}
				if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					errs = append(errs, err)
					return nil
				}
				deleted++
				freed += info.Size()
				return nil
			})
			if walkErr != nil {
				errs = append(errs, walkErr)
			}
		}
	}
	return deleted, freed, errors.Join(errs...)
}
