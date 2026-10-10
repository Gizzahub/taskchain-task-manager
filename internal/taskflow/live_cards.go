package taskflow

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LoadLiveCards reads every non-storage card. Unlike FindCards, a read
// failure fails the caller: state actions and promote refuse a board they
// could only see part of.
func LoadLiveCards(root string) ([]*Card, error) {
	if err := readableTasksDir(root); err != nil {
		return nil, err
	}
	tasksDir := tasksDirName()
	tasksPath := filepath.Join(root, tasksDir)
	var cards []*Card
	err := filepath.WalkDir(tasksPath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relToTasks, relErr := filepath.Rel(tasksPath, p)
		if relErr != nil {
			return relErr
		}
		name := d.Name()
		if d.IsDir() {
			if p == tasksPath {
				return nil
			}
			if name == ".ce" || name == "evidence" {
				return filepath.SkipDir
			}
			if IsStorageDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".md" {
			return nil
		}
		switch strings.ToLower(name) {
		case "readme.md", "index.md", "template.md":
			return nil
		}
		rel := filepath.ToSlash(relToTasks)
		card, readErr := ReadCardIn(root, tasksDir, rel)
		if readErr != nil {
			return fmt.Errorf("read card %s: %w", pathJoin(tasksDir, rel), readErr)
		}
		if card == nil {
			return fmt.Errorf("read card %s: empty", pathJoin(tasksDir, rel))
		}
		cards = append(cards, card)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return cards, nil
}
