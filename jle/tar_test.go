package jle

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"testing"
)

// tarOf writes the given name→body pairs into an in-memory tar the way
// `gomount stream` emits one: each file is a regular entry whose name is its full
// volume path and whose body is the file's bytes.
func tarOf(t *testing.T, files map[string][]byte) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Size:     int64(len(body)),
			Mode:     0o400,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}

func decodeRecords(t *testing.T, b []byte) []record {
	t.Helper()
	var recs []record
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var r record
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		recs = append(recs, r)
	}
	return recs
}

// TestRunTarSelectsJumpLists covers the --tar plumbing without a committed
// artefact: runTar processes only AutomaticDestinations entries — a non-jump-list
// and a CustomDestinations file are skipped (never counted), and an
// AutomaticDestinations-named entry whose bytes are not an OLE compound file is
// counted as a failure while the stream keeps going. The jump-list decode itself
// is mscfb's, exercised by the existing -f/-d tests.
func TestRunTarSelectsJumpLists(t *testing.T) {
	tr := tarOf(t, map[string][]byte{
		"Windows/System32/notepad.exe":                                   []byte("MZ not a jump list"),
		"Recent/CustomDestinations/abc.customDestinations-ms":            []byte("junk"),
		"Recent/AutomaticDestinations/deadbeef.automaticDestinations-ms": []byte("not an OLE compound file"),
	})
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	if failed := runTar(tr, enc, true); failed != 1 {
		t.Fatalf("want 1 failure (the corrupt jump list; the others are skipped uncounted), got %d", failed)
	}
	if recs := decodeRecords(t, out.Bytes()); len(recs) != 0 {
		t.Fatalf("want 0 records, got %d", len(recs))
	}
}

// TestRunTarEmptyStreamIsClean checks an empty tar yields no records and no
// failures.
func TestRunTarEmptyStreamIsClean(t *testing.T) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	if failed := runTar(tarOf(t, map[string][]byte{}), enc, true); failed != 0 {
		t.Fatalf("empty tar should report 0 failures, got %d", failed)
	}
	if recs := decodeRecords(t, out.Bytes()); len(recs) != 0 {
		t.Fatalf("empty tar should emit no records, got %d", len(recs))
	}
}
