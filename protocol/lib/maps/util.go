package maps

import (
	"fmt"
	"sort"

	"golang.org/x/exp/constraints"
)

// MergeAllMapsMustHaveDistinctKeys merges all the maps into a single map.
// Panics if there are duplicate keys.
func MergeAllMapsMustHaveDistinctKeys[K comparable, V any](maps ...map[K]V) map[K]V {
	combinedMap := make(map[K]V)
	for _, m := range maps {
		for k, v := range m {
			if _, exists := combinedMap[k]; exists {
				panic(fmt.Sprintf("duplicate key: %v", k))
			}
			combinedMap[k] = v
		}
	}
	return combinedMap
}

// GetSortedKeys returns the keys of the map in sorted order.
func GetSortedKeys[K constraints.Ordered, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	return keys
}

// InvertMustHaveDistinctValues returns a new map with the keys and values of the input map swapped. If there are
// duplicate values in the input map, the method will panic.
func InvertMustHaveDistinctValues[K comparable, V comparable](m map[K]V) map[V]K {
	invert := make(map[V]K, len(m))
	for k, v := range m {
		if _, exists := invert[v]; exists {
			panic(fmt.Sprintf("duplicate map value: %v", v))
		}
		invert[v] = k
	}
	return invert
}