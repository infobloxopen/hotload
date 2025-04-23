package internal

import (
	"testing"

	"github.com/google/uuid"
)

func TestNonRandomReader(t *testing.T) {
	var start byte = 3
	nrr := NewNonRandomReader(start)
	if nrr == nil {
		t.Fatalf("NewNonRandomReader returned nil")
	}

	buf := make([]byte, 523)
	count, err := nrr.Read(buf)
	if nrr == nil {
		t.Errorf("NewNonRandomReader.Read returned err=%v", err)
	}
	if count != len(buf) {
		t.Errorf("got count=%d, expected len=%d", count, len(buf))
	}

	for idx, gotVal := range buf {
		expVal := (idx + int(start) + (idx / 256)) % 256
		//t.Logf("got buf[%d]=%d, expected %d", idx, gotVal, expVal)
		if int(gotVal) != expVal {
			t.Errorf("got buf[%d]=%d, expected %d", idx, gotVal, expVal)
		}
	}
}

func Test256UniqueGuidsUsingNonRandomReader(t *testing.T) {
	nrr := NewNonRandomReader(99)
	uuid.SetRand(nrr)
	uniqueGuids := make(map[string]struct{})
	for i := 1; i <= 256; i++ {
		guid := uuid.New().String()
		//t.Logf("generated %dth guid=%q", i, guid)
		_, found := uniqueGuids[guid]
		if found {
			t.Errorf("non-unique guid=%q for i=%d", guid, i)
		} else {
			uniqueGuids[guid] = struct{}{}
		}
	}

	if len(uniqueGuids) != 256 {
		t.Errorf("len(uniqueGuids)=%d but should be 256", len(uniqueGuids))
	}

	guid := uuid.New().String()
	//t.Logf("generated 257th guid=%q", guid)
	_, found := uniqueGuids[guid]
	if !found {
		t.Errorf("257th guid=%q should not be unique", guid)
	}
}
