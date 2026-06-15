package cmd

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

// kbpatchDecode reconstructs new = patch(old, delta) from a KBD1 delta. This is
// the Go twin of the engine's C kbpatch and of kbpatchEncode; the minter runs it
// to self-verify every diff before shipping (reconstruct == target, else fail).
func kbpatchDecode(old, delta []byte) ([]byte, error) {
	if len(delta) < 4+8*7 || string(delta[0:4]) != "KBD1" {
		return nil, fmt.Errorf("bad KBD1 header")
	}
	p := delta[4:]
	rd := func() uint64 { v := binary.LittleEndian.Uint64(p[:8]); p = p[8:]; return v }
	newLen := rd()
	cRaw := rd()
	cLen := rd()
	dRaw := rd()
	dLen := rd()
	eRaw := rd()
	eLen := rd()
	if uint64(len(p)) != cLen+dLen+eLen {
		return nil, fmt.Errorf("KBD1 stream length mismatch")
	}
	zc := p[:cLen]
	zd := p[cLen : cLen+dLen]
	ze := p[cLen+dLen:]

	control, err := kbInflate(zc, cRaw)
	if err != nil {
		return nil, fmt.Errorf("control inflate: %w", err)
	}
	diff, err := kbInflate(zd, dRaw)
	if err != nil {
		return nil, fmt.Errorf("diff inflate: %w", err)
	}
	extra, err := kbInflate(ze, eRaw)
	if err != nil {
		return nil, fmt.Errorf("extra inflate: %w", err)
	}

	out := make([]byte, newLen)
	var ctrlpos, diffpos, extrapos uint64
	var oldpos, newpos int64
	oldLen := int64(len(old))

	for uint64(newpos) < newLen {
		var ctrl [3]int64
		for i := 0; i < 3; i++ {
			if ctrlpos+8 > cRaw {
				return nil, fmt.Errorf("control overrun")
			}
			ctrl[i] = kbOfftin(control[ctrlpos:])
			ctrlpos += 8
		}
		if newpos+ctrl[0] > int64(newLen) {
			return nil, fmt.Errorf("diff segment overrun")
		}
		for i := int64(0); i < ctrl[0]; i++ {
			var ob byte
			if oldpos+i >= 0 && oldpos+i < oldLen {
				ob = old[oldpos+i]
			}
			out[newpos+i] = ob + diff[diffpos+uint64(i)]
		}
		diffpos += uint64(ctrl[0])
		newpos += ctrl[0]
		oldpos += ctrl[0]
		if newpos+ctrl[1] > int64(newLen) {
			return nil, fmt.Errorf("extra segment overrun")
		}
		for i := int64(0); i < ctrl[1]; i++ {
			out[newpos+i] = extra[extrapos+uint64(i)]
		}
		extrapos += uint64(ctrl[1])
		newpos += ctrl[1]
		oldpos += ctrl[2]
	}
	return out, nil
}

func kbInflate(src []byte, rawLen uint64) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := make([]byte, 0, rawLen)
	buf := bytes.NewBuffer(out)
	if _, err := io.Copy(buf, r); err != nil {
		return nil, err
	}
	if uint64(buf.Len()) != rawLen {
		return nil, fmt.Errorf("inflate length mismatch: got %d want %d", buf.Len(), rawLen)
	}
	return buf.Bytes(), nil
}

// kbOfftin: inverse of kbOfftout (sign-magnitude int64 little-endian).
func kbOfftin(buf []byte) int64 {
	y := int64(buf[7] & 0x7f)
	y = y*256 + int64(buf[6])
	y = y*256 + int64(buf[5])
	y = y*256 + int64(buf[4])
	y = y*256 + int64(buf[3])
	y = y*256 + int64(buf[2])
	y = y*256 + int64(buf[1])
	y = y*256 + int64(buf[0])
	if buf[7]&0x80 != 0 {
		y = -y
	}
	return y
}
