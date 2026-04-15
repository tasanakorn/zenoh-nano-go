// Package kematch implements Zenoh key expression matching.
package kematch

import "strings"

// Match reports whether pattern matches key.
//
// Chunks are separated by '/'. The wildcards are:
//   - "*"  matches exactly one non-empty chunk.
//   - "**" matches zero or more chunks.
//
// Other chunks must match literally.
func Match(pattern, key string) bool {
	p := strings.Split(pattern, "/")
	k := strings.Split(key, "/")
	return matchChunks(p, k)
}

func matchChunks(p, k []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			// Try to consume any number of chunks (including zero).
			rest := p[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(k); i++ {
				if matchChunks(rest, k[i:]) {
					return true
				}
			}
			return false
		}
		if len(k) == 0 {
			return false
		}
		if !matchChunk(p[0], k[0]) {
			return false
		}
		p = p[1:]
		k = k[1:]
	}
	return len(k) == 0
}

func matchChunk(pc, kc string) bool {
	if pc == "*" {
		return kc != ""
	}
	return pc == kc
}
