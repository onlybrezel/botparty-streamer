package main

import (
	"bytes"
	"io"
	"testing"
)

// makeStream concatenates byte slices separated by 3-byte Annex-B start codes.
func makeStream(nalus ...[]byte) []byte {
	var buf []byte
	for _, n := range nalus {
		buf = append(buf, 0x00, 0x00, 0x01)
		buf = append(buf, n...)
	}
	return buf
}

func TestNextNALU_SingleNALU(t *testing.T) {
	data := append([]byte{0x00, 0x00, 0x01, 0x41}, 0xDE, 0xAD)
	r := NewAnnexBReader(bytes.NewReader(data))
	nalu, err := r.NextNALU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(nalu, []byte{0x41, 0xDE, 0xAD}) {
		t.Fatalf("got %v, want [0x41 0xDE 0xAD]", nalu)
	}

	_, err = r.NextNALU()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestNextNALU_MultipleNALUs(t *testing.T) {
	nalu1 := []byte{0x67, 0xAA}
	nalu2 := []byte{0x68, 0xBB}
	nalu3 := []byte{0x41, 0xCC}
	data := makeStream(nalu1, nalu2, nalu3)

	r := NewAnnexBReader(bytes.NewReader(data))

	got1, err := r.NextNALU()
	if err != nil {
		t.Fatalf("nalu1: %v", err)
	}
	if !bytes.Equal(got1, nalu1) {
		t.Fatalf("nalu1: got %v, want %v", got1, nalu1)
	}

	got2, err := r.NextNALU()
	if err != nil {
		t.Fatalf("nalu2: %v", err)
	}
	if !bytes.Equal(got2, nalu2) {
		t.Fatalf("nalu2: got %v, want %v", got2, nalu2)
	}

	got3, err := r.NextNALU()
	if err != nil {
		t.Fatalf("nalu3: %v", err)
	}
	if !bytes.Equal(got3, nalu3) {
		t.Fatalf("nalu3: got %v, want %v", got3, nalu3)
	}

	_, err = r.NextNALU()
	if err != io.EOF {
		t.Fatalf("expected EOF after last nalu, got %v", err)
	}
}

func TestNextNALU_FourByteStartCode(t *testing.T) {
	// 4-byte start code: 0x00 0x00 0x00 0x01
	data := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0x00, 0x00, 0x01, 0x41, 0xBB}
	r := NewAnnexBReader(bytes.NewReader(data))

	got, err := r.NextNALU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, []byte{0x67, 0xAA}) {
		t.Fatalf("got %v, want [0x67 0xAA]", got)
	}
}

func TestNextNALU_EmptyStream(t *testing.T) {
	r := NewAnnexBReader(bytes.NewReader(nil))
	_, err := r.NextNALU()
	if err != io.EOF {
		t.Fatalf("expected EOF on empty stream, got %v", err)
	}
}

// ---------- findStartCode unit tests ----------

func TestFindStartCode_ThreeByte(t *testing.T) {
	b := []byte{0x00, 0x00, 0x01, 0x41}
	idx, length := findStartCode(b, 0)
	if idx != 0 || length != 3 {
		t.Fatalf("got idx=%d len=%d, want 0, 3", idx, length)
	}
}

func TestFindStartCode_FourByte(t *testing.T) {
	b := []byte{0x00, 0x00, 0x00, 0x01, 0x41}
	idx, length := findStartCode(b, 0)
	if idx != 0 || length != 4 {
		t.Fatalf("got idx=%d len=%d, want 0, 4", idx, length)
	}
}

func TestFindStartCode_NotFound(t *testing.T) {
	b := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	idx, _ := findStartCode(b, 0)
	if idx != -1 {
		t.Fatalf("expected -1, got %d", idx)
	}
}

func TestFindStartCode_FromOffset(t *testing.T) {
	// First code at 0, second at 5.
	b := []byte{0x00, 0x00, 0x01, 0xAA, 0xBB, 0x00, 0x00, 0x01, 0xCC}
	idx, length := findStartCode(b, 3)
	if idx != 5 || length != 3 {
		t.Fatalf("got idx=%d len=%d, want 5, 3", idx, length)
	}
}

// ---------- AccessUnitAssembler tests ----------

func TestAssembler_IDRFrame(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)

	sps := []byte{0x67, 0x42, 0x00, 0x1E} // nal_unit_type=7
	pps := []byte{0x68, 0xCE, 0x38, 0x80} // nal_unit_type=8
	idr := []byte{0x65, 0xB8}             // nal_unit_type=5

	if frame, _, flushed, _ := a.Push(sps); flushed || frame != nil {
		t.Fatal("SPS should not flush a frame")
	}
	if frame, _, flushed, _ := a.Push(pps); flushed || frame != nil {
		t.Fatal("PPS should not flush a frame")
	}

	// IDR slice — AUD is not in this stream so frame won't flush yet.
	a.Push(idr)

	// A second IDR should flush the first AU (new picture detected or uncertainty flush).
	idr2 := []byte{0x65, 0xB8}
	frame, key, flushed, _ := a.Push(idr2)
	if !flushed {
		t.Fatal("expected flush when second IDR VCL arrives while first IDR is pending")
	}
	if frame == nil {
		t.Fatal("expected non-nil frame data on flush")
	}
	_ = key
}

func TestAssembler_KeyFrameFlag(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)

	// Simulate AUD + SPS + PPS + IDR then AUD to flush.
	aud := []byte{0x09, 0xF0} // nal_unit_type=9
	sps := []byte{0x67, 0x00}
	pps := []byte{0x68, 0x00}
	idr := []byte{0x65, 0x00}

	a.Push(aud)
	a.Push(sps)
	a.Push(pps)
	a.Push(idr)

	// Second AUD triggers flush of first AU.
	frame, key, flushed, _ := a.Push(aud)
	if !flushed {
		t.Fatal("expected flush on second AUD")
	}
	if frame == nil {
		t.Fatal("expected non-nil frame data")
	}
	if !key {
		t.Fatal("IDR access unit should be marked as key frame")
	}
}

func TestAssembler_NonIDRFrame(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)

	aud := []byte{0x09, 0xF0}
	nonIdr := []byte{0x41, 0x00} // nal_unit_type=1

	a.Push(aud)
	a.Push(nonIdr)

	_, key, flushed, _ := a.Push(aud)
	if !flushed {
		t.Fatal("expected flush on second AUD")
	}
	if key {
		t.Fatal("non-IDR frame must not be marked as key frame")
	}
}

func TestAssembler_SPSPPSInjectedOnKeyFrame(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)

	sps := []byte{0x67, 0x42, 0x00, 0x1E}
	pps := []byte{0x68, 0x00}

	// First AU: AUD + SPS + PPS + IDR, flushed by second AUD.
	a.Push([]byte{0x09, 0xF0})
	a.Push(sps)
	a.Push(pps)
	a.Push([]byte{0x65, 0x00})
	a.Push([]byte{0x09, 0xF0}) // flush first AU

	// Second AU: IDR without SPS/PPS — assembler should prepend cached SPS/PPS.
	a.Push([]byte{0x65, 0x00})
	frame, key, flushed, _ := a.Push([]byte{0x09, 0xF0})
	if !flushed {
		t.Fatal("expected flush")
	}
	if !key {
		t.Fatal("expected key frame")
	}
	if !bytes.Contains(frame, sps) {
		t.Fatalf("expected SPS injected into keyframe output; got %d bytes", len(frame))
	}
	if !bytes.Contains(frame, pps) {
		t.Fatal("expected PPS injected into keyframe output")
	}
}

func TestAssembler_FlushFinal(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)
	a.Push([]byte{0x09, 0xF0})
	a.Push([]byte{0x65, 0x00})

	frame, key, flushed, reason := a.FlushFinal()
	if !flushed {
		t.Fatal("FlushFinal should flush pending VCL data")
	}
	if frame == nil {
		t.Fatal("expected non-nil frame")
	}
	if !key {
		t.Fatal("IDR frame should be key")
	}
	if reason != "eof" {
		t.Fatalf("expected reason 'eof', got %q", reason)
	}
}

func TestAssembler_FlushFinalEmptyNoOp(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)
	a.Push([]byte{0x09, 0xF0}) // AUD only, no VCL
	frame, _, flushed, _ := a.FlushFinal()
	if flushed || frame != nil {
		t.Fatal("FlushFinal without VCL data should be a no-op")
	}
}

func TestAssembler_LastSPSPPS(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)
	sps := []byte{0x67, 0xAA}
	pps := []byte{0x68, 0xBB}
	a.Push(sps)
	a.Push(pps)

	gotSPS, gotPPS := a.LastSPSPPS()
	if !bytes.Equal(gotSPS, sps) {
		t.Fatalf("SPS mismatch: got %v", gotSPS)
	}
	if !bytes.Equal(gotPPS, pps) {
		t.Fatalf("PPS mismatch: got %v", gotPPS)
	}
}

func TestAssembler_SeedSPSPPS(t *testing.T) {
	a := NewAccessUnitAssembler(64, 2*1024*1024)
	sps := []byte{0x67, 0xAA}
	pps := []byte{0x68, 0xBB}
	a.SeedSPSPPS(sps, pps)

	gotSPS, gotPPS := a.LastSPSPPS()
	if !bytes.Equal(gotSPS, sps) {
		t.Fatalf("seeded SPS mismatch: got %v", gotSPS)
	}
	if !bytes.Equal(gotPPS, pps) {
		t.Fatalf("seeded PPS mismatch: got %v", gotPPS)
	}

	// SeedSPSPPS must not overwrite existing entries.
	a.SeedSPSPPS([]byte{0x67, 0xFF}, []byte{0x68, 0xFF})
	gotSPS2, _ := a.LastSPSPPS()
	if !bytes.Equal(gotSPS2, sps) {
		t.Fatal("SeedSPSPPS must not overwrite existing SPS")
	}
}

// ---------- bitReader / RBSP helpers ----------

func TestDecodeRBSP_EscapeSequence(t *testing.T) {
	// 0x00 0x00 0x03 0xXX — the 0x03 emulation prevention byte must be removed.
	in := []byte{0x00, 0x00, 0x03, 0x01}
	out := decodeRBSP(in)
	want := []byte{0x00, 0x00, 0x01}
	if !bytes.Equal(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestDecodeRBSP_NoEscape(t *testing.T) {
	in := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	out := decodeRBSP(in)
	if !bytes.Equal(out, in) {
		t.Fatalf("got %v, want %v", out, in)
	}
}

func TestBitReader_ReadUE(t *testing.T) {
	cases := []struct {
		bits []byte
		want uint
	}{
		{[]byte{0b1_0000000}, 0},           // "1" → 0
		{[]byte{0b010_00000}, 1},           // "010" → 1
		{[]byte{0b011_00000}, 2},           // "011" → 2
		{[]byte{0b00100_000}, 3},           // "00100" → 3
	}
	for _, tc := range cases {
		br := newBitReader(tc.bits)
		v, ok := br.readUE()
		if !ok {
			t.Fatalf("readUE failed for input %08b", tc.bits[0])
		}
		if v != tc.want {
			t.Fatalf("got %d, want %d for input %08b", v, tc.want, tc.bits[0])
		}
	}
}
