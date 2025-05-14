package logger

import (
	"fmt"
	"sort"
	"strings"
)

type KeyValueArgs []any

func (kvArgs KeyValueArgs) Copy() KeyValueArgs {
	// Use integer modulo math to ensure even number of args are copied
	evenLen := len(kvArgs) / 2 * 2
	newArr := make(KeyValueArgs, 0, evenLen)
	newArr = append(newArr, kvArgs[:evenLen]...)
	return newArr
}

func (kvArgs *KeyValueArgs) AppendKVPairs(keyValuePairs ...any) {
	// Use integer modulo math to ensure even number of args are added
	evenLen := len(keyValuePairs) / 2 * 2
	*kvArgs = append(*kvArgs, keyValuePairs[:evenLen]...)
}

type KVArgsTreeNode interface {
	GetParent() KVArgsTreeNode
	GetKVArgs() KeyValueArgs
}

// KVArgsTreeIterateAncestors is recursive fn to iterate thru KVArgs tree,
// starting with oldest ancestor first (ie: youngest ancestor overrides
// older ancestor keys)
func KVArgsTreeIterateAncestors(kvtn KVArgsTreeNode, iterateFn func(key, val any)) {
	if kvtn.GetParent() != nil {
		KVArgsTreeIterateAncestors(kvtn.GetParent(), iterateFn)
	}

	// Use integer modulo math to ensure even number of args are iterated
	kvArgs := kvtn.GetKVArgs()
	evenLen := len(kvArgs) / 2 * 2
	for i := 0; i < evenLen; i = i + 2 {
		kAny := kvArgs[i]
		vAny := kvArgs[i+1]
		iterateFn(kAny, vAny)
	}
}

type KeyValueMap map[string]any

func (kvMap *KeyValueMap) Merge(srcMap KeyValueMap) {
	for kStr, vAny := range srcMap {
		(*kvMap)[kStr] = vAny
	}
}

func (kvMap KeyValueMap) Copy() KeyValueMap {
	newMap := make(KeyValueMap)
	newMap.Merge(kvMap)
	return newMap
}

func (kvMap *KeyValueMap) MergeKVPairs(keyValuePairs ...any) {
	// Use integer modulo math to ensure even number of args are iterated
	evenLen := len(keyValuePairs) / 2 * 2
	for i := 0; i < evenLen; i = i + 2 {
		kAny := keyValuePairs[i]
		vAny := keyValuePairs[i+1]
		kStr, _ := AnyToString(kAny)
		(*kvMap)[kStr] = vAny
	}
}

// String implements fmt.Stringer interface for KeyValueMap.
// Returns stringified list of sorted key/value pairs.
// (Sorted for deterministic testing results)
func (kvMap KeyValueMap) String() string {
	keys := make([]string, 0, len(kvMap))
	for k := range kvMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for _, kStr := range keys {
		vAny := kvMap[kStr]
		vStr, _ := AnyToString(vAny)
		buf.WriteString(" ")
		buf.WriteString(kStr)
		buf.WriteString("=")
		buf.WriteString(vStr)
	}
	return buf.String()
}

func AnyToString(vAny any) (string, bool) {
	vStr, ok := vAny.(string)
	if ok {
		return vStr, ok
	}

	vStringer, ok := vAny.(fmt.Stringer)
	if ok {
		return vStringer.String(), ok
	}

	return fmt.Sprintf("%v", vAny), true
}

type KVMapTreeNode interface {
	GetParent() KVMapTreeNode
	GetKVMap() KeyValueMap
}

// KVMapTreeCopyMergeAncestors is recursive fn to copy/merge ancestor KVMaps,
// starting with oldest ancestor first (ie: youngest ancestor overrides
// older ancestor keys)
func KVMapTreeCopyMergeAncestors(kvtn KVMapTreeNode) KeyValueMap {
	var newMap KeyValueMap
	if kvtn.GetParent() == nil {
		newMap = kvtn.GetKVMap().Copy()
	} else {
		newMap = KVMapTreeCopyMergeAncestors(kvtn.GetParent())
		newMap.Merge(kvtn.GetKVMap())
	}
	return newMap
}
