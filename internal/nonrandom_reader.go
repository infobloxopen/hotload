package internal

import (
	"sync"
)

// NonRandomReader is a reader that returns a deterministic
// (non-random) sequence of bytes.
// Intended for testing to supply deterministic "random" values.
// The sequence of bytes eventually rolls over and repeats itself,
// so this reader should NOT be used in production code.
type NonRandomReader struct {
	sync.Mutex
	nextVal byte
	counter byte
}

// NewNonRandomReader returns a new NonRandomReader.
// The first byte supplied will be `startVal`.
func NewNonRandomReader(startVal byte) *NonRandomReader {
	return &NonRandomReader{
		nextVal: startVal,
	}
}

// Read is thread-safe and implements io.Reader interface.
// Fills buffer with non-random bytes and returns count of filled bytes,
// which in this case is always the length of the buffer.
//
// Returns byte values in the following deterministic sequence
// (note that all byte values are modulo 256):
// 1st   set of 256 bytes: [(start)..(start+255)]
// 2nd   set of 256 bytes: [(start+1)..(start+1+255)]
// 3rd   set of 256 bytes: [(start+2)..(start+2+255)]
// ...
// 255th set of 256 bytes: [(start+254)..(start+254+255)]
// 256th set of 256 bytes: [(start+255)..(start+255+255)]
// 257th set of 256 bytes: [(start)..(start+255)] (rolled over, sequence repeats itself)
//
// Reason for doing this is that uuid consumes 16 bytes on each uuid generation.
// If we don't shift the sequence after every 256 bytes,
// than NonRandomReader will only supply enough bytes for 16 unique uuids.
// If we shift the sequence after every 256 bytes,
// than NonRandomReader will supply enough bytes for 256 unique uuids
// before rolling over.
func (nrr *NonRandomReader) Read(buf []byte) (count int, err error) {
	nrr.Lock()
	defer nrr.Unlock()

	count = len(buf)
	for i := 0; i < count; i++ {
		buf[i] = nrr.nextVal
		nrr.nextVal = nrr.nextVal + 1
		nrr.counter = nrr.counter + 1

		// If next set of 256 bytes, shift sequence
		if nrr.counter == 0 {
			nrr.nextVal = nrr.nextVal + 1
		}
	}

	return count, nil
}
