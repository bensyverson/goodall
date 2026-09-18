package mailbox

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Apple reads Apple Mail's on-disk store.
//
// The store is a tree: V10/<account>/<Folder>.mbox/<mailbox>/Data, and under
// Data any number of numeric levels before a Messages directory of .emlx
// files. A .emlx file is the message's byte count in a field of exactly ten
// characters, a newline, exactly that many bytes of RFC 822 message, and then
// an XML property list whose date-received key holds the Unix second the
// message arrived. A .partial.emlx is the same file for a message Mail never
// fetched in full; it is read as a message with whatever body it has.
type Apple struct {
	// dirs are the directories whose Messages subdirectories get walked.
	dirs []string
}

// appleCountWidth is the width of a .emlx file's count field, followed by a
// newline: the message starts at appleCountWidth+1.
const appleCountWidth = 10

// maxPlistBytes bounds the property list read from the end of a .emlx file.
// Mail writes a few hundred bytes; this is room to spare and a ceiling on what
// a corrupt file can cost.
const maxPlistBytes = 1 << 16

// OpenApple opens an Apple Mail store at path, which may be any of: a store
// root (the directory holding V10, or V10 itself), an account directory, a
// <Folder>.mbox directory, or one mailbox directory inside a folder. Given a
// store root or an account directory it lists every INBOX.mbox beneath;
// given a folder or a mailbox directory it lists that folder, whichever it is.
func OpenApple(path string) (*Apple, error) {
	// A path completed by a shell carries a trailing separator, and the shape
	// of the store is read off the last element of the path.
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: apple store %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mailbox: apple store %s is not a directory", path)
	}
	dirs, err := appleScanDirs(path)
	if err != nil {
		return nil, err
	}
	return &Apple{dirs: dirs}, nil
}

// appleScanDirs works out which directories to walk from the shape of path.
func appleScanDirs(path string) ([]string, error) {
	if filepath.Base(path) == "V10" {
		return appleInboxes(path)
	}
	if isDir(filepath.Join(path, "V10")) {
		return appleInboxes(filepath.Join(path, "V10"))
	}
	if strings.HasSuffix(path, ".mbox") || isDir(filepath.Join(path, "Data")) {
		return []string{path}, nil
	}
	if hasMboxChild(path) {
		return appleInboxes(path)
	}
	return nil, fmt.Errorf("mailbox: %s is not an apple store, account, folder or mailbox directory", path)
}

// appleInboxes finds every INBOX.mbox under root. Only the inbox is listed:
// triage is about what arrived, and Sent, Drafts and Junk are not that.
func appleInboxes(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == "INBOX.mbox" {
			found = append(found, path)
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mailbox: apple store %s: %w", root, err)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("mailbox: no INBOX.mbox under %s", root)
	}
	return found, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// hasMboxChild reports whether the directory holds mail folders, which is what
// an account directory looks like.
func hasMboxChild(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".mbox") {
			return true
		}
	}
	return false
}

// List lists the store's messages newest first by the plist's date-received,
// falling back to the file's modification time when the plist carries none.
// Only the messages that survive the limit are read and parsed.
//
// A .emlx file whose count line or message will not read is reported in the
// listing's Skipped: Mail writes into the store while it runs, so a store of
// tens of thousands of files can always hold one that is half-written, and one
// such file must not cost a reader the rest of the inbox. A directory that
// cannot be walked is the store failing, and is returned as an error.
func (a *Apple) List(ctx context.Context, limit int) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	var (
		items   []listed
		skipped []Skip
	)
	for _, dir := range a.dirs {
		found, passed, err := a.scan(ctx, dir)
		if err != nil {
			return Listing{}, err
		}
		items = append(items, found...)
		skipped = append(skipped, passed...)
	}
	return collect(ctx, items, limit, skipped)
}

// scan walks one folder for .emlx files and reads each one's count line and
// property list, never its body. A file it cannot read becomes a Skip; only a
// directory it cannot walk is an error.
func (a *Apple) scan(ctx context.Context, dir string) ([]listed, []Skip, error) {
	var (
		items   []listed
		skipped []Skip
	)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d == nil || d.IsDir() {
				return err
			}
			skipped = append(skipped, Skip{Path: path, Err: fmt.Errorf("mailbox: %s: %w", path, err)})
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".emlx") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "Messages" {
			return nil
		}
		f, err := emlxHeader(path)
		if err != nil {
			skipped = append(skipped, Skip{Path: path, Err: err})
			return nil
		}
		items = append(items, listed{
			path:     path,
			received: f.received,
			key:      path,
			read:     func() ([]byte, error) { return emlxMessage(f.path, f.count) },
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return items, skipped, nil
}

// appleFile is one located .emlx file: enough to read the message later
// without walking the store again.
type appleFile struct {
	path     string
	count    int64
	received time.Time
}

// emlxHeader reads a .emlx file's count line and trailing property list.
func emlxHeader(path string) (appleFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return appleFile{}, fmt.Errorf("mailbox: %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return appleFile{}, fmt.Errorf("mailbox: %s: %w", path, err)
	}
	count, err := emlxCount(f, path)
	if err != nil {
		return appleFile{}, err
	}
	start := int64(appleCountWidth) + 1
	if info.Size() < start+count {
		return appleFile{}, fmt.Errorf("mailbox: %s: the count line says %d bytes but the file holds %d after it", path, count, info.Size()-start)
	}

	received := info.ModTime()
	if plistLen := info.Size() - (start + count); plistLen > 0 {
		if plistLen > maxPlistBytes {
			plistLen = maxPlistBytes
		}
		plist := make([]byte, plistLen)
		if _, err := f.ReadAt(plist, start+count); err != nil && !errors.Is(err, io.EOF) {
			return appleFile{}, fmt.Errorf("mailbox: %s: %w", path, err)
		}
		seconds, ok, err := plistInt(plist, "date-received")
		if err != nil {
			return appleFile{}, fmt.Errorf("mailbox: %s: %w", path, err)
		}
		if ok {
			received = time.Unix(seconds, 0).UTC()
		}
	}
	return appleFile{path: path, count: count, received: received}, nil
}

// emlxCount reads the ten-character count field and the newline after it.
func emlxCount(r io.ReaderAt, path string) (int64, error) {
	line := make([]byte, appleCountWidth+1)
	if _, err := r.ReadAt(line, 0); err != nil {
		return 0, fmt.Errorf("mailbox: %s: reading the count line: %w", path, err)
	}
	if line[appleCountWidth] != '\n' {
		return 0, fmt.Errorf("mailbox: %s: the count line is not %d characters and a newline", path, appleCountWidth)
	}
	field := strings.TrimRight(string(line[:appleCountWidth]), " ")
	count, err := strconv.ParseInt(field, 10, 64)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("mailbox: %s: the count line reads %q, which is not a byte count", path, string(line[:appleCountWidth]))
	}
	return count, nil
}

// emlxMessage reads the message bytes a .emlx file's count line describes.
func emlxMessage(path string, count int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: %s: %w", path, err)
	}
	defer f.Close()
	raw := make([]byte, count)
	if _, err := f.ReadAt(raw, int64(appleCountWidth)+1); err != nil {
		return nil, fmt.Errorf("mailbox: %s: reading %d message bytes: %w", path, count, err)
	}
	return raw, nil
}

// plistInt reads one top-level integer out of an XML property list.
//
// A plist dict alternates keys and values, so the decoder tracks the key it
// last saw and the dict nesting: a key of the same name inside a nested dict
// is a different key and does not answer. Written here rather than taken from
// a library because it is the only plist the package reads.
func plistInt(data []byte, key string) (int64, bool, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	pending := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, false, nil
			}
			return 0, false, fmt.Errorf("reading the property list: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "dict":
				depth++
			case "key":
				var name string
				if err := decoder.DecodeElement(&name, &element); err != nil {
					return 0, false, fmt.Errorf("reading a property list key: %w", err)
				}
				if depth == 1 {
					pending = strings.TrimSpace(name)
				}
			case "integer":
				if depth != 1 || pending != key {
					pending = ""
					continue
				}
				var value string
				if err := decoder.DecodeElement(&value, &element); err != nil {
					return 0, false, fmt.Errorf("reading %s: %w", key, err)
				}
				n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
				if err != nil {
					return 0, false, fmt.Errorf("%s is %q, which is not a number", key, strings.TrimSpace(value))
				}
				return n, true, nil
			default:
				pending = ""
			}
		case xml.EndElement:
			if element.Name.Local == "dict" {
				depth--
				pending = ""
			}
		}
	}
}
