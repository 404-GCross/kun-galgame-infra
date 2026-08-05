package personphotos

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"api/pkg/imageclient"

	"gorm.io/datatypes"
)

// writeFile creates a non-empty file, making its directory.
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMirrorNoRootAndNoManifest(t *testing.T) {
	// An empty root is the pre-crawl dry run: it must load, and resolve nothing.
	m, err := loadMirror("")
	if err != nil {
		t.Fatalf("loadMirror(\"\"): %v", err)
	}
	if m.has("123") {
		t.Fatal("empty root resolved bytes")
	}

	// A mirror with bytes but no dims.jsonl must still work by probing.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "123", "logo.png"))
	m, err = loadMirror(root)
	if err != nil {
		t.Fatalf("loadMirror(no manifest): %v", err)
	}
	got, ok := m.resolve("123")
	if !ok || filepath.Base(got) != "logo.png" {
		t.Fatalf("resolve = %q, %v; want .../logo.png, true", got, ok)
	}
}

// TestMirrorResolveManifestAndStem is also the compatibility pin: this lane
// reads the mirror the same crawler command writes for the label-logo lane, so
// the file stem is "logo", not "photo". Renaming it would silently stop
// resolving bytes already on disk.
func TestMirrorResolveManifestAndStem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "7", "logo.webp"))
	writeFile(t, filepath.Join(root, "9", "shot.gif"))
	writeFile(t, filepath.Join(root, "8", "photo.png")) // wrong stem — invisible
	manifest := `{"id":"9","file":"9/shot.gif","w":100,"h":100,"url":"https://example.test/9.gif"}
{"id":"404","file":"404/logo.jpg","w":1,"h":1,"url":""}

`
	if err := os.WriteFile(filepath.Join(root, dimsFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := loadMirror(root)
	if err != nil {
		t.Fatalf("loadMirror: %v", err)
	}
	// Probed by the crawler's stem, even though no manifest row names it.
	if got, ok := m.resolve("7"); !ok || filepath.Base(got) != "logo.webp" {
		t.Fatalf("resolve(7) = %q, %v", got, ok)
	}
	// The manifest's file wins over the stem probe.
	if got, ok := m.resolve("9"); !ok || filepath.Base(got) != "shot.gif" {
		t.Fatalf("resolve(9) = %q, %v", got, ok)
	}
	// A manifest row whose bytes are absent is not a resolution.
	if _, ok := m.resolve("404"); ok {
		t.Fatal("resolve(404): manifest row without bytes must not resolve")
	}
	// A file under any other stem is not this lane's artefact.
	if m.has("8") {
		t.Fatal("resolve(8): only the crawler's logo.<ext> stem counts")
	}
}

func TestLoadMirrorRejectsCorruptManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, dimsFileName), []byte("{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMirror(root); err == nil {
		t.Fatal("corrupt manifest must be a hard error, not a silent skip")
	}
}

func TestMergeProvenancePrependsAndPreservesOtherFields(t *testing.T) {
	cur := datatypes.JSON(`{"display_name":[{"source":"vndb","at":"2020-01-01T00:00:00Z"}],"photo_hash":[{"source":"bangumi","at":"2021-01-01T00:00:00Z"}]}`)
	out := mergeProvenance(cur, "bangumi", "2026-08-05T00:00:00Z")

	var doc map[string][]provEntry
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal merged provenance: %v", err)
	}
	if len(doc["display_name"]) != 1 || doc["display_name"][0].Source != "vndb" {
		t.Fatalf("other fields must survive untouched: %+v", doc["display_name"])
	}
	got := doc[provField]
	if len(got) != 2 {
		t.Fatalf("photo_hash provenance = %+v, want 2 entries", got)
	}
	if got[0].At != "2026-08-05T00:00:00Z" {
		t.Fatalf("latest entry must be first: %+v", got[0])
	}
	if got[1].At != "2021-01-01T00:00:00Z" {
		t.Fatalf("history must be kept: %+v", got[1])
	}
}

func TestMergeProvenanceFromEmptyAndUnreadable(t *testing.T) {
	for name, cur := range map[string]datatypes.JSON{
		"empty":      nil,
		"blank":      datatypes.JSON(`{}`),
		"unreadable": datatypes.JSON(`not json at all`),
	} {
		out := mergeProvenance(cur, sourceKey, "2026-08-05T00:00:00Z")
		var doc map[string][]provEntry
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		if len(doc[provField]) != 1 || doc[provField][0].Source != sourceKey {
			t.Fatalf("%s: want a single bangumi entry, got %+v", name, doc[provField])
		}
	}
}

func TestWriteIDsOnlyListsIDsWithoutBytes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "20", "logo.jpg"))
	m, err := loadMirror(root)
	if err != nil {
		t.Fatal(err)
	}
	cands := []candidate{
		{PersonID: 1, ExternalID: "30"},
		{PersonID: 2, ExternalID: "20"}, // already mirrored — omitted
		{PersonID: 3, ExternalID: "10"},
		{PersonID: 4, ExternalID: "30"}, // duplicate id — emitted once
	}
	out := filepath.Join(t.TempDir(), "ids.txt")
	n, err := writeIDs(out, cands, m)
	if err != nil {
		t.Fatalf("writeIDs: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || string(body) != "10\n30\n" {
		t.Fatalf("writeIDs = %d, %q; want 2, \"10\\n30\\n\"", n, string(body))
	}
}

func TestWindow(t *testing.T) {
	c := []candidate{{PersonID: 1}, {PersonID: 2}, {PersonID: 3}}
	if got := window(c, 0, 0); len(got) != 3 {
		t.Fatalf("no window = %d", len(got))
	}
	if got := window(c, 2, 1); len(got) != 2 || got[0].PersonID != 2 {
		t.Fatalf("window(2,1) = %+v", got)
	}
	if got := window(c, 0, 9); got != nil {
		t.Fatalf("offset past the end = %+v, want nil", got)
	}
}

// fakeUploader stands in for the image client so the write path can be exercised
// without an image service or a network.
type fakeUploader struct {
	hash    string
	err     error
	presets []string
	pinged  []string
}

func (f *fakeUploader) UploadWithSub(_ context.Context, _ io.Reader, _, preset, _ string) (*imageclient.UploadResult, error) {
	f.presets = append(f.presets, preset)
	if f.err != nil {
		return nil, f.err
	}
	return &imageclient.UploadResult{Hash: f.hash}, nil
}

func (f *fakeUploader) ReferencePing(_ context.Context, hashes []string) (*imageclient.ReferencePingResult, error) {
	f.pinged = append(f.pinged, hashes...)
	return &imageclient.ReferencePingResult{Updated: int64(len(hashes))}, nil
}

// TestFillDryRunNeverUploads is the guarantee the default mode rests on: a dry
// run reads no bytes into the image service and touches no row, whether or not
// the mirror has the file.
func TestFillDryRunNeverUploads(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "5", "logo.jpg"))
	m, err := loadMirror(root)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeUploader{hash: "nope"}
	r := &runner{cli: fake, mirror: m, stats: &Stats{}}

	got := r.fill(context.Background(), candidate{PersonID: 1, ExternalID: "5"}, false)
	if got.would != 1 || got.uploaded != 0 || got.hash != "" {
		t.Fatalf("dry run = %+v, want would=1 and nothing else", got)
	}
	// A candidate with no mirrored bytes is missing, in dry run and apply alike.
	got = r.fill(context.Background(), candidate{PersonID: 2, ExternalID: "404"}, true)
	if got.missing != 1 || got.uploaded != 0 {
		t.Fatalf("absent bytes = %+v, want missing=1", got)
	}
	if len(fake.presets) != 0 || len(fake.pinged) != 0 {
		t.Fatal("dry run touched the image service")
	}
}

// TestUploadUsesTheCatalogLogoPreset pins the preset: it is the one the catalog
// image client's image_allowed_presets lists for this scope, and a preset it
// does not list 403s every single upload.
func TestUploadUsesTheCatalogLogoPreset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "1", "logo.jpg")
	writeFile(t, path)

	fake := &fakeUploader{hash: "abc"}
	r := &runner{cli: fake, stats: &Stats{}}
	if _, err := r.upload(context.Background(), path); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(fake.presets) != 1 || fake.presets[0] != "catalog_logo" {
		t.Fatalf("preset = %v, want [catalog_logo]", fake.presets)
	}
}

// TestUploadRetriesTransientButNotTerminal pins the retry policy: quota and
// moderation are terminal (retrying them is pointless and costs the whole run),
// anything else is retried.
func TestUploadRetriesTransientButNotTerminal(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "1", "logo.jpg")
	writeFile(t, path)

	for _, terminal := range []error{imageclient.ErrQuotaExceeded, imageclient.ErrModerationRejected} {
		counting := &countingUploader{err: terminal}
		r := &runner{cli: counting, stats: &Stats{}}
		if _, err := r.upload(context.Background(), path); !errors.Is(err, terminal) {
			t.Fatalf("upload err = %v, want %v", err, terminal)
		}
		if counting.calls != 1 {
			t.Fatalf("terminal error retried %d times", counting.calls-1)
		}
	}

	// A transient error retries; the context is cancelled so the test does not
	// sit through the backoff schedule.
	counting := &countingUploader{err: errors.New("connection refused")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &runner{cli: counting, stats: &Stats{}}
	if _, err := r.upload(ctx, path); err == nil {
		t.Fatal("transient failure must surface as an error once retries are exhausted")
	}
	if counting.calls == 0 {
		t.Fatal("transient error was never attempted")
	}
}

// countingUploader always fails, counting attempts.
type countingUploader struct {
	err   error
	calls int
}

func (c *countingUploader) UploadWithSub(_ context.Context, _ io.Reader, _, _, _ string) (*imageclient.UploadResult, error) {
	c.calls++
	return nil, c.err
}

func (c *countingUploader) ReferencePing(_ context.Context, _ []string) (*imageclient.ReferencePingResult, error) {
	return &imageclient.ReferencePingResult{}, nil
}
