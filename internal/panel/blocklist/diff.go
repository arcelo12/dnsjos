package blocklist

import (
	"bytes"
	"os"
	"strings"
	"syscall"

	"github.com/colinmarc/cdb"
)

// maxTracked caps the names kept per direction; past it only the count grows.
// ponytail: a huge change (e.g. a source removed) records its first 500k names only.
var maxTracked = 500_000

type diff struct {
	added, removed int
	names          map[string]bool // name → added (false = removed)
}

// diffCDB compares two builds key by key. Both files are mmapped, so a 10M-entry
// diff costs page cache, not heap; only the changed names are held in memory.
func diffCDB(oldPath, newPath string) (d diff, err error) {
	o, err := mmapCDB(oldPath)
	if err != nil {
		return d, err
	}
	defer o.close()
	n, err := mmapCDB(newPath)
	if err != nil {
		return d, err
	}
	defer n.close()
	d.names = map[string]bool{}
	if d.added, err = missing(n.db, o.db, d.names, true); err != nil {
		return d, err
	}
	d.removed, err = missing(o.db, n.db, d.names, false)
	return d, err
}

// missing records the keys of a that b lacks. A key listed by several sources is
// counted once (until maxTracked is reached).
func missing(a, b *cdb.CDB, names map[string]bool, added bool) (int, error) {
	count, kept := 0, 0
	it := a.Iter()
	for it.Next() {
		v, err := b.Get(it.Key())
		if err != nil {
			return 0, err
		}
		if v != nil {
			continue
		}
		if kept >= maxTracked {
			count++
			continue
		}
		name := keyName(it.Key())
		if _, dup := names[name]; !dup {
			names[name] = added
			count++
			kept++
		}
	}
	return count, it.Err()
}

// keyName decodes a wire-format key back to its dotted name ("\x01a\x03com\x00" → "a.com").
func keyName(k []byte) string {
	var sb strings.Builder
	for len(k) > 1 && int(k[0]) < len(k) {
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.Write(k[1 : 1+k[0]])
		k = k[1+k[0]:]
	}
	return sb.String()
}

type mapped struct {
	db   *cdb.CDB
	data []byte
}

func (m mapped) close() { syscall.Munmap(m.data) }

func mmapCDB(path string) (mapped, error) {
	f, err := os.Open(path)
	if err != nil {
		return mapped{}, err
	}
	defer f.Close() // the mapping outlives the descriptor
	st, err := f.Stat()
	if err != nil {
		return mapped{}, err
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return mapped{}, err
	}
	db, err := cdb.New(bytes.NewReader(data), nil)
	if err != nil {
		syscall.Munmap(data)
		return mapped{}, err
	}
	return mapped{db, data}, nil
}
