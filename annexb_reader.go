package main

import (
"bytes"
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
if startIdx >= 0 {
nextStartIdx, _ := findStartCode(r.buf, startIdx+startLen)
if nextStartIdx >= 0 {
nalu := bytes.Trim(r.buf[startIdx+startLen:nextStartIdx], "\x00")
r.buf = append(r.buf[:0], r.buf[nextStartIdx:]...)
if len(nalu) == 0 {
continue
}
return append([]byte(nil), nalu...), nil
}
}

if r.eof {
if startIdx >= 0 {
nalu := bytes.Trim(r.buf[startIdx+startLen:], "\x00")
r.buf = nil
if len(nalu) == 0 {
return nil, io.EOF
}
return append([]byte(nil), nalu...), nil
}
if len(bytes.Trim(r.buf, "\x00")) == 0 {
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
nalus    [][]byte
hasVCL   bool
hasIDR   bool
lastSPS  []byte
lastPPS  []byte
updated  time.Time
}

func NewAccessUnitAssembler() *AccessUnitAssembler {
return &AccessUnitAssembler{updated: time.Now()}
}

func (a *AccessUnitAssembler) Push(nalu []byte) ([]byte, bool, bool) {
if len(nalu) < 1 {
return nil, false, false
}
nType := nalu[0] & 0x1F
isVCL := nType >= 1 && nType <= 5

if nType == 9 {
if a.hasVCL {
frame, key := a.flushFrame()
a.resetState()
a.nalus = append(a.nalus, append([]byte(nil), nalu...))
a.updated = time.Now()
return frame, key, true
}
a.resetState()
a.nalus = append(a.nalus, append([]byte(nil), nalu...))
a.updated = time.Now()
return nil, false, false
}

if isVCL && a.hasVCL && startsNewPicture(nalu) {
frame, key := a.flushFrame()
a.resetState()
a.addNALU(nalu)
return frame, key, true
}

a.addNALU(nalu)
return nil, false, false
}

func (a *AccessUnitAssembler) FlushIfStale(maxAge time.Duration) ([]byte, bool, bool) {
if !a.hasVCL {
return nil, false, false
}
if time.Since(a.updated) < maxAge {
return nil, false, false
}
frame, key := a.flushFrame()
a.resetState()
return frame, key, true
}

func (a *AccessUnitAssembler) FlushFinal() ([]byte, bool, bool) {
if !a.hasVCL {
return nil, false, false
}
frame, key := a.flushFrame()
a.resetState()
return frame, key, true
}

func (a *AccessUnitAssembler) addNALU(nalu []byte) {
nType := nalu[0] & 0x1F
copyNALU := append([]byte(nil), nalu...)
a.nalus = append(a.nalus, copyNALU)
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

out := make([]byte, 0, 4096)
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
a.updated = time.Now()
}

func startsNewPicture(nalu []byte) bool {
if len(nalu) < 2 {
return false
}
nType := nalu[0] & 0x1F
if nType < 1 || nType > 5 {
return false
}

rbsp := make([]byte, 0, len(nalu)-1)
zeros := 0
for i := 1; i < len(nalu); i++ {
b := nalu[i]
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

br := newBitReader(rbsp)
firstMB, ok := br.readUE()
if !ok {
return false
}
return firstMB == 0
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
