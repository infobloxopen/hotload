package logger

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestV1LevelLogger(t *testing.T) {
	var buf strings.Builder
	v1logger := func(args ...any) {
		buf.WriteString(fmt.Sprintln(args...))
	}
	WithLogger(v1logger)
	stt := struct {
		integer int
	}{
		integer: 314159,
	}
	llg1 := WithKV("string-key", "i-should-be-overridden",
		"one-key", 1, "struct-key", stt, "extra-unpaired-key-llg1")
	llg2 := llg1.WithKV("string-key", "i-will-override-parent",
		"slice-key", []int{11, 22, 33}, "map-key", map[int]float32{
			0: 0.1,
			1: 1.1,
			2: 2.2,
		})
	llg2.DebugKV("debug-level-msg")
	llg2.ErrorKV(os.ErrPermission, "error-level-msg", "pointer-key", &llg1, "extra-unpaired-key-ErrorKV")

	loggedOutput := buf.String()
	t.Logf("loggedOutput=`%s`", loggedOutput)
	assert.NotContains(t, loggedOutput, "debug-level-msg")
	assert.NotContains(t, loggedOutput, "extra-unpaired-key-llg1")
	assert.NotContains(t, loggedOutput, "extra-unpaired-key-ErrorKV")
	assert.NotContains(t, loggedOutput, "i-should-be-overridden")
	assert.Contains(t, loggedOutput, "error-level-msg")
	assert.Contains(t, loggedOutput, "err=permission denied")
	assert.Contains(t, loggedOutput, "map-key=map[0:0.1 1:1.1 2:2.2]")
	assert.Contains(t, loggedOutput, "one-key=1")
	assert.Contains(t, loggedOutput, "pointer-key=0x")
	assert.Contains(t, loggedOutput, "slice-key=[11 22 33]")
	assert.Contains(t, loggedOutput, "i-will-override-parent")
	assert.Contains(t, loggedOutput, "struct-key={314159}")
}
