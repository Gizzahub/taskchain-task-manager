package taskflow

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// LinkRewrite records one rewritten link destination and the line it sat on.
type LinkRewrite struct {
	Line int
	From string
	To   string
}

// InboundDoc collects the lines of one document that pointed at the card.
type InboundDoc struct {
	Path  string
	Lines []int
}

// LinkUpdate reports what reconciling a card's links did. Skipped holds the
// first document that could not be reconciled, and when it is set nothing else
// is reported: the caller sees the refusal, not a partial list.
type LinkUpdate struct {
	Outbound []LinkRewrite
	Inbound  []InboundDoc
	Skipped  string
}

// MoveCard relocates one card into dest and reconciles the links both ways.
// It returns the destination path relative to the tasks directory, whether a
// **Status** body cell was rewritten, and the link update.
func MoveCard(root, cardPath string, dest MoveDestination) (newRel string, synced bool, links LinkUpdate, err error) {
	srcRel, ok := relUnderTasks(cardPath)
	if !ok {
		return "", false, links, fmt.Errorf("%q is not under %s/", cardPath, TasksDir)
	}
	parts := strings.Split(srcRel, "/")
	zoneIdx, _, inZone := ZoneSegment(parts)
	if !inZone {
		return "", false, links, fmt.Errorf("task is not in a zone (todo, doing, review, blocked, done): %s", srcRel)
	}

	if parts[zoneIdx] == dest.Zone {
		// Already in the target zone: sync the cell and the frontmatter status.
		// A card with no Status cell still has to stop claiming the zone it left.
		full := filepath.Join(root, TasksDir, filepath.FromSlash(srcRel))
		synced, err = rewriteStatusCell(full, dest.Status)
		if err != nil {
			return srcRel, synced, links, err
		}
		if _, err := stampFrontmatterField(full, "status", movedStatusWord(dest)); err != nil {
			return srcRel, synced, links, err
		}
		return srcRel, synced, links, nil
	}

	destParts := append([]string{}, parts...)
	destParts[zoneIdx] = dest.Zone
	destRel := strings.Join(destParts, "/")
	srcFull := filepath.Join(root, TasksDir, filepath.FromSlash(srcRel))
	dstFull := filepath.Join(root, TasksDir, filepath.FromSlash(destRel))

	if _, err := os.Stat(dstFull); err == nil {
		return "", false, links, fmt.Errorf("destination already exists: %s", destRel)
	}
	if err := os.MkdirAll(filepath.Dir(dstFull), 0o755); err != nil {
		return "", false, links, fmt.Errorf("create destination directory: %w", err)
	}
	if err := moveFile(root, srcFull, dstFull); err != nil {
		return "", false, links, err
	}

	synced, err = rewriteStatusCell(dstFull, dest.Status)
	if err != nil {
		return destRel, false, links, err
	}
	if _, err := stampFrontmatterField(dstFull, "status", movedStatusWord(dest)); err != nil {
		return destRel, synced, links, err
	}

	links = reconcileCardLinks(root, srcRel, destRel)
	return destRel, synced, links, nil
}

// ArchiveCard files one card into storage, keeping the zone it sat in, and
// reconciles inbound links. It refuses cards outside tasks/ and cards already
// under storage, and refuses a destination that already exists.
func ArchiveCard(root, cardPath string) (newRel string, links LinkUpdate, err error) {
	srcRel, ok := relUnderTasks(cardPath)
	if !ok {
		return "", links, fmt.Errorf("%q is not under %s/", cardPath, TasksDir)
	}
	parts := strings.Split(srcRel, "/")
	destRel := StorageWriteDir
	if len(parts) > 1 {
		for i, name := range parts[:len(parts)-1] {
			if IsStorageDir(name) {
				return "", links, fmt.Errorf("refusing to archive %s: it is already under tasks/%s",
					srcRel, strings.Join(parts[:i+1], "/"))
			}
		}
		destRel = path.Join(archiveDirParts(parts)...)
	}
	if err := os.MkdirAll(filepath.Join(root, TasksDir, filepath.FromSlash(destRel)), 0o755); err != nil {
		return "", links, fmt.Errorf("create archive directory: %w", err)
	}
	destRel = path.Join(destRel, path.Base(srcRel))
	dstFull := filepath.Join(root, TasksDir, filepath.FromSlash(destRel))
	if _, err := os.Stat(dstFull); err == nil {
		return "", links, fmt.Errorf("already archived: %s", dstFull)
	}

	srcFull := filepath.Join(root, TasksDir, filepath.FromSlash(srcRel))
	if err := stampArchivedStatus(srcFull); err != nil {
		return "", links, err
	}
	if err := moveFile(root, srcFull, dstFull); err != nil {
		return "", links, err
	}

	links = reconcileArchiveLinks(root, srcRel, destRel)
	return destRel, links, nil
}

// stampArchivedStatus writes status: done into the frontmatter unless the card
// already records superseded — a superseded card's word is the reason it
// stopped, and archiving must not overwrite it.
func stampArchivedStatus(fullPath string) error {
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read card before archive stamp: %w", err)
	}
	if bytesContainsStatusSuperseded(content) {
		return nil
	}
	_, err = stampFrontmatterField(fullPath, "status", string(StatusDone))
	return err
}

func bytesContainsStatusSuperseded(content []byte) bool {
	return containsLine(content, []byte("status: superseded"))
}

func containsLine(content, line []byte) bool {
	if containsAt(content, 0, line) {
		return true
	}
	for i := 0; i+1 < len(content); i++ {
		if content[i] == '\n' && containsAt(content, i+1, line) {
			return true
		}
	}
	return false
}

func containsAt(content []byte, at int, line []byte) bool {
	if at+len(line) > len(content) {
		return false
	}
	for j := 0; j < len(line); j++ {
		if content[at+j] != line[j] {
			return false
		}
	}
	return true
}

// archiveDirParts keeps the card's zone (or kind) directory under storage, so
// tasks/{module}/done/ drains into tasks/{module}/_archive/done/.
func archiveDirParts(parts []string) []string {
	if zoneIdx, _, inZone := ZoneSegment(parts); inZone {
		dir := append([]string{}, parts[:zoneIdx]...)
		return append(dir, StorageWriteDir, parts[zoneIdx])
	}
	for i, name := range parts[:len(parts)-1] {
		if IsKindDir(name) {
			dir := append([]string{}, parts[:i]...)
			return append(dir, StorageWriteDir, name)
		}
	}
	dir := append([]string{}, parts[:len(parts)-1]...)
	return append(dir, StorageWriteDir)
}

// reconcileCardLinks rewrites links on both sides of a move. Outbound: the
// moved card's own relative links resolve from a new directory, so they are
// rewritten to keep pointing at the same files. Inbound: every other document
// that linked to the old path is retargeted to the new one. Storage documents
// are never edited, and the moved card is skipped on the inbound pass.
func reconcileCardLinks(root, oldTasksRel, newTasksRel string) LinkUpdate {
	update := LinkUpdate{}
	oldRepoRel := path.Join(TasksDir, oldTasksRel)
	newRepoRel := path.Join(TasksDir, newTasksRel)
	oldDir := path.Dir(oldRepoRel)
	newDir := path.Dir(newRepoRel)

	if oldDir != newDir {
		full := filepath.Join(root, filepath.FromSlash(newRepoRel))
		if contentBytes, err := os.ReadFile(full); err == nil {
			rewritten, rewrites := retargetOutbound(string(contentBytes), oldDir, newDir)
			if len(rewrites) > 0 {
				if err := writePreservingMode(full, []byte(rewritten)); err != nil {
					update.Skipped = newRepoRel
				} else {
					update.Outbound = rewrites
				}
			}
		}
	}
	reconcileInbound(root, oldRepoRel, newRepoRel, &update)
	return update
}

// reconcileArchiveLinks retargets the documents that pointed at an archived
// card. The archive changes only the directory prefix, so inbound links are
// the whole reconciliation.
func reconcileArchiveLinks(root, oldTasksRel, newTasksRel string) LinkUpdate {
	update := LinkUpdate{}
	reconcileInbound(root, path.Join(TasksDir, oldTasksRel), path.Join(TasksDir, newTasksRel), &update)
	return update
}

// reconcileInbound retargets every tree document whose links resolve to the
// card's old repo-relative path.
func reconcileInbound(root, oldRepoRel, newRepoRel string, update *LinkUpdate) {
	for _, doc := range treeMarkdown(root) {
		docRepoRel := path.Join(TasksDir, doc)
		if docRepoRel == newRepoRel || IsStorageDir(path.Dir(doc)) {
			continue
		}
		full := filepath.Join(root, TasksDir, filepath.FromSlash(doc))
		contentBytes, err := os.ReadFile(full)
		if err != nil {
			update.Skipped = docRepoRel
			return
		}
		docDir := path.Join(TasksDir, path.Dir(doc))
		retargeted, lines := retargetLinksIn(
			string(contentBytes),
			docDir,
			oldRepoRel,
			relPathFrom(docDir, newRepoRel),
		)
		if len(lines) == 0 {
			continue
		}
		if err := writePreservingMode(full, []byte(retargeted)); err != nil {
			update.Skipped = docRepoRel
			return
		}
		linesForDoc := make([]int, 0, len(lines))
		for _, rewrite := range lines {
			linesForDoc = append(linesForDoc, rewrite.Line)
		}
		sort.Ints(linesForDoc)
		update.Inbound = append(update.Inbound, InboundDoc{Path: docRepoRel, Lines: linesForDoc})
	}
	sort.Slice(update.Inbound, func(i, j int) bool { return update.Inbound[i].Path < update.Inbound[j].Path })
}

// treeMarkdown lists every markdown file under tasks/, sorted, storage
// included: the caller decides which documents may be edited.
func treeMarkdown(root string) []string {
	var docs []string
	_ = filepath.WalkDir(filepath.Join(root, TasksDir), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".ce" || d.Name() == "evidence" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(filepath.Join(root, TasksDir), p)
		if err != nil || filepath.Ext(rel) != ".md" {
			return nil
		}
		docs = append(docs, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(docs)
	return docs
}

// retargetOutbound rewrites a moved card's relative links so the same targets
// keep resolving from the card's new directory.
func retargetOutbound(content, oldDir, newDir string) (string, []LinkRewrite) {
	var rewrites []LinkRewrite
	var out strings.Builder
	var fences FenceScanner
	// Exact reconstruction, as in rewriteRelativeLinks: the content's own
	// trailing-newline shape survives untouched.
	for i, raw := range strings.Split(content, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		if fences.Classify(raw) != OutsideFence {
			out.WriteString(raw)
			continue
		}
		newLine, lines := retargetLineOutbound(raw, i+1, oldDir, newDir)
		rewrites = append(rewrites, lines...)
		out.WriteString(newLine)
	}
	return out.String(), rewrites
}

func retargetLineOutbound(line string, lineNo int, oldDir, newDir string) (string, []LinkRewrite) {
	var rewrites []LinkRewrite
	var b strings.Builder
	for i := 0; i < len(line); {
		m := markdownLinkRe.FindStringSubmatchIndex(line[i:])
		if m == nil {
			b.WriteString(line[i:])
			break
		}
		start, end := i+m[0], i+m[1]
		rawDest := line[i+m[6] : i+m[7]]
		target, escaped, ok := parseLinkDestination(rawDest)
		if !ok || strings.HasPrefix(target, "..") {
			b.WriteString(line[i:end])
			i = end
			continue
		}
		// The link resolved from the old directory; keep it resolving there.
		resolved := path.Join(oldDir, target)
		newTarget := relPathFrom(newDir, resolved)
		if newTarget == target {
			b.WriteString(line[i:end])
			i = end
			continue
		}
		if escaped {
			newTarget = escapePath(newTarget)
		}
		b.WriteString(line[i:start])
		b.WriteString(line[i+m[2]:i+m[3]] + "[" + line[i+m[4]:i+m[5]] + "](" + newTarget + ")")
		rewrites = append(rewrites, LinkRewrite{Line: lineNo, From: target, To: newTarget})
		i = end
	}
	return b.String(), rewrites
}

// retargetLinksIn rewrites the links of one document whose resolved
// destination is oldResolved, writing newLiteral, and returns the rewritten
// content plus one entry per changed line.
func retargetLinksIn(content, docDir, oldResolved, newLiteral string) (string, []LinkRewrite) {
	rewritten, rewrites, err := rewriteRelativeLinks(content, docDir, oldResolved, newLiteral)
	if err != nil || len(rewrites) == 0 {
		return content, nil
	}
	return rewritten, rewrites
}

// relPathFrom renders target as a path relative to fromDir.
func relPathFrom(fromDir, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(fromDir), filepath.FromSlash(target))
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}
