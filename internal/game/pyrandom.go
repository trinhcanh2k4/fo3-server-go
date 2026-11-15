package game

// A Python-compatible random.Random for string seeds: MT19937 with
// CPython's version-2 seeding (int.from_bytes(seed + sha512(seed)),
// then init_by_array) and CPython's sample() pool algorithm. The
// weekly quest draw (fx._week_quests) seeds Random("quests/<week>")
// and the client-visible quest list must match it exactly.

import "crypto/sha512"

type PyRandom struct {
	mt    [624]uint32
	index int
}

func NewPyRandom(seedStr string) *PyRandom {
	r := &PyRandom{}
	seed := []byte(seedStr)
	digest := sha512.Sum512(seed)
	full := append(append([]byte{}, seed...), digest[:]...)
	// int.from_bytes(..., 'big'), then split into 32-bit words from
	// the least significant end (CPython's int seeding).
	n := len(full)
	key := []uint32{}
	for i := n; i > 0; i -= 4 {
		var w uint32
		start := i - 4
		if start < 0 {
			start = 0
		}
		for j := start; j < i; j++ {
			w = (w << 8) | uint32(full[j])
		}
		key = append(key, w)
	}
	if len(key) == 0 {
		key = []uint32{0}
	}
	r.initByArray(key)
	return r
}

func (r *PyRandom) initGenrand(s uint32) {
	r.mt[0] = s
	for i := 1; i < 624; i++ {
		r.mt[i] = 1812433253*(r.mt[i-1]^(r.mt[i-1]>>30)) + uint32(i)
	}
	r.index = 624
}

func (r *PyRandom) initByArray(key []uint32) {
	r.initGenrand(19650218)
	i, j := 1, 0
	k := 624
	if len(key) > k {
		k = len(key)
	}
	for ; k > 0; k-- {
		r.mt[i] = (r.mt[i] ^ ((r.mt[i-1] ^ (r.mt[i-1] >> 30)) * 1664525)) + key[j] + uint32(j)
		i++
		j++
		if i >= 624 {
			r.mt[0] = r.mt[623]
			i = 1
		}
		if j >= len(key) {
			j = 0
		}
	}
	for k := 623; k > 0; k-- {
		r.mt[i] = (r.mt[i] ^ ((r.mt[i-1] ^ (r.mt[i-1] >> 30)) * 1566083941)) - uint32(i)
		i++
		if i >= 624 {
			r.mt[0] = r.mt[623]
			i = 1
		}
	}
	r.mt[0] = 0x80000000
	r.index = 624
}

func (r *PyRandom) uint32() uint32 {
	if r.index >= 624 {
		var y uint32
		for i := 0; i < 624; i++ {
			y = (r.mt[i] & 0x80000000) | (r.mt[(i+1)%624] & 0x7FFFFFFF)
			v := r.mt[(i+397)%624] ^ (y >> 1)
			if y&1 == 1 {
				v ^= 0x9908B0DF
			}
			r.mt[i] = v
		}
		r.index = 0
	}
	y := r.mt[r.index]
	r.index++
	y ^= y >> 11
	y ^= (y << 7) & 0x9D2C5680
	y ^= (y << 15) & 0xEFC60000
	y ^= y >> 18
	return y
}

// getrandbits mirrors CPython for k <= 32.
func (r *PyRandom) getrandbits(k int) uint32 {
	if k == 0 {
		return 0
	}
	return r.uint32() >> (32 - k)
}

func (r *PyRandom) randbelow(n int) int {
	k := 0
	for (1 << k) < n {
		k++
	}
	// bit_length of n
	k = 0
	for m := n; m > 0; m >>= 1 {
		k++
	}
	for {
		v := int(r.getrandbits(k))
		if v < n {
			return v
		}
	}
}

// SamplePy returns k indices drawn exactly like
// random.Random(seed).sample(range(n), k) for small n (pool method).
func SamplePy(seedStr string, n, k int) []int {
	r := NewPyRandom(seedStr)
	pool := make([]int, n)
	for i := range pool {
		pool[i] = i
	}
	out := make([]int, k)
	for i := 0; i < k; i++ {
		j := r.randbelow(n - i)
		out[i] = pool[j]
		pool[j] = pool[n-i-1]
	}
	return out
}
