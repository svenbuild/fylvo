package fs

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
)

// Addresses of connected storages are refused by the ordinary parser, however they are
// written, and accepted by the one meant for them.
func TestConnectedUriRefusedByDefault(t *testing.T) {
	refused := []string{
		"cloudreve://abc123@connected",
		"cloudreve://abc123@connected/",
		"cloudreve://abc123@connected/Docs/a.txt",
		"cloudreve://connected/Docs",
		"cloudreve://abc123@CONNECTED/Docs",
		"cloudreve://abc123@Connected/Docs?name=x",
	}
	for _, address := range refused {
		if _, err := NewUriFromString(address); !errors.Is(err, ErrConnectedUri) {
			t.Errorf("NewUriFromString(%q): %v, want ErrConnectedUri", address, err)
		}
		if _, err := NewUriFromStrings("cloudreve://my/a", address); !errors.Is(err, ErrConnectedUri) {
			t.Errorf("NewUriFromStrings(..., %q): %v, want ErrConnectedUri", address, err)
		}

		uri, err := NewAnyUriFromString(address)
		if err != nil {
			t.Errorf("NewAnyUriFromString(%q): %v", address, err)
			continue
		}
		if uri.FileSystem() != constants.FileSystemConnected {
			t.Errorf("NewAnyUriFromString(%q): file system %q", address, uri.FileSystem())
		}
	}

	// A host written with an escape is no host for the parser at all.
	if _, err := NewUriFromString("cloudreve://abc@%63onnected/Docs"); err == nil {
		t.Error("an escaped host was accepted")
	}
}

// Everything else parses as before.
func TestOtherUrisUnchanged(t *testing.T) {
	for _, address := range []string{
		"cloudreve://my",
		"cloudreve://my/Docs/a.txt",
		"cloudreve://my/connected/a.txt",
		"cloudreve://connected@my/a.txt",
		"cloudreve://abc@share/Docs/",
		"cloudreve://trash",
		"cloudreve://shared_with_me",
	} {
		plain, err := NewUriFromString(address)
		if err != nil {
			t.Errorf("NewUriFromString(%q): %v", address, err)
			continue
		}
		any, err := NewAnyUriFromString(address)
		if err != nil || any.String() != plain.String() {
			t.Errorf("NewAnyUriFromString(%q) = %v, %v; want %v", address, any, err, plain)
		}
	}
}

// The JSON form refuses connected addresses, the binary form used for the cache does not:
// one refused value there would make the whole cache unreadable at the next start.
func TestConnectedUriUnmarshal(t *testing.T) {
	var fromJSON URI
	err := json.Unmarshal([]byte(`{"uri":"cloudreve://abc123@connected/Docs"}`), &fromJSON)
	if !errors.Is(err, ErrConnectedUri) {
		t.Errorf("JSON: %v, want ErrConnectedUri", err)
	}

	var ok URI
	if err := json.Unmarshal([]byte(`{"uri":"cloudreve://my/Docs"}`), &ok); err != nil || ok.FileSystem() != constants.FileSystemMy {
		t.Errorf("JSON of an ordinary address: %v", err)
	}

	connected, err := NewAnyUriFromString("cloudreve://abc123@connected/Docs")
	if err != nil {
		t.Fatal(err)
	}
	type stored struct {
		Uris []*URI
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(stored{Uris: []*URI{connected}}); err != nil {
		t.Fatal(err)
	}
	var back stored
	if err := gob.NewDecoder(&buf).Decode(&back); err != nil {
		t.Fatalf("binary form: %v", err)
	}
	if len(back.Uris) != 1 || back.Uris[0].String() != connected.String() {
		t.Fatalf("binary form: %+v", back)
	}
}
