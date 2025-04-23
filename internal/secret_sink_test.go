package internal

import (
	"testing"
	"time"
)

func TestRandomSecretSink(t *testing.T) {
	expLen := 3
	rss := NewRandomSecretSink(expLen).(*RandomSecretSink)

	input := []string{"1", "2", "3", "1", "4", "5"}
	for _, actual := range input {
		gotRandom, err := rss.Add(actual)
		if err != nil {
			t.Errorf("Add(%q) fail err=%v", actual, err)
		}
		if len(gotRandom) != randomLen {
			t.Errorf("got len(gotRandom)=%d, expect=%d", len(gotRandom), randomLen)
		}
		time.Sleep(1 * time.Millisecond)
	}

	t.Logf("rss.secretStore=%#v", rss.secretStore)

	gotLen := len(rss.secretStore)
	if gotLen != expLen {
		t.Errorf("got len(rss.secretStore)=%d, expect=%d", gotLen, expLen)
	}

	expectStore := []string{"1", "4", "5"}
	for _, actual := range expectStore {
		_, found := rss.secretStore[actual]
		if !found {
			t.Errorf("%q not found in rss.secretStore", actual)
		}
	}
}
