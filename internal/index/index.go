package index

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/avnis/kb-system/internal/frontmatter"
)

type Entry struct {
	Slug    string
	Summary string
}

const IndexHeader = "# Wiki Index\n"

// mu is a package-level mutex protecting all INDEX.md operations.
// Since kb is a single binary, in-process locking is sufficient.
var mu sync.Mutex

// Read parses INDEX.md into entries.
func Read(indexPath string) ([]Entry, error) {
	mu.Lock()
	defer mu.Unlock()
	return readUnlocked(indexPath)
}

// Append adds a new entry or updates an existing one.
func Append(indexPath string, entry Entry) error {
	mu.Lock()
	defer mu.Unlock()

	entries, err := readUnlocked(indexPath)
	if err != nil {
		return err
	}

	found := false
	for i, existing := range entries {
		if strings.EqualFold(existing.Slug, entry.Slug) {
			entries[i].Summary = entry.Summary
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, entry)
	}

	return writeUnlocked(indexPath, entries)
}

// Remove deletes an entry by slug.
func Remove(indexPath string, slug string) error {
	mu.Lock()
	defer mu.Unlock()

	entries, err := readUnlocked(indexPath)
	if err != nil {
		return err
	}

	var filtered []Entry
	for _, existing := range entries {
		if !strings.EqualFold(existing.Slug, slug) {
			filtered = append(filtered, existing)
		}
	}

	return writeUnlocked(indexPath, filtered)
}

// Rebuild regenerates INDEX.md entirely from wiki/*.md frontmatter summaries.
// This is the disaster recovery path.
func Rebuild(wikiDir string, indexPath string) error {
	mu.Lock()
	defer mu.Unlock()

	var entries []Entry

	err := filepath.WalkDir(wikiDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "INDEX.md") || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read wiki file %s: %w", path, err)
		}

		fm, _, err := frontmatter.ParseWiki(content)
		if err != nil || fm.Summary == "" {
			return nil
		}

		slug := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		entries = append(entries, Entry{Slug: slug, Summary: fm.Summary})
		return nil
	})

	if err != nil {
		return err
	}

	return writeUnlocked(indexPath, entries)
}

// Exists checks if a slug has an entry in INDEX.md.
func Exists(indexPath string, slug string) (bool, error) {
	entries, err := Read(indexPath)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Slug, slug) {
			return true, nil
		}
	}
	return false, nil
}

// readUnlocked reads INDEX.md without acquiring the mutex (caller must hold it).
func readUnlocked(indexPath string) ([]Entry, error) {
	file, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var entries []Entry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "- [[") {
			continue
		}

		endBracket := strings.Index(line, "]]")
		if endBracket == -1 {
			continue
		}

		slug := line[4:endBracket]
		rem := strings.TrimSpace(line[endBracket+2:])
		rem = strings.TrimPrefix(rem, "—")
		rem = strings.TrimPrefix(rem, "--")
		rem = strings.TrimPrefix(rem, "-")
		summary := strings.TrimSpace(rem)

		entries = append(entries, Entry{Slug: slug, Summary: summary})
	}
	return entries, scanner.Err()
}

// writeUnlocked writes entries to INDEX.md without acquiring the mutex (caller must hold it).
func writeUnlocked(indexPath string, entries []Entry) error {
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Slug) < strings.ToLower(entries[j].Slug)
	})

	file, err := os.Create(indexPath)
	if err != nil {
		return err
	}
	defer file.Close()

	w := bufio.NewWriter(file)
	w.WriteString(IndexHeader)
	w.WriteString(fmt.Sprintf("<!-- count: %d -->\n\n", len(entries)))
	for _, entry := range entries {
		w.WriteString(fmt.Sprintf("- [[%s]] — %s\n", entry.Slug, entry.Summary))
	}
	return w.Flush()
}
