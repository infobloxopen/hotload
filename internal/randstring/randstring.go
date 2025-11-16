package randstring

import (
	"crypto/rand"
	"encoding/hex"
)

// Generate creates a random string of the specified length using crypto/rand.
// The string will contain hexadecimal characters (0-9, a-f).
func Generate(length int) (string, error) {
	// Need length/2 bytes since each byte becomes 2 hex chars
	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	hexStr := hex.EncodeToString(bytes)
	// Return exactly the requested length
	if len(hexStr) > length {
		return hexStr[:length], nil
	}
	return hexStr, nil
}
