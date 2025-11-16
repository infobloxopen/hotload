package internal

import (
	"testing"

	"github.com/infobloxopen/hotload/internal/randstring"
)

func TestNonRandomReader(t *testing.T) {
	var start byte = 3
	nrr := NewNonRandomReader(start)
	if nrr == nil {
		t.Fatalf("NewNonRandomReader returned nil")
	}

	buf := make([]byte, 523)
	count, err := nrr.Read(buf)
	if err != nil {
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

func TestRandstringGenerate(t *testing.T) {
	// Test that randstring generates strings of the correct length
	for _, length := range []int{8, 16, 32} {
		str, err := randstring.Generate(length)
		if err != nil {
			t.Errorf("randstring.Generate(%d) returned error: %v", length, err)
		}
		if len(str) != length {
			t.Errorf("randstring.Generate(%d) returned string of length %d, expected %d", length, len(str), length)
		}
	}

	// Test that multiple calls generate different strings
	str1, _ := randstring.Generate(16)
	str2, _ := randstring.Generate(16)
	if str1 == str2 {
		t.Errorf("randstring.Generate produced identical strings: %s", str1)
	}
}
