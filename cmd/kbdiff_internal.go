package cmd

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
)

// kbdiffEncode produces a KBD1 delta such that kbpatch(old, delta) == new.
// Format (KBD1): magic "KBD1" | newLen u64 | cRaw u64 | cLen u64 | dRaw u64 |
// dLen u64 | eRaw u64 | eLen u64 | zlib(control)||zlib(diff)||zlib(extra).
// bsdiff-style suffix-array whole-file matching; streams raw-DEFLATE compressed (decoded by a vendored puff inflater in
// the engine — version-proof, no engine-zlib dependency). The engine's
// kbpatch reconstructs from these. Proven byte-exact against the C patcher.
func kbdiffEncode(old, new []byte) []byte {
	ctrl, diff, extra := kbBsdiff(old, new)
	zc := kbDeflate(ctrl)
	zd := kbDeflate(diff)
	ze := kbDeflate(extra)

	var out bytes.Buffer
	out.WriteString("KBD1")
	wu := func(v int) {
		var u8 [8]byte
		binary.LittleEndian.PutUint64(u8[:], uint64(v))
		out.Write(u8[:])
	}
	wu(len(new))
	wu(len(ctrl))
	wu(len(zc))
	wu(len(diff))
	wu(len(zd))
	wu(len(extra))
	wu(len(ze))
	out.Write(zc)
	out.Write(zd)
	out.Write(ze)
	return out.Bytes()
}

// kbDeflate raw-DEFLATEs b (no zlib header/adler) so the engine's vendored puff
// inflater can decode it without depending on the engine's zlib symbols/ABI —
// version-proof across Flutter engine builds.
func kbDeflate(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// ---- bsdiff core (qsufsort) ----

func kbSplit(I, V []int64, start, length, h int64) {
	var i, j, k, x, tmp, jj, kk int64
	if length < 16 {
		for k = start; k < start+length; k += j {
			j = 1
			x = V[I[k]+h]
			for i = 1; k+i < start+length; i++ {
				if V[I[k+i]+h] < x {
					x = V[I[k+i]+h]
					j = 0
				}
				if V[I[k+i]+h] == x {
					tmp = I[k+j]
					I[k+j] = I[k+i]
					I[k+i] = tmp
					j++
				}
			}
			for i = 0; i < j; i++ {
				V[I[k+i]] = k + j - 1
			}
			if j == 1 {
				I[k] = -1
			}
		}
		return
	}
	x = V[I[start+length/2]+h]
	jj = 0
	kk = 0
	for i = start; i < start+length; i++ {
		if V[I[i]+h] < x {
			jj++
		}
		if V[I[i]+h] == x {
			kk++
		}
	}
	jj += start
	kk += jj
	i = start
	j = 0
	k = 0
	for i < jj {
		if V[I[i]+h] < x {
			i++
		} else if V[I[i]+h] == x {
			tmp = I[i]
			I[i] = I[jj+j]
			I[jj+j] = tmp
			j++
		} else {
			tmp = I[i]
			I[i] = I[kk+k]
			I[kk+k] = tmp
			k++
		}
	}
	for jj+j < kk {
		if V[I[jj+j]+h] == x {
			j++
		} else {
			tmp = I[jj+j]
			I[jj+j] = I[kk+k]
			I[kk+k] = tmp
			k++
		}
	}
	if jj > start {
		kbSplit(I, V, start, jj-start, h)
	}
	for i = 0; i < kk-jj; i++ {
		V[I[jj+i]] = kk - 1
	}
	if kk-jj == 1 {
		I[jj] = -1
	}
	if start+length > kk {
		kbSplit(I, V, kk, start+length-kk, h)
	}
}

func kbQsufsort(old []byte) []int64 {
	oldsize := int64(len(old))
	buckets := make([]int64, 256)
	I := make([]int64, oldsize+1)
	V := make([]int64, oldsize+1)
	for _, c := range old {
		buckets[c]++
	}
	for i := int64(1); i < 256; i++ {
		buckets[i] += buckets[i-1]
	}
	for i := int64(255); i > 0; i-- {
		buckets[i] = buckets[i-1]
	}
	buckets[0] = 0
	for i := int64(0); i < oldsize; i++ {
		buckets[old[i]]++
		I[buckets[old[i]]] = i
	}
	I[0] = oldsize
	for i := int64(0); i < oldsize; i++ {
		V[i] = buckets[old[i]]
	}
	V[oldsize] = 0
	for i := int64(1); i < 256; i++ {
		if buckets[i] == buckets[i-1]+1 {
			I[buckets[i]] = -1
		}
	}
	I[0] = -1
	for h := int64(1); I[0] != -(oldsize + 1); h += h {
		var n int64
		i := int64(0)
		for i < oldsize+1 {
			if I[i] < 0 {
				n -= I[i]
				i -= I[i]
			} else {
				if n != 0 {
					I[i-n] = -n
				}
				n = V[I[i]] + 1 - i
				kbSplit(I, V, i, n, h)
				i += n
				n = 0
			}
		}
		if n != 0 {
			I[i-n] = -n
		}
	}
	for i := int64(0); i < oldsize+1; i++ {
		I[V[i]] = i
	}
	return I
}

func kbMatchlen(old, new []byte) int64 {
	var i int64
	for i < int64(len(old)) && i < int64(len(new)) {
		if old[i] != new[i] {
			break
		}
		i++
	}
	return i
}

func kbSearch(I []int64, old, new []byte, st, en int64) (int64, int64) {
	if en-st < 2 {
		x := kbMatchlen(old[I[st]:], new)
		y := kbMatchlen(old[I[en]:], new)
		if x > y {
			return x, I[st]
		}
		return y, I[en]
	}
	x := st + (en-st)/2
	if bytes.Compare(old[I[x]:kbMin(int64(len(old)), I[x]+int64(len(new)))], new[:kbMin(int64(len(new)), int64(len(old))-I[x])]) < 0 {
		return kbSearch(I, old, new, x, en)
	}
	return kbSearch(I, old, new, st, x)
}

func kbMin(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func kbOfftout(x int64, buf []byte) {
	var y int64
	if x < 0 {
		y = -x
	} else {
		y = x
	}
	for i := 0; i < 8; i++ {
		buf[i] = byte(y & 0xff)
		y >>= 8
	}
	if x < 0 {
		buf[7] |= 0x80
	}
}

func kbBsdiff(old, new []byte) ([]byte, []byte, []byte) {
	I := kbQsufsort(old)
	oldsize := int64(len(old))
	newsize := int64(len(new))

	var control, diff, extra bytes.Buffer
	var scan, pos, length int64
	var lastscan, lastpos, lastoffset int64

	for scan < newsize {
		var oldscore int64
		scan += length
		scsc := scan
		for scan < newsize {
			length, pos = kbSearch(I, old, new[scan:], 0, oldsize)
			for ; scsc < scan+length; scsc++ {
				if scsc+lastoffset < oldsize && old[scsc+lastoffset] == new[scsc] {
					oldscore++
				}
			}
			if (length == oldscore && length != 0) || length > oldscore+8 {
				break
			}
			if scan+lastoffset < oldsize && old[scan+lastoffset] == new[scan] {
				oldscore--
			}
			scan++
		}
		if length != oldscore || scan == newsize {
			var s, Sf, lenf int64
			for i := int64(0); lastscan+i < scan && lastpos+i < oldsize; {
				if old[lastpos+i] == new[lastscan+i] {
					s++
				}
				i++
				if s*2-i > Sf*2-lenf {
					Sf = s
					lenf = i
				}
			}
			var lenb int64
			if scan < newsize {
				var sb, Sb int64
				for i := int64(1); scan >= lastscan+i && pos >= i; i++ {
					if old[pos-i] == new[scan-i] {
						sb++
					}
					if sb*2-i > Sb*2-lenb {
						Sb = sb
						lenb = i
					}
				}
			}
			if lastscan+lenf > scan-lenb {
				overlap := (lastscan + lenf) - (scan - lenb)
				s = 0
				var Ss, lens int64
				for i := int64(0); i < overlap; i++ {
					if new[lastscan+lenf-overlap+i] == old[lastpos+lenf-overlap+i] {
						s++
					}
					if new[scan-lenb+i] == old[pos-lenb+i] {
						s--
					}
					if s > Ss {
						Ss = s
						lens = i + 1
					}
				}
				lenf += lens - overlap
				lenb -= lens
			}
			for i := int64(0); i < lenf; i++ {
				diff.WriteByte(new[lastscan+i] - old[lastpos+i])
			}
			for i := int64(0); i < (scan-lenb)-(lastscan+lenf); i++ {
				extra.WriteByte(new[lastscan+lenf+i])
			}
			var b [8]byte
			kbOfftout(lenf, b[:])
			control.Write(b[:])
			kbOfftout((scan-lenb)-(lastscan+lenf), b[:])
			control.Write(b[:])
			kbOfftout((pos-lenb)-(lastpos+lenf), b[:])
			control.Write(b[:])

			lastscan = scan - lenb
			lastpos = pos - lenb
			lastoffset = pos - scan
		}
	}
	return control.Bytes(), diff.Bytes(), extra.Bytes()
}
