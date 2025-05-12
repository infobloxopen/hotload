package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyValueArgs(t *testing.T) {
	raw := []any{"a1", 1, "a2", 2, "a3", 3, "extra"}
	t.Logf("cap(raw)=%d, len(raw)=%d (%p)raw=%v", cap(raw), len(raw), raw, raw)
	assert.Equal(t, 7, cap(raw))
	assert.Equal(t, 7, len(raw))
	assert.Equal(t, "extra", raw[len(raw)-1])

	kva1 := KeyValueArgs(raw).Copy()
	t.Logf("cap(kva1)=%d, len(kva1)=%d (%p)kva1=%v", cap(kva1), len(kva1), kva1, kva1)
	assert.Equal(t, 6, cap(kva1))
	assert.Equal(t, 6, len(kva1))
	assert.Equal(t, 3, kva1[len(kva1)-1])

	kva1.AppendKVPairs("b4", 4, "b5", 5, "extra")
	t.Logf("cap(kva1)=%d, len(kva1)=%d (%p)kva1=%v", cap(kva1), len(kva1), kva1, kva1)
	assert.Equal(t, 12, cap(kva1))
	assert.Equal(t, 10, len(kva1))
	assert.Equal(t, 5, kva1[len(kva1)-1])

	t.Logf("cap(raw)=%d, len(raw)=%d (%p)raw=%v", cap(raw), len(raw), raw, raw)
}

func TestKeyValueMap(t *testing.T) {
	raw := map[string]any{"a1": 1, "a2": 2, "a3": 3}
	t.Logf("len(raw)=%d (%p)raw=%v", len(raw), raw, raw)
	assert.Equal(t, 3, len(raw))

	kva1 := KeyValueMap(raw).Copy()
	t.Logf("len(kva1)=%d (%p)kva1=%v", len(kva1), kva1, kva1)
	assert.Equal(t, 3, len(kva1))
	assert.Contains(t, kva1, "a3")

	kva1.MergeKVPairs("a3", 33, "b4", 4, "b5", 5, "extra")
	t.Logf("len(kva1)=%d (%p)kva1=%v", len(kva1), kva1, kva1)
	assert.Equal(t, 5, len(kva1))
	assert.Contains(t, kva1, "b5")
	assert.NotContains(t, kva1, "extra")
	assert.Equal(t, 33, kva1["a3"])

	kva1.Merge(KeyValueMap{"b5": 55, "c6": 6})
	t.Logf("len(kva1)=%d (%p)kva1=%v", len(kva1), kva1, kva1)
	assert.Equal(t, 6, len(kva1))
	assert.Contains(t, kva1, "c6")
	assert.Equal(t, 55, kva1["b5"])

	t.Logf("len(raw)=%d (%p)raw=%v", len(raw), raw, raw)
}
