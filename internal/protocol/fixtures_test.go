package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolFixturesDecode(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "protocol", "fixtures", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no protocol fixtures found")
	}
	for _, path := range paths {
		path := path
		t.Run(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(data); err != nil {
				t.Fatalf("fixture does not satisfy protocol envelope: %v", err)
			}
		})
	}
}

func TestThreadSnapshotFixtureSeparatesSourceAndLatestPreview(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "fixtures", "thread.snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	message, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	var payload ThreadSnapshotPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Threads) != 1 || payload.Threads[0].Preview == payload.Threads[0].LatestMessagePreview || payload.Threads[0].LatestMessagePreview == "" {
		t.Fatalf("thread preview semantics are not separated: %#v", payload.Threads)
	}
}
