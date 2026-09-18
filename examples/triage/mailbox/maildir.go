package mailbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Maildir reads a Maildir: one file per message, delivered into new and moved
// into cur once a client has seen it. Deliveries still in progress live in
// tmp, which is never listed.
type Maildir struct {
	path string
}

// maildirDirs are the subdirectories that hold delivered messages.
var maildirDirs = []string{"cur", "new"}

// maxMaildirHeaderBytes bounds the header block read while scanning for dates.
const maxMaildirHeaderBytes = 1 << 16

// OpenMaildir opens the Maildir at path, which must hold a cur or a new
// directory.
func OpenMaildir(path string) (*Maildir, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: maildir %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mailbox: maildir %s is not a directory", path)
	}
	for _, dir := range maildirDirs {
		if isDir(filepath.Join(path, dir)) {
			return &Maildir{path: path}, nil
		}
	}
	return nil, fmt.Errorf("mailbox: maildir %s holds neither a cur nor a new directory", path)
}

// List lists cur and new newest first by the Date header, falling back to the
// file's modification time — which is what a Maildir delivery sets, and so is
// a good answer for a message with no Date. Only the messages that survive the
// limit are read and parsed.
//
// A file that will not read or parse as a message is reported in the listing's
// Skipped and the listing goes on; a directory that cannot be read is returned
// as an error.
func (d *Maildir) List(ctx context.Context, limit int) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	var (
		items   []listed
		skipped []Skip
	)
	for _, dir := range maildirDirs {
		found, passed, err := d.scan(ctx, filepath.Join(d.path, dir))
		if err != nil {
			return Listing{}, err
		}
		items = append(items, found...)
		skipped = append(skipped, passed...)
	}
	return collect(ctx, items, limit, skipped)
}

// scan lists one Maildir subdirectory, reading each message's header block but
// none of its body. Dotfiles are skipped: Maildir keeps its own state in them.
// A file it cannot open becomes a Skip; only the directory failing is an error.
func (d *Maildir) scan(ctx context.Context, dir string) ([]listed, []Skip, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("mailbox: %s: %w", dir, err)
	}
	var (
		items   []listed
		skipped []Skip
	)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			skipped = append(skipped, Skip{Path: path, Err: fmt.Errorf("mailbox: %s: %w", path, err)})
			continue
		}
		received := info.ModTime()
		block, err := readHeaderBlock(path, maxMaildirHeaderBytes)
		if err != nil {
			skipped = append(skipped, Skip{Path: path, Err: err})
			continue
		}
		if h, err := headerOnly(block); err == nil {
			if when, err := h.Date(); err == nil {
				received = when
			}
		}
		items = append(items, listed{
			path:     path,
			received: received,
			key:      path,
			read:     func() ([]byte, error) { return os.ReadFile(path) },
		})
	}
	return items, skipped, nil
}

// readHeaderBlock reads a message file up to the blank line that ends its
// header, so a date can be had without the body.
func readHeaderBlock(path string, max int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: %s: %w", path, err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var block bytes.Buffer
	for block.Len() < max {
		line, err := reader.ReadBytes('\n')
		if len(trimEOL(line)) == 0 {
			break
		}
		block.Write(line)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("mailbox: %s: %w", path, err)
			}
			break
		}
	}
	return block.Bytes(), nil
}
