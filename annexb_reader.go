package main

import (
"errors"
"fmt"
"io"
"time"
)

type EncodedFrame struct {
Data      []byte
Duration  time.Duration
KeyFrame  bool
CreatedAt time.Time
}

type AnnexBReader struct {
r   io.Reader
buf []byte
eof bool
}

func NewAnnexBReader(r io.Reader) *AnnexBReader {
return &AnnexBReader{r: r, buf: make([]byte, 0, 64*1024)}
}

func (r *AnnexBReader) NextNALU() ([]byte, error) {
for {
startIdx, startLen := findStartCode(r.buf, 0)
if startIdx > 0 {
// Keep parser aligned by dropping bytes before the first start code.
r.buf = append(r.buf[:0], r.buf[startIdx:]...)
startIdx = 0
}

if startIdx >= 0 {
nextStartIdx, _ := findStartCode(r.buf, startLen)
if nextStartIdx >= 0 {
nalu := append([]byte(nil), r.buf[startLen:nextStartIdx]...)
r.buf = append(r.buf[:0], r.buf[nextStartIdx:]...)
if len(nalu) == 0 {
continue
}
return nalu, nil
}
}

if r.eof {
if startIdx >= 0 {
nalu := append([]byte(nil), r.buf[startLen:]...)
r.buf = nil
if len(nalu) == 0 {
return nil, io.EOF
}
return nalu, nil
}
if len(r.buf) == 0 {
return nil, io.EOF
}
return nil, errors.New("annex-b stream ended without start code")
}

tmp := make([]byte, 16*1024)
n, err := r.r.Read(tmp)
if n > 0 {
r.buf = append(r.buf, tmp[:n]...)
if len(r.buf) > 8*1024*1024 {
return nil, fmt.Errorf("annex-b parser buffer exceeded 8MB; stream likely invalid")
}
}
if err != nil {
if errors.Is(err, io.EOF) {
r.eof = true
continue
}
return nil, err
}
}
}

func findStartCode(b []byte, from int) (int, int) {
if from < 0 {
from = 0
}
for i := from; i+3 < len(b); i++ {
if b[i] == 0x00 && b[i+1] == 0x00 {
if b[i+2] == 0x01 {
return i, 3
}
if i+3 < len(b) && b[i+2] == 0x00 && b[i+3] == 0x01 {
return i, 4
}
}
}
return -1, 0
}

type AccessUnitAssembler struct {
nalus   [][]byte
hasVCL  bool
hasIDR  bool
lastSPS []byte
lastPPS []byte
updated time.Time
auBytes int

maxNALUs int
maxBytes int
}

func NewAccessUnitAssembler(maxNALUs int, maxBytes int) *AccessUnitAssembler {
if maxNALUs <= 0 {
maxNALUs = 64
}
if maxBytes <= 0 {
maxBytes = 2 * 1024 * 1024
}
return &AccessUnitAssembler{
updated:  time.Now(),
maxNALUs: maxNALUs,
maxBytes: maxBytes,
}
}

func (a *AccessUnitAssembler) Push(nalu []byte) ([]byte, bool, bool, string) {
if len(nalu) < 1 {
return nil, false, false, ""
}

nType := nalu[0] & 0x1F
isVCL := nType >= 1 && nType <= 5

if nType == 9 {
if a.hasVCL {
frame, key := a.flushFrame()
a.resetState()
a.addNALU(nalu)
return frame, key, true, "aud"
}
a.resetState()
a.addNALU(nalu)
return nil, false, false, ""
}

if isVCL && a.hasVCL {
newPic, known := startsNewPicture(nalu)
if known && newPic {
frame, key := a.flushFrame()
a.resetState()
a.addNALU(nalu)
return frame, key, true, "new_picture"
}
if !known {
frame, key := a.flushFrame()
a.resetState()
a.addNALU(nalu)
return frame, key, true, "uncertain_slice_boundary"
}
}

a.addNALU(nalu)

if a.auBytes > a.maxBytes && a.hasVCL {
// Guard-flush: a partial AU flushed here may not be independently decodable
// unless it contains an IDR + SPS/PPS. flushFrame() prepends the cached
// SPS/PPS when hasIDR is set. Non-IDR partial flushes rely on the decoder
// reference frame buffer remaining valid; the waitForKeyframe path in
// main.go re-arms on the next error to recover gracefully.
frame, key := a.flushFrame()
a.resetState()
return frame, key, true, "max_au_bytes"
}
if len(a.nalus) > a.maxNALUs && a.hasVCL {
frame, key := a.flushFrame()
a.resetState()
return frame, key, true, "max_au_nalus"
}
if (a.auBytes > a.maxBytes || len(a.nalus) > a.maxNALUs) && !a.hasVCL {
a.resetState()
return nil, false, false, "non_vcl_guard_reset"
}

return nil, false, false, ""
}

func (a *AccessUnitAssembler) FlushIfStale(maxAge time.Duration) ([]byte, bool, bool, string) {
if !a.hasVCL {
return nil, false, false, ""
}
if time.Since(a.updated) < maxAge {
return nil, false, false, ""
}
frame, key := a.flushFrame()
a.resetState()
return frame, key, true, "stale_timeout"
}

func (a *AccessUnitAssembler) FlushFinal() ([]byte, bool, bool, string) {
	if !a.hasVCL {
		return nil, false, false, ""
	}
	frame, key := a.flushFrame()
	a.resetState()
	return frame, key, true, "eof"
}

// LastSPSPPS returns copies of the most recently seen SPS and PPS NALUs.
// Either or both may be nil if not yet observed.
func (a *AccessUnitAssembler) LastSPSPPS() (sps []byte, pps []byte) {
	if len(a.lastSPS) > 0 {
		sps = append([]byte(nil), a.lastSPS...)
	}
	if len(a.lastPPS) > 0 {
		pps = append([]byte(nil), a.lastPPS...)
	}
	return
}

// SeedSPSPPS pre-loads SPS/PPS into the assembler's cache if not already set.
// Used to carry SPS/PPS across ffmpeg connection restarts.
func (a *AccessUnitAssembler) SeedSPSPPS(sps, pps []byte) {
	if len(sps) > 0 && len(a.lastSPS) == 0 {
		a.lastSPS = append([]byte(nil), sps...)
	}
	if len(pps) > 0 && len(a.lastPPS) == 0 {
		a.lastPPS = append([]byte(nil), pps...)
	}
}

func (a *AccessUnitAssembler) addNALU(nalu []byte) {
nType := nalu[0] & 0x1F
copyNALU := append([]byte(nil), nalu...)
a.nalus = append(a.nalus, copyNALU)
a.auBytes += len(copyNALU)
a.updated = time.Now()

if nType == 7 {
a.lastSPS = append([]byte(nil), copyNALU...)
}
if nType == 8 {
a.lastPPS = append([]byte(nil), copyNALU...)
}
if nType >= 1 && nType <= 5 {
a.hasVCL = true
}
if nType == 5 {
a.hasIDR = true
}
}

func (a *AccessUnitAssembler) flushFrame() ([]byte, bool) {
key := a.hasIDR
nalus := a.nalus
if key {
hasSPS := false
hasPPS := false
for _, n := range nalus {
if len(n) == 0 {
continue
}
t := n[0] & 0x1F
if t == 7 {
hasSPS = true
}
if t == 8 {
hasPPS = true
}
}
prefix := make([][]byte, 0, 2)
if !hasSPS && len(a.lastSPS) > 0 {
prefix = append(prefix, append([]byte(nil), a.lastSPS...))
}
if !hasPPS && len(a.lastPPS) > 0 {
prefix = append(prefix, append([]byte(nil), a.lastPPS...))
}
if len(prefix) > 0 {
nalus = append(prefix, nalus...)
}
}

out := make([]byte, 0, a.auBytes+len(nalus)*4)
for _, n := range nalus {
if len(n) == 0 {
continue
}
out = append(out, 0x00, 0x00, 0x00, 0x01)
out = append(out, n...)
}
return out, key
}

func (a *AccessUnitAssembler) resetState() {
a.nalus = a.nalus[:0]
a.hasVCL = false
a.hasIDR = false
a.auBytes = 0
a.updated = time.Now()
}

func startsNewPicture(nalu []byte) (bool, bool) {
if len(nalu) < 2 {
return false, false
}
nType := nalu[0] & 0x1F
if nType < 1 || nType > 5 {
return false, false
}

rbsp := decodeRBSP(nalu[1:])
br := newBitReader(rbsp)
firstMB, ok := br.readUE()
if !ok {
return false, false
}
return firstMB == 0, true
}

func decodeRBSP(payload []byte) []byte {
rbsp := make([]byte, 0, len(payload))
zeros := 0
for i := 0; i < len(payload); i++ {
b := payload[i]
if zeros == 2 && b == 0x03 {
zeros = 0
continue
}
rbsp = append(rbsp, b)
if b == 0x00 {
zeros++
} else {
zeros = 0
}
}
return rbsp
}

type bitReader struct {
data []byte
pos  int
}

func newBitReader(data []byte) *bitReader {
return &bitReader{data: data}
}

func (b *bitReader) readBit() (uint8, bool) {
if b.pos >= len(b.data)*8 {
return 0, false
}
byteIdx := b.pos / 8
bitIdx := 7 - (b.pos % 8)
b.pos++
return (b.data[byteIdx] >> bitIdx) & 1, true
}

func (b *bitReader) readUE() (uint, bool) {
leadingZeroBits := 0
for {
bit, ok := b.readBit()
if !ok {
return 0, false
}
if bit == 0 {
leadingZeroBits++
if leadingZeroBits > 31 {
return 0, false
}
continue
}
break
}

var suffix uint
for i := 0; i < leadingZeroBits; i++ {
bit, ok := b.readBit()
if !ok {
return 0, false
}
suffix = (suffix << 1) | uint(bit)
}
return (1 << uint(leadingZeroBits)) - 1 + suffix, true
}
